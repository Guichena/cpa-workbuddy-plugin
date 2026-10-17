#!/usr/bin/env node
/**
 * 豆包面板 management key 鉴权回归脚本（Node vm + DOM 桩，真实执行 panel.html 内联 JS）。
 *
 * 背景：宿主对 /v0/management/* 统一鉴权，不带 Authorization 时直接返回
 * 401 {"error":"missing management key"}。2026-10-17 生产上点击「导入」即报
 * 「导入失败：missing management key」——面板缺少 key 获取逻辑。
 *
 * 断言点：
 *   1. 无 key 时不得发出请求，且必须弹出密钥输入框并给出可操作提示；
 *   2. sessionStorage 已有 key 时，请求必须带 Authorization: Bearer；
 *   3. iframe 嵌入场景可从父窗口 localStorage（cli-proxy-auth）解出 key；
 *   4. URL ?key= 可作为来源，读取后从地址栏剔除并落 sessionStorage；
 *   5. 手动填入 key 后点「连接」写入 sessionStorage 并重新拉取账号；
 *   6. 401/403 时清 key、重新弹框，且错误提示不得是「未知错误」。
 *
 * 用法：node test/doubao/panel_auth_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { fileURLToPath } from "url";

const plugin = "doubao";
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const panelPath = path.join(repoRoot, plugin, "panel.html");

const ENC_PREFIX = "enc::v1::";
const SECRET_SALT = "cli-proxy-api-webui::secure-storage";

function b64encode(bytes) {
  return Buffer.from(bytes).toString("base64");
}
function xorBytes(data, key) {
  const out = new Uint8Array(data.length);
  for (let i = 0; i < data.length; i++) out[i] = data[i] ^ key[i % key.length];
  return out;
}
// 复刻面板的混淆格式，构造父窗口 localStorage 中真实的存储值。
function obfuscate(plain, host, ua) {
  const key = Buffer.from(`${SECRET_SALT}|${host}|${ua}`, "utf8");
  const data = Buffer.from(plain, "utf8");
  return ENC_PREFIX + b64encode(xorBytes(data, key));
}

function loadPanelContext(opts = {}) {
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

  const sessionStore = new Map();
  if (opts.sessionKey) sessionStore.set("doubao-mgmt-key", opts.sessionKey);
  const localStore = new Map();
  if (opts.panelKey) {
    localStore.set("cli-proxy-auth", obfuscate(
      JSON.stringify({ state: { managementKey: opts.panelKey } }),
      opts.host || "cpa.luode.dev",
      opts.ua || "harness",
    ));
  }

  const capturedRequests = [];
  const replacedUrls = [];
  let responseStatus = 200;
  let responsePayload = { accounts: [], count: 0 };

  const ctx = {
    console, Date, Math, JSON, Number, String, Object, Array, Boolean, RegExp, Error, Promise,
    isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    TextEncoder, TextDecoder, setTimeout, clearTimeout,
    setInterval: () => 1, clearInterval: () => {},
    requestAnimationFrame: (f) => f(),
    Blob: class {},
    // 保留真实 URL 构造器（readUrlKey 用它剔除 ?key=），只补对象 URL 两个静态方法。
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
    navigator: { userAgent: opts.ua || "harness" },
    history: { replaceState(_s, _t, url) { replacedUrls.push(String(url)); } },
    sessionStorage: {
      getItem(k) { return sessionStore.has(k) ? sessionStore.get(k) : null; },
      setItem(k, v) { sessionStore.set(k, String(v)); },
      removeItem(k) { sessionStore.delete(k); },
    },
    localStorage: {
      getItem(k) { return localStore.has(k) ? localStore.get(k) : null; },
      setItem(k, v) { localStore.set(k, String(v)); },
    },
    location: {
      host: opts.host || "cpa.luode.dev",
      search: opts.search || "",
      href: "https://" + (opts.host || "cpa.luode.dev") + "/panel" + (opts.search || ""),
      pathname: "/panel",
      hash: "",
    },
    fetch: async (url, reqOpts) => {
      capturedRequests.push({ path: String(url), opts: reqOpts || {} });
      return {
        status: responseStatus,
        ok: responseStatus >= 200 && responseStatus < 300,
        statusText: responseStatus === 200 ? "OK" : "ERR",
        headers: { get: () => "application/json" },
        text: async () => JSON.stringify(responsePayload),
        json: async () => responsePayload,
      };
    },
  };
  ctx.window = ctx;
  ctx.self = opts.embedded === false ? ctx : ctx;
  ctx.top = opts.embedded === false ? ctx : {};
  ctx.globalThis = ctx;
  vm.createContext(ctx);
  vm.runInContext(main, ctx, { filename: `${plugin}-panel-main.js` });

  return {
    ctx, document, sessionStore, capturedRequests, replacedUrls,
    setResponse(status, payload) { responseStatus = status; responsePayload = payload; },
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
  console.log(`[回归] 面板 = ${plugin}/panel.html（management key 鉴权）`);

  // 阶段1：无 key —— 不得发请求，必须弹框并给出可操作提示。
  console.log("阶段1 无 key 拦截：");
  {
    const { ctx, document, capturedRequests } = loadPanelContext();
    const msg = document.getElementById("msg");
    let err = null;
    try {
      await ctx.api("/accounts");
    } catch (e) {
      err = e;
    }
    check("无 key 时不发请求", capturedRequests.length, 0);
    checkTrue("抛出可操作错误", err && err.message.includes("管理密钥"));
    check("弹出密钥输入框", document.getElementById("authBox").style.display, "block");
    checkTrue("提示不是「未知错误」", !String(msg.textContent || "").includes("未知错误"));
  }

  // 阶段2：sessionStorage 已有 key —— 请求必须带 Bearer。
  console.log("阶段2 已有 key 注入请求头：");
  {
    const { ctx, capturedRequests } = loadPanelContext({ sessionKey: "sk-existing" });
    await ctx.api("/accounts");
    const req = capturedRequests.pop();
    check("Authorization 头", req.opts.headers.Authorization, "Bearer sk-existing");
    check("Content-Type 头", req.opts.headers["Content-Type"], "application/json");
  }

  // 阶段3：iframe 嵌入 + 父窗口 localStorage —— 解出 key 并落 sessionStorage。
  console.log("阶段3 父窗口 localStorage 兜底：");
  {
    const { ctx, capturedRequests, sessionStore } = loadPanelContext({ panelKey: "sk-from-parent" });
    await ctx.api("/accounts");
    const req = capturedRequests.pop();
    check("Authorization 头", req.opts.headers.Authorization, "Bearer sk-from-parent");
    check("已固化到 sessionStorage", sessionStore.get("doubao-mgmt-key"), "sk-from-parent");
  }

  // 阶段4：URL ?key= —— 读取后从地址栏剔除并落 sessionStorage。
  console.log("阶段4 URL key 兜底：");
  {
    const { ctx, capturedRequests, sessionStore, replacedUrls } = loadPanelContext({ search: "?key=sk-from-url&theme=dark" });
    await ctx.api("/accounts");
    const req = capturedRequests.pop();
    check("Authorization 头", req.opts.headers.Authorization, "Bearer sk-from-url");
    check("已固化到 sessionStorage", sessionStore.get("doubao-mgmt-key"), "sk-from-url");
    checkTrue("从地址栏剔除 key", replacedUrls.length > 0 && !String(replacedUrls[0]).includes("key="));
    checkTrue("保留 theme 参数", replacedUrls.length > 0 && String(replacedUrls[0]).includes("theme=dark"));
  }

  // 阶段5：手动填写 key 后点「连接」—— 写 sessionStorage 并重新拉取账号。
  console.log("阶段5 手动填入 key：");
  {
    const { ctx, document, capturedRequests, sessionStore } = loadPanelContext();
    document.getElementById("keyInput").value = "sk-manual";
    document.getElementById("keySave")._fire("click");
    await tick();
    check("已写入 sessionStorage", sessionStore.get("doubao-mgmt-key"), "sk-manual");
    check("关闭密钥输入框", document.getElementById("authBox").style.display, "none");
    checkTrue("触发账号拉取", capturedRequests.some((r) => r.path.includes("/accounts")));
    const req = capturedRequests.filter((r) => r.path.includes("/accounts")).pop();
    check("拉取请求带 Bearer", req.opts.headers.Authorization, "Bearer sk-manual");
  }

  // 阶段6：401 —— 清 key、重新弹框、错误可读。
  console.log("阶段6 401 处理：");
  {
    const { ctx, document, capturedRequests, sessionStore, setResponse } = loadPanelContext({ sessionKey: "sk-stale" });
    setResponse(401, { error: "missing management key" });
    // 首屏 refresh() 已用旧 key 发过一次请求；这里只观察显式调用是否被拦断。
    const before = capturedRequests.length;
    let err = null;
    try {
      await ctx.api("/accounts");
    } catch (e) {
      err = e;
    }
    checkTrue("401 抛错", !!err);
    checkTrue("错误说明可读", err && /管理密钥/.test(err.message));
    checkTrue("清掉失效 key", !sessionStore.has("doubao-mgmt-key"));
    check("重新弹出输入框", document.getElementById("authBox").style.display, "block");
    check("401 后不再自动重发", capturedRequests.length - before, 1);
  }

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
