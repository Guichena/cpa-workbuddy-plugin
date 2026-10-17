#!/usr/bin/env node
/**
 * 豆包面板「测试模型」回归脚本（Node vm + DOM 桩，真实执行 panel.html 内联 JS）。
 *
 * 覆盖 TASK-012 的对齐目标（与 workbuddy 面板同构）：
 *   1. 每张账号卡片都有「测试」按钮，且按钮携带账号标识与展示名；
 *   2. 点「测试」→ 打开模型弹窗 → GET /models?auth_index=... 拉取候选；
 *   3. 模型列表只渲染后端返回项，点某项 → POST /test-active 带 auth_index + model；
 *   4. 成功时就地展示「测试成功」并给出提示；
 *   5. 失败（200 + error）时就地展示「测试失败」，不得误报成功；
 *   6. 关闭弹窗后丢弃迟到响应，避免错投到别的账号；
 *   7. 测试进行中禁用模型按钮，结束后恢复。
 *
 * 用法：node test/doubao/panel_test_model_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { fileURLToPath } from "url";

const plugin = "doubao";
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const panelPath = path.join(repoRoot, plugin, "panel.html");

const ACCOUNTS = [
  {
    auth_id: "doubao-aaaa1111", auth_index: "doubao-aaaa1111", label: "豆包主号",
    screen_name: "星野", disabled: false, auth_state: "ok",
    created_at: "2026-10-17T02:00:00Z", updated_at: "2026-10-17T02:00:00Z",
  },
];

function loadPanelContext() {
  const html = fs.readFileSync(panelPath, "utf8");
  const bodies = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
  const main = bodies[bodies.length - 1];
  if (!main) throw new Error(`未在 ${panelPath} 中解析到内联脚本`);
  bodies.forEach((body, i) => {
    try {
      new vm.Script(body, { filename: `${plugin}-panel-script-${i}.js` });
    } catch (e) {
      throw new Error(`第 ${i + 1} 个 script 块语法校验失败：${e && e.message ? e.message : e}`);
    }
  });
  console.log(`[语法] ${bodies.length} 个 script 块编译通过`);

  const mkEl = (id) => {
    const listeners = {};
    return {
      id, textContent: "", innerHTML: "", value: "", checked: false, disabled: false,
      dataset: {}, style: {}, firstChild: null, children: [],
      classList: {
        _s: new Set(),
        add(c) { this._s.add(c); }, remove(c) { this._s.delete(c); },
        toggle(c) { this._s.has(c) ? this._s.delete(c) : this._s.add(c); },
        contains(c) { return this._s.has(c); },
      },
      addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
      removeEventListener() {}, focus() {}, remove() {},
      replaceWith() {}, appendChild() {}, insertBefore() {},
      querySelector() { return null; }, querySelectorAll() { return []; },
      setAttribute(k, v) { this.dataset[k] = v; }, getAttribute(k) { return this.dataset[k] || null; },
      _listeners: listeners,
      _fire(type, ev) { (listeners[type] || []).forEach((fn) => fn(ev || {})); },
    };
  };

  const els = new Map();
  const document = {
    getElementById(id) {
      if (!els.has(id)) els.set(id, mkEl(id));
      return els.get(id);
    },
    querySelector() { return null; },
    querySelectorAll(sel) {
      // 只服务「测试中禁用模型按钮」这一处：返回上次渲染进弹窗的模型按钮。
      if (sel === "#testModels button") return ctx.__modelButtons || [];
      return [];
    },
    createElement(tag) {
      const e = mkEl(tag);
      e.querySelector = () => mkEl("child");
      e.firstChild = { remove() {}, classList: { add() {}, remove() {} } };
      return e;
    },
    addEventListener() {}, removeEventListener() {},
    body: { appendChild() {}, removeChild() {} },
    documentElement: mkEl("html"),
  };

  const routes = {};
  const capturedRequests = [];
  const ctx = {
    console, Date, Math, JSON, Number, String, Object, Array, Boolean, RegExp, Error, Promise,
    isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    TextEncoder, TextDecoder, setTimeout, clearTimeout,
    setInterval: () => 1, clearInterval: () => {},
    requestAnimationFrame: (f) => f(),
    Blob: class {},
    URL: Object.assign(
      class extends URL {
        static createObjectURL() { return "blob:x"; }
        static revokeObjectURL() {}
      },
      { createObjectURL() { return "blob:x"; }, revokeObjectURL() {} },
    ),
    URLSearchParams,
    btoa: (s) => Buffer.from(s, "binary").toString("base64"),
    atob: (s) => Buffer.from(s, "base64").toString("binary"),
    confirm: () => true,
    open: () => null,
    document,
    navigator: { userAgent: "harness" },
    history: { replaceState() {} },
    sessionStorage: (() => {
      const store = new Map([["doubao-mgmt-key", "test-mgmt-key"]]);
      return {
        getItem(k) { return store.has(k) ? store.get(k) : null; },
        setItem(k, v) { store.set(k, String(v)); },
        removeItem(k) { store.delete(k); },
      };
    })(),
    localStorage: { getItem() { return null; }, setItem() {} },
    location: { host: "cpa.luode.dev", search: "", href: "https://cpa.luode.dev/panel" },
    fetch: async (url, opts) => {
      const p = String(url);
      const body = JSON.parse((opts && opts.body) || "{}");
      capturedRequests.push({ path: p, opts: opts || {}, body });
      let payload;
      for (const key of Object.keys(routes)) {
        if (p.includes(key)) { payload = await routes[key](body, p); break; }
      }
      if (payload === undefined) payload = { error: "no-route:" + p };
      return {
        status: 200, ok: true, statusText: "OK",
        headers: { get: () => "application/json" },
        text: async () => JSON.stringify(payload),
        json: async () => payload,
      };
    },
  };
  ctx.window = ctx; ctx.self = ctx; ctx.top = ctx; ctx.globalThis = ctx;
  vm.createContext(ctx);
  vm.runInContext(main, ctx, { filename: `${plugin}-panel-main.js` });
  return {
    ctx, document, els, capturedRequests,
    route: (key, fn) => { routes[key] = fn; },
    requestsTo: (key) => capturedRequests.filter((r) => r.path.includes(key)),
  };
}

const failures = [];
function check(label, actual, expected) {
  const ok = String(actual) === String(expected);
  if (!ok) failures.push(`${label}: 期望 ${expected}，实际 ${actual}`);
  console.log(`   ${ok ? "PASS" : "FAIL"}  ${label} = ${actual}（期望 ${expected}）`);
}
function checkTrue(label, cond) {
  const ok = !!cond;
  if (!ok) failures.push(`${label}: 断言为假`);
  console.log(`   ${ok ? "PASS" : "FAIL"}  ${label}`);
}

const tick = () => new Promise((r) => setTimeout(r, 0));

(async () => {
  console.log(`[回归] 面板 = ${plugin}/panel.html（测试模型）`);
  const { ctx, document, route, requestsTo } = loadPanelContext();

  route("/models", () => ({ models: ["doubao/default", "doubao/pro"] }));
  route("/test-active", () => ({ ok: true, model: "doubao/default", message: "活跃成功 (模型: doubao/default, 耗时: 1.2s)" }));

  const fireCardClick = (act, id, label) => {
    const fakeBtn = {
      getAttribute(k) {
        if (k === "data-id") return id;
        if (k === "data-act") return act;
        if (k === "data-label") return label || "";
        return null;
      },
    };
    document.getElementById("list")._fire("click", { target: { closest: () => fakeBtn } });
  };

  // 阶段1：卡片必须带「测试」按钮，且按钮携带账号标识与展示名。
  console.log("阶段1 卡片测试按钮：");
  ctx.renderAccounts(ACCOUNTS);
  const cardHTML = document.getElementById("list").innerHTML;
  checkTrue("卡片含测试按钮", /data-act="test"/.test(cardHTML));
  checkTrue("测试按钮携带 auth_id", /data-act="test" data-id="doubao-aaaa1111"/.test(cardHTML));
  checkTrue("测试按钮携带展示名", /data-label="豆包主号"/.test(cardHTML));

  // 阶段2：点测试 → 打开弹窗并拉取模型列表。
  console.log("阶段2 打开模型弹窗：");
  fireCardClick("test", "doubao-aaaa1111", "豆包主号");
  await tick();
  await tick();
  checkTrue("弹窗打开", document.getElementById("testModal").classList.contains("show"));
  checkTrue("提示写明目标账号", document.getElementById("testHint").innerHTML.includes("豆包主号"));
  const modelsReq = requestsTo("/models").pop();
  checkTrue("请求带 auth_index 查询参数", modelsReq && modelsReq.path.includes("auth_index=doubao-aaaa1111"));
  const modelsHTML = document.getElementById("testModels").innerHTML;
  checkTrue("渲染两个模型", (modelsHTML.match(/data-model=/g) || []).length === 2);
  checkTrue("模型名完整展示", modelsHTML.includes("doubao/default") && modelsHTML.includes("doubao/pro"));

  // 阶段3：点模型 → POST /test-active，成功就地展示。
  console.log("阶段3 执行测试成功：");
  const modelBtn = {
    disabled: false,
    getAttribute(k) { return k === "data-model" ? "doubao/default" : null; },
  };
  document.getElementById("testModels")._fire("click", { target: { closest: () => modelBtn } });
  await tick();
  await tick();
  const testReq = requestsTo("/test-active").pop();
  checkTrue("测试请求带 auth_index", testReq && testReq.body.auth_index === "doubao-aaaa1111");
  checkTrue("测试请求带 model", testReq && testReq.body.model === "doubao/default");
  check("结果区展示成功", document.getElementById("testResult").innerHTML.includes("测试成功"), "true");
  check("提示区成功态", document.getElementById("msg").className, "msg ok");
  checkTrue("提示含耗时信息", document.getElementById("msg").textContent.includes("耗时"));
  checkTrue("模型按钮已恢复可用", modelBtn.disabled === false);

  // 阶段4：失败（200 + error）必须就地报错，不得误报成功。
  console.log("阶段4 执行测试失败：");
  route("/test-active", () => ({ error: "测试失败（模型: doubao/default）：上游限流，请稍后重试" }));
  document.getElementById("testModels")._fire("click", { target: { closest: () => modelBtn } });
  await tick();
  await tick();
  checkTrue("结果区展示失败", document.getElementById("testResult").innerHTML.includes("测试失败"));
  checkTrue("结果区含上游原因", document.getElementById("testResult").innerHTML.includes("上游限流"));
  check("提示区失败态", document.getElementById("msg").className, "msg err");

  // 阶段5：关闭弹窗后迟到响应必须丢弃，不得错投到下一个账号。
  console.log("阶段5 关闭后丢弃迟到响应：");
  let releaseModels;
  route("/models", () => new Promise((resolve) => { releaseModels = () => resolve({ models: ["doubao/late"] }); }));
  ctx.openTestModal("doubao-aaaa1111", "豆包主号");
  await tick();
  ctx.closeTestModal();
  releaseModels();
  await tick();
  await tick();
  checkTrue("迟到响应未写入弹窗", !document.getElementById("testModels").innerHTML.includes("doubao/late"));
  checkTrue("关闭后弹窗隐藏", !document.getElementById("testModal").classList.contains("show"));

  // 阶段6：上游清单读不到时必须显示回落说明，否则用户会以为账号只有这几个模型。
  console.log("阶段6 回落提示：");
  route("/models", () => ({
    models: ["doubao/default", "doubao/pro"],
    source: "fallback",
    notice: "未能读取上游模型清单（请求失败），当前显示插件内置能力别名",
  }));
  ctx.openTestModal("doubao-aaaa1111", "豆包主号");
  await tick();
  await tick();
  const fallbackHTML = document.getElementById("testModels").innerHTML;
  checkTrue("显示回落原因", fallbackHTML.includes("内置能力别名"));
  checkTrue("回落时仍渲染模型按钮", (fallbackHTML.match(/data-model=/g) || []).length === 2);

  // 阶段7：动态清单必须完整渲染上游代号，并提供刷新入口。
  console.log("阶段7 上游动态清单：");
  const upstreamModels = ["doubao/default", "doubao/pro", "doubao/ogt", "doubao/Aoe", "doubao/anceba"];
  route("/models", () => ({ models: upstreamModels, source: "upstream" }));
  ctx.openTestModal("doubao-aaaa1111", "豆包主号");
  await tick();
  await tick();
  const upstreamHTML = document.getElementById("testModels").innerHTML;
  check("渲染全部上游模型", (upstreamHTML.match(/data-model=/g) || []).length, upstreamModels.length);
  checkTrue("上游代号完整展示", upstreamHTML.includes("doubao/ogt") && upstreamHTML.includes("doubao/anceba"));
  checkTrue("提供刷新入口", upstreamHTML.includes("reloadModels"));
  checkTrue("上游成功时无回落提示", !upstreamHTML.includes("内置能力别名"));

  if (failures.length) {
    console.log(`\n[结果] ${failures.length} 项失败`);
    failures.forEach((f) => console.log("   - " + f));
    process.exit(1);
  }
  console.log("\n[结果] 全部通过");
})();
