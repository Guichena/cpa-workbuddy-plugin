#!/usr/bin/env node
/**
 * 豆包面板账号管理回归脚本（Node vm + DOM 桩，真实执行 panel.html 内联 JS）。
 *
 * 与 panel_import_repro.mjs 的分工：那个脚本覆盖「导入」链路，本脚本只覆盖
 * TASK-008 的「多账号启停 / 删除」链路，断言点对齐实施总览的五项通过标准：
 *   1. 两个账号均可在面板启停/删除（按钮 → 请求 → 刷新）；
 *   2. 禁用账号的卡片渲染为禁用态，启用账号渲染为启用态；
 *   3. 启停请求体携带 auth_id，删除前必须经 confirm 确认；
 *   4. 取消 confirm 时不发删除请求（防误删）；
 *   5. 操作失败时回显错误且不误报成功。
 *
 * 用法：node test/doubao/panel_accounts_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { fileURLToPath } from "url";

const plugin = "doubao";
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const panelPath = path.join(repoRoot, plugin, "panel.html");

// 夹具：两个账号，一个启用一个禁用，模拟「多账号池」最小形态。
const ACCOUNTS = [
  {
    auth_id: "doubao-aaaa1111", auth_index: "doubao-aaaa1111", label: "豆包主号",
    screen_name: "星野", disabled: false, auth_state: "ok",
    created_at: "2026-10-17T02:00:00Z", updated_at: "2026-10-17T02:00:00Z",
  },
  {
    auth_id: "doubao-bbbb2222", auth_index: "doubao-bbbb2222", label: "备用号",
    disabled: true, auth_state: "ok",
    created_at: "2026-10-17T02:10:00Z", updated_at: "2026-10-17T02:10:00Z",
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

  const routes = {};
  const capturedRequests = [];
  let confirmResult = true;
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
    confirm: (msg) => { ctx.__confirmMessage = msg; return confirmResult; },
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
    setConfirm: (v) => { confirmResult = v; },
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
  console.log(`[回归] 面板 = ${plugin}/panel.html`);
  const { ctx, document, route, requestsTo, setConfirm } = loadPanelContext();

  route("/accounts", () => ({ accounts: ACCOUNTS, count: ACCOUNTS.length }));
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

  // 阶段1：多账号渲染 —— 一启用一禁用，状态与按钮文案必须一致。
  console.log("阶段1 多账号渲染：");
  ctx.renderAccounts(ACCOUNTS);
  const html = document.getElementById("list").innerHTML;
  check("账号计数", document.getElementById("count").textContent, "共 2 个账号");
  checkTrue("两个账号都渲染", html.includes("doubao-aaaa1111") && html.includes("doubao-bbbb2222"));
  checkTrue("启用态徽章", html.includes("已启用"));
  checkTrue("禁用态徽章", html.includes("已禁用"));
  checkTrue("启用账号按钮为「禁用」", /data-act="disable" data-id="doubao-aaaa1111"/.test(html));
  checkTrue("禁用账号按钮为「启用」", /data-act="enable" data-id="doubao-bbbb2222"/.test(html));
  checkTrue("每个账号都有删除按钮", (html.match(/data-act="delete"/g) || []).length === 2);

  // 阶段2：禁用账号 → 点「启用」发 /enable，并触发账号刷新。
  console.log("阶段2 启用禁用账号：");
  const refreshBefore = requestsTo("/accounts").length;
  fireCardClick("enable", "doubao-bbbb2222");
  await tick();
  const enableReq = requestsTo("/enable").pop();
  checkTrue("点启用 → /enable 携带 auth_id", enableReq && enableReq.body.auth_id === "doubao-bbbb2222");
  checkTrue("操作成功后刷新账号列表", requestsTo("/accounts").length > refreshBefore);
  check("启用成功提示", document.getElementById("msg").textContent, "操作成功");
  check("启用成功态", document.getElementById("msg").className, "msg ok");

  // 阶段3：启用账号 → 点「禁用」发 /disable。
  console.log("阶段3 禁用启用账号：");
  fireCardClick("disable", "doubao-aaaa1111");
  await tick();
  const disableReq = requestsTo("/disable").pop();
  checkTrue("点禁用 → /disable 携带 auth_id", disableReq && disableReq.body.auth_id === "doubao-aaaa1111");

  // 阶段4：删除前必须 confirm；确认后才发 /delete。
  console.log("阶段4 删除确认：");
  setConfirm(true);
  fireCardClick("delete", "doubao-bbbb2222");
  await tick();
  const deleteReq = requestsTo("/delete").pop();
  checkTrue("确认后发 /delete 且携带 auth_id", deleteReq && deleteReq.body.auth_id === "doubao-bbbb2222");
  checkTrue("删除前弹出确认", String(ctx.__confirmMessage || "").includes("删除"));

  // 阶段5：取消 confirm 时不得删除（防误删）。
  console.log("阶段5 取消删除：");
  setConfirm(false);
  const deleteBefore = requestsTo("/delete").length;
  fireCardClick("delete", "doubao-aaaa1111");
  await tick();
  check("取消确认不发 /delete", requestsTo("/delete").length - deleteBefore, 0);

  // 阶段6：操作失败必须回显错误，不得误报成功。
  console.log("阶段6 操作失败回显：");
  route("/disable", () => ({ error: "账号不存在：doubao-aaaa1111" }));
  fireCardClick("disable", "doubao-aaaa1111");
  await tick();
  const msg = document.getElementById("msg");
  checkTrue("失败提示包含失败原因", msg.textContent.includes("操作失败") && msg.textContent.includes("账号不存在"));
  check("失败为错误态", msg.className, "msg err");

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
