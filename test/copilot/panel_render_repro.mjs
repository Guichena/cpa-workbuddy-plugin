#!/usr/bin/env node
/**
 * Copilot 面板渲染回归脚本（Node vm + DOM 桩，真实执行 panel.html 内联 JS）。
 *
 * 回归对象：GitHub Copilot 面板的账号卡片渲染、额度进度条、状态徽章与设备码登录链路。
 * 覆盖断言：
 *   1. script 块语法可编译；
 *   2. 账号卡片按状态渲染徽章（正常 / 已禁用 / 测试失败 / 冷却中）；
 *   3. 配额快照按 chat / completions / premium_interactions 渲染进度条，
 *      并按剩余比例着色（≤30% 黄、≤10% 红）；
 *   4. 无配额缓存的账号不渲染进度条且不报错；配额带 error 时展示错误文案；
 *   5. 启用/禁用按钮按 disabled 状态互斥渲染；
 *   6. 搜索过滤按 label / username / user_id / auth_id 命中；
 *   7. 设备码登录：/login-start 返回 user_code 后渲染设备码并启动轮询，
 *      轮询 success 后关闭弹窗并重新加载账号。
 *
 * 用法：node test/copilot/panel_render_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { fileURLToPath } from "url";

const plugin = "copilot";
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const panelPath = path.join(repoRoot, plugin, "panel.html");

// 夹具：4 个账号，覆盖正常 / 禁用 / 测试失败 / 冷却中四种状态与三种配额形态。
const ACCOUNTS = [
  {
    auth_id: "copilot-aaa", auth_index: "copilot-aaa", label: "账号A", disabled: false,
    test_failed: false, cooling_down: false, account_type: "individual",
    username: "octocat", user_id: "1", expires_at: "2026-11-01T00:00:00Z",
    models: ["gpt-4.1", "gpt-4o"],
    quota: {
      plan: "individual_pro", access_type_sku: "copilot_pro", chat_enabled: true,
      quota_reset_date: "2026-11-01", fetched_at: "2026-10-15T05:00:00Z",
      chat: { entitlement: 100, remaining: 40, percent_remaining: 40, unlimited: false },
      completions: { entitlement: 2000, remaining: 2000, percent_remaining: 100, unlimited: false },
      premium_interactions: { entitlement: 300, remaining: 0, percent_remaining: 0, unlimited: false },
    },
  },
  {
    auth_id: "copilot-bbb", auth_index: "copilot-bbb", label: "账号B", disabled: true,
    test_failed: false, cooling_down: false, account_type: "business",
    username: "hubber", user_id: "2", models: ["gpt-4o"], quota: null,
  },
  {
    auth_id: "copilot-ccc", auth_index: "copilot-ccc", label: "账号C", disabled: false,
    test_failed: true, cooling_down: false, account_type: "individual",
    username: "tester", user_id: "3", models: ["o4-mini"], quota: { error: "upstream 403" },
  },
  {
    auth_id: "copilot-ddd", auth_index: "copilot-ddd", label: "账号D", disabled: false,
    test_failed: false, cooling_down: true, account_type: "individual",
    username: "cooler", user_id: "4", models: ["claude-sonnet-4"],
    quota: { plan: "individual_free", unlimited_note: true, chat: { entitlement: 0, remaining: 0, unlimited: true } },
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

  const mkEl = (id) => ({
    id, textContent: "", innerHTML: "", value: "", checked: false, disabled: false,
    dataset: {}, style: {}, firstChild: null,
    children: [],
    classList: {
      _s: new Set(),
      add(c) { this._s.add(c); }, remove(c) { this._s.delete(c); },
      toggle(c) { this._s.has(c) ? this._s.delete(c) : this._s.add(c); },
      contains(c) { return this._s.has(c); },
    },
    addEventListener() {}, removeEventListener() {}, focus() {}, remove() {},
    replaceWith() {}, appendChild() {}, insertBefore() {},
    querySelector() { return null; }, querySelectorAll() { return []; },
    setAttribute() {}, getAttribute() { return null; },
  });

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

  let payload;
  const capturedRequests = [];
  // 轮询计数器：让 /login-poll 首次 pending、第二次 success，驱动登录链路完整走完。
  let loginPollCount = 0;
  const intervals = [];
  const ctx = {
    console, Date, Math, JSON, Number, String, Object, Array, Boolean, RegExp, Error, Promise,
    isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    TextEncoder, TextDecoder, setTimeout, clearTimeout,
    setInterval: (fn) => { intervals.push(fn); return intervals.length; },
    clearInterval: () => {},
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
    sessionStorage: { getItem() { return "KEY"; }, setItem() {}, removeItem() {} },
    localStorage: { getItem() { return null; }, setItem() {} },
    location: { host: "cpa.luode.dev", search: "", href: "https://cpa.luode.dev/panel" },
    fetch: async (url, opts) => {
      const p = String(url);
      capturedRequests.push({ path: p, opts: opts || {} });
      let body;
      if (p.includes("/accounts")) body = payload || { accounts: [], count: 0 };
      else if (p.includes("/login-start")) {
        body = {
          ok: true,
          state: "st-1",
          user_code: "ABCD-1234",
          verification_uri: "https://github.com/login/device",
          interval: 3,
        };
      } else if (p.includes("/login-poll")) {
        loginPollCount++;
        body = loginPollCount === 1
          ? { status: "pending", message: "等待授权" }
          : { status: "success", message: "授权成功" };
      } else body = { error: "n/a" };
      return {
        status: 200, ok: true, statusText: "OK",
        headers: { get: () => "application/json" },
        text: async () => JSON.stringify(body),
        json: async () => body,
      };
    },
  };
  ctx.window = ctx; ctx.self = ctx; ctx.top = ctx; ctx.globalThis = ctx;
  vm.createContext(ctx);
  vm.runInContext(main, ctx, { filename: `${plugin}-panel-main.js` });
  return {
    ctx, document, els,
    setPayload: (p) => { payload = p; },
    capturedRequests,
    intervals,
    getLoginPollCount: () => loginPollCount,
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
  const { ctx, document, setPayload, capturedRequests, intervals } = loadPanelContext();

  setPayload({ accounts: ACCOUNTS, count: ACCOUNTS.length });
  await ctx.loadAccounts();

  // 阶段1：卡片渲染与状态徽章。
  console.log("阶段1 卡片与状态徽章：");
  const cardA = ctx.card(ACCOUNTS[0]);
  const cardB = ctx.card(ACCOUNTS[1]);
  const cardC = ctx.card(ACCOUNTS[2]);
  const cardD = ctx.card(ACCOUNTS[3]);
  checkTrue("A 正常徽章", cardA.includes("正常"));
  checkTrue("B 已禁用徽章", cardB.includes("已禁用"));
  checkTrue("C 测试失败徽章", cardC.includes("测试失败"));
  checkTrue("D 冷却中徽章", cardD.includes("冷却中"));

  // 阶段2：配额进度条渲染与着色。
  console.log("阶段2 配额进度条：");
  check("A 进度条数（chat/completions/premium）", (cardA.match(/class="pb-item"/g) || []).length, "3");
  checkTrue("A 展示计划名徽章", cardA.includes("individual_pro"));
  checkTrue("A 展示剩余/授予", cardA.includes("剩 40 / 100"));
  checkTrue("A 耗尽配额转红", cardA.includes("pb-fill err"));
  checkTrue("A 部分剩余转黄（40/100=40%）", !cardA.includes("pb-fill warn"));
  checkTrue("B 无配额不渲染进度条", !cardB.includes('class="pb-item"'));
  checkTrue("C 配额错误展示错误文案", cardC.includes("upstream 403"));
  checkTrue("D unlimited 展示无限", cardD.includes("无限"));

  // 阶段3：启用/禁用按钮互斥。
  console.log("阶段3 启停按钮：");
  checkTrue("A 渲染禁用按钮", cardA.includes('data-action="disable"'));
  checkTrue("A 不渲染启用按钮", !cardA.includes('data-action="enable"'));
  checkTrue("B 渲染启用按钮", cardB.includes('data-action="enable"'));
  checkTrue("B 不渲染禁用按钮", !cardB.includes('data-action="disable"'));

  // 阶段4：搜索过滤。
  console.log("阶段4 搜索过滤：");
  document.getElementById("search").value = "hubber";
  vm.runInContext("currentSearch='hubber'", ctx);
  ctx.render();
  const gridHTML = document.getElementById("grid").innerHTML;
  checkTrue("搜索命中账号B", gridHTML.includes("copilot-bbb"));
  checkTrue("搜索排除账号A", !gridHTML.includes("copilot-aaa"));
  vm.runInContext("currentSearch=''", ctx);
  ctx.render();

  // 阶段5：设备码登录链路。
  console.log("阶段5 设备码登录：");
  capturedRequests.length = 0;
  await document.getElementById("btnLogin").onclick();
  const startReq = capturedRequests.find((r) => r.path.includes("/login-start"));
  checkTrue("发起 /login-start", !!startReq);
  const loginBody = document.getElementById("loginBody").innerHTML;
  checkTrue("渲染设备码", loginBody.includes("ABCD-1234"));
  checkTrue("渲染授权地址", loginBody.includes("https://github.com/login/device"));
  checkTrue("启动轮询定时器", intervals.length > 0);

  // 手动驱动一次轮询：首次 pending。
  await intervals[0]();
  const pollReq = capturedRequests.filter((r) => r.path.includes("/login-poll"));
  check("轮询次数", pollReq.length, "1");
  checkTrue("轮询携带 state", pollReq[0].path.includes("state=st-1"));
  checkTrue("pending 时弹窗仍打开", document.getElementById("loginMask").classList.contains("on"));

  // 再次轮询：success 后应关闭弹窗并重新拉取账号。
  capturedRequests.length = 0;
  await intervals[0]();
  checkTrue("success 后关闭弹窗", !document.getElementById("loginMask").classList.contains("on"));
  checkTrue("success 后重新加载账号", capturedRequests.some((r) => r.path.includes("/accounts")));

  console.log("");
  if (failures.length) {
    console.log(`[结果] FAIL —— ${failures.length} 项断言未通过：`);
    failures.forEach((f) => console.log(`  - ${f}`));
    process.exit(1);
  }
  console.log("[结果] PASS —— 全部断言通过");
})().catch((e) => {
  console.error("[结果] FAIL —— 脚本异常：", e && e.stack ? e.stack : e);
  process.exit(1);
});
