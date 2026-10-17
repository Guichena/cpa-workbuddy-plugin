#!/usr/bin/env node
/**
 * 豆包面板登录态导入回归脚本（Node vm + DOM 桩，真实执行 panel.html 内联 JS）。
 *
 * 回归对象：豆包 Provider 面板的登录态导入、账号卡片渲染与启停/删除链路。
 * 覆盖断言：
 *   1. script 块语法可编译；
 *   2. 空 Cookie 由前端直接拦下，不发起 /import 请求；
 *   3. 合法 Cookie → POST /import → 清空输入框 + 提示成功 + 触发账号刷新；
 *   4. 垃圾 Cookie → POST /import 返回 error → 提示失败且不清空输入框（防假成功）；
 *   5. 账号卡片按 disabled 渲染启用/禁用徽章与按钮文案，昵称/手机行按字段有无条件渲染；
 *   6. 空列表渲染空状态占位；
 *   7. 卡片按钮：禁用态点「启用」发 /enable，启用态点「禁用」发 /disable，
 *      「删除」经 confirm 后发 /delete，请求体均携带 auth_id。
 *
 * 用法：node test/doubao/panel_import_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { fileURLToPath } from "url";

const plugin = "doubao";
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const panelPath = path.join(repoRoot, plugin, "panel.html");

// 夹具：3 个账号，覆盖启用 / 禁用 / 字段缺失三种形态。
const ACCOUNTS = [
  {
    auth_id: "doubao-aaaa1111", auth_index: "doubao-aaaa1111", label: "豆包主号",
    screen_name: "星野", mobile: "184****9548", sec_user_id: "sec_abc***",
    disabled: false, created_at: "2026-10-17T02:00:00Z", updated_at: "2026-10-17T02:00:00Z",
  },
  {
    auth_id: "doubao-bbbb2222", auth_index: "doubao-bbbb2222", label: "备用号",
    disabled: true, created_at: "2026-10-17T02:10:00Z", updated_at: "2026-10-17T02:10:00Z",
  },
  {
    auth_id: "doubao-cccc3333", auth_index: "doubao-cccc3333", label: "",
    screen_name: "无名氏", disabled: false,
    created_at: "2026-10-17T02:20:00Z", updated_at: "2026-10-17T02:20:00Z",
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
      setAttribute() {}, getAttribute() { return null; },
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
    querySelectorAll() { return []; },
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

  // 每个路径一个响应体生成器，由用例按需替换。
  const routes = {};
  const capturedRequests = [];
  const ctx = {
    console, Date, Math, JSON, Number, String, Object, Array, Boolean, RegExp, Error, Promise,
    isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    TextEncoder, TextDecoder, setTimeout, clearTimeout,
    setInterval: () => 1, clearInterval: () => {},
    requestAnimationFrame: (f) => f(),
    Blob: class {},
    URL: { createObjectURL() { return "blob:x"; }, revokeObjectURL() {} },
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
        if (p.includes(key)) { payload = routes[key](body); break; }
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

(async () => {
  console.log(`[回归] 面板 = ${plugin}/panel.html`);
  const { ctx, document, route, requestsTo } = loadPanelContext();

  route("/accounts", () => ({ accounts: ACCOUNTS, count: ACCOUNTS.length }));

  // 阶段1：空 Cookie 前端拦截，不发起请求。
  console.log("阶段1 空 Cookie 前端拦截：");
  document.getElementById("cookie").value = "   ";
  const beforeEmpty = requestsTo("/import").length;
  await ctx.doImport();
  check("空 Cookie 不发起 /import", requestsTo("/import").length - beforeEmpty, 0);
  checkTrue("空 Cookie 提示", document.getElementById("msg").textContent.includes("请先粘贴 Cookie"));
  check("空 Cookie 提示为错误态", document.getElementById("msg").className, "msg err");

  // 阶段2：垃圾 Cookie 必须被拒绝（防假成功），且不清空输入框。
  console.log("阶段2 垃圾 Cookie 拒绝：");
  route("/import", () => ({ error: "登录态无效：判据端点返回 error" }));
  document.getElementById("cookie").value = "sessionid=fake; flow_cur_user_sec_id=fake";
  document.getElementById("label").value = "";
  await ctx.doImport();
  const garbageReq = requestsTo("/import").pop();
  check("垃圾 Cookie 发起 1 次 /import", requestsTo("/import").length, 1);
  checkTrue("垃圾 Cookie 请求体携带原 Cookie",
    garbageReq && garbageReq.body.cookie === "sessionid=fake; flow_cur_user_sec_id=fake");
  checkTrue("垃圾 Cookie 提示失败", document.getElementById("msg").textContent.includes("导入失败"));
  checkTrue("垃圾 Cookie 失败原因回显", document.getElementById("msg").textContent.includes("登录态无效"));
  check("垃圾 Cookie 失败为错误态", document.getElementById("msg").className, "msg err");
  check("垃圾 Cookie 不清空输入框", document.getElementById("cookie").value,
    "sessionid=fake; flow_cur_user_sec_id=fake");

  // 阶段3：合法 Cookie 导入成功 → 清空输入 + 成功提示 + 刷新账号。
  console.log("阶段3 合法 Cookie 导入成功：");
  route("/import", () => ({ ok: true, auth_id: "doubao-aaaa1111", label: "豆包主号", message: "登录态导入成功" }));
  document.getElementById("cookie").value = "sessionid=good; flow_cur_user_sec_id=good";
  document.getElementById("label").value = "豆包主号";
  await ctx.doImport();
  const okReq = requestsTo("/import").pop();
  checkTrue("合法 Cookie 请求体携带 label", okReq && okReq.body.label === "豆包主号");
  checkTrue("合法 Cookie 提示成功", document.getElementById("msg").textContent.includes("导入成功"));
  check("合法 Cookie 成功态", document.getElementById("msg").className, "msg ok");
  check("合法 Cookie 清空输入框", document.getElementById("cookie").value, "");
  check("合法 Cookie 清空备注", document.getElementById("label").value, "");
  check("合法 Cookie 触发账号刷新", requestsTo("/accounts").length >= 1, true);

  // 阶段4：账号卡片渲染。
  console.log("阶段4 账号卡片渲染：");
  ctx.renderAccounts(ACCOUNTS);
  const html = document.getElementById("list").innerHTML;
  check("账号计数文案", document.getElementById("count").textContent, "共 3 个账号");
  checkTrue("启用徽章", html.includes("已启用"));
  checkTrue("禁用徽章", html.includes("已禁用"));
  checkTrue("启用态按钮显示「禁用」", html.includes(">禁用<"));
  checkTrue("禁用态按钮显示「启用」", html.includes(">启用<"));
  checkTrue("昵称行渲染", html.includes("星野"));
  checkTrue("手机行渲染", html.includes("184****9548"));
  checkTrue("空 label 回落到 auth_id", html.includes("doubao-cccc3333"));
  checkTrue("卡片按钮带 auth_id", html.includes('data-id="doubao-aaaa1111"'));
  checkTrue("卡片按钮带动作类型", html.includes('data-act="delete"'));

  // 阶段5：空列表占位。
  console.log("阶段5 空列表占位：");
  ctx.renderAccounts([]);
  checkTrue("空列表占位", document.getElementById("list").innerHTML.includes("暂无账号"));
  check("空列表计数", document.getElementById("count").textContent, "共 0 个账号");

  // 阶段6：卡片按钮事件链（toggle / delete）。
  console.log("阶段6 卡片按钮事件链：");
  route("/enable", () => ({ ok: true }));
  route("/disable", () => ({ ok: true }));
  route("/delete", () => ({ ok: true }));

  const fireCardClick = (act, id) => {
    const fakeBtn = {
      getAttribute(k) {
        if (k === "data-id") return id;
        if (k === "data-act") return act;
        return null;
      },
    };
    document.getElementById("list")._fire("click", { target: { closest: () => fakeBtn } });
  };

  fireCardClick("enable", "doubao-bbbb2222");
  await new Promise((r) => setTimeout(r, 0));
  const enableReq = requestsTo("/enable").pop();
  checkTrue("禁用账号点启用 → /enable 携带 auth_id",
    enableReq && enableReq.body.auth_id === "doubao-bbbb2222");

  fireCardClick("disable", "doubao-aaaa1111");
  await new Promise((r) => setTimeout(r, 0));
  const disableReq = requestsTo("/disable").pop();
  checkTrue("启用账号点禁用 → /disable 携带 auth_id",
    disableReq && disableReq.body.auth_id === "doubao-aaaa1111");

  fireCardClick("delete", "doubao-cccc3333");
  await new Promise((r) => setTimeout(r, 0));
  const deleteReq = requestsTo("/delete").pop();
  checkTrue("点删除 → /delete 携带 auth_id",
    deleteReq && deleteReq.body.auth_id === "doubao-cccc3333");

  console.log("");
  if (failures.length) {
    console.log(`[回归] 失败 ${failures.length} 项：`);
    failures.forEach((f) => console.log(`   - ${f}`));
    process.exit(1);
  }
  console.log("[回归] 全部断言通过");
  process.exit(0);
})().catch((e) => {
  console.error("[回归] 脚本异常：", e);
  process.exit(1);
});
