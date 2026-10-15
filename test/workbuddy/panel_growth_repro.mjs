#!/usr/bin/env node
/**
 * WorkBuddy 面板「成长中心」按钮回归脚本（Node vm + DOM 桩，真实执行 panel.html 内联 JS）。
 *
 * 回归对象：CN 卡片新增的「成长中心」按钮（data-action=growth）与结果摘要行。
 * 期望行为：CN 卡片渲染该按钮、Global 卡片不渲染；点击后 POST /growth 且 body
 * 携带 auth_index；有收益时摘要为「+N 积分 / +M 能量 / K 任务」，无收益时
 * 显示「今日已领完」；返回 error 时摘要显示失败。
 *
 * 用法：
 *   node test/workbuddy/panel_growth_repro.mjs [panel-html-path]
 *
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { fileURLToPath } from "url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const panelPath = process.argv[2]
  ? path.resolve(process.argv[2])
  : path.join(repoRoot, "workbuddy", "panel.html");
const SS_KEY = "workbuddy-mgmt-key";
const CONTENT_TYPE = "application/json";

const cnAccount = {
  auth_index: "a1", auth_id: "acct-a1", nickname: "CN 账号", region: "cn",
  plan: "free", disabled: false, exhausted: false, test_failed: false,
  success: 1, failed: 0,
  credits: { total_remain: 50, total_used: 10, total_size: 60, pack_count: 1, packages: [{ name: "p" }] },
  checkin: { today_checked_in: false },
};
const globalAccount = Object.assign({}, cnAccount, {
  auth_index: "a2", auth_id: "acct-a2", nickname: "Global 账号", region: "global",
});

// 载入面板并构建 DOM / 存储 / fetch 桩，返回可观测的请求记录与元素访问器。
function loadPanelContext(growthPayload) {
  const html = fs.readFileSync(panelPath, "utf8");
  const bodies = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
  if (!bodies.length) throw new Error("未在 " + panelPath + " 中解析到内联脚本");
  bodies.forEach((body, i) => {
    try {
      new vm.Script(body, { filename: "wb-panel-script-" + i + ".js" });
    } catch (e) {
      throw new Error("第 " + (i + 1) + " 个 script 块语法校验失败：" + (e && e.message ? e.message : e));
    }
  });

  const fetches = [];
  const session = new Map([[SS_KEY, "TESTKEY"]]);
  const summaryEls = new Map();

  const mkEl = (id) => {
    const classes = new Set();
    return {
      id, textContent: "", innerHTML: "", value: "", checked: false, disabled: false,
      className: "", dataset: {}, style: {}, children: [],
      firstChild: { remove() {} },
      classList: {
        add(c) { classes.add(c); }, remove(c) { classes.delete(c); },
        toggle(c) { classes.has(c) ? classes.delete(c) : classes.add(c); },
        contains(c) { return classes.has(c); }, _has(c) { return classes.has(c); },
      },
      addEventListener() {}, removeEventListener() {}, focus() {}, remove() {},
      appendChild() {}, replaceChildren() {}, setAttribute() {}, getAttribute() { return null; },
      removeAttribute() {},
      querySelector(sel) { return sel === ".t" || sel === ".d" ? mkEl(id + sel) : null; },
      querySelectorAll() { return []; },
    };
  };
  const els = new Map();
  const getEl = (id) => { if (!els.has(id)) els.set(id, mkEl(id)); return els.get(id); };

  // 支持 [data-growth-summary="<idx>"] 属性选择器：growthOne 依赖它写摘要行。
  const growthSummaryEl = (selector) => {
    const m = /\[data-growth-summary="(.*)"\]/.exec(selector);
    if (!m) return null;
    const idx = m[1];
    if (!summaryEls.has(idx)) summaryEls.set(idx, mkEl("growth-summary-" + idx));
    return summaryEls.get(idx);
  };

  const document = {
    title: "",
    getElementById(id) { return getEl(id); },
    querySelector(selector) {
      if (selector.startsWith("#")) return getEl(selector.slice(1));
      if (selector.startsWith("[data-growth-summary=")) return growthSummaryEl(selector);
      return null;
    },
    querySelectorAll() { return []; },
    createElement(tag) { return mkEl(tag); },
    addEventListener() {}, removeEventListener() {},
    body: { appendChild() {}, removeChild() {} },
    documentElement: mkEl("html"),
  };

  const fetchStub = async (url, options) => {
    const u = String(url);
    const opt = options || {};
    fetches.push({ url: u, options: opt });
    let payload = {};
    if (u.indexOf("/growth") >= 0) {
      payload = growthPayload;
    } else if (u.indexOf("/accounts") >= 0) {
      payload = { accounts: [JSON.parse(JSON.stringify(cnAccount)), JSON.parse(JSON.stringify(globalAccount))], active_auth: "a1", active_id: "a1", server_time: "2026-10-15 10:00:00" };
    } else if (u.indexOf("/refresh/status") >= 0) {
      payload = { running: false, total: 0, done: 0, failed: 0, per_account: [] };
    } else if (u.indexOf("/refresh") >= 0) {
      payload = { running: false };
    }
    return {
      status: 200, statusText: "OK", ok: true,
      headers: { get: () => CONTENT_TYPE },
      text: async () => JSON.stringify(payload),
      json: async () => payload,
    };
  };

  const ctx = {
    console, Date, Math, JSON, Number, String, Object, Array, Boolean, RegExp, Error, Promise,
    isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    TextEncoder, TextDecoder, setTimeout, clearTimeout,
    setInterval: () => 0, clearInterval: () => 0,
    requestAnimationFrame: (f) => f(),
    Blob: class {},
    URL: { createObjectURL() { return "blob:x"; }, revokeObjectURL() {} },
    URLSearchParams,
    btoa: (s) => Buffer.from(s, "binary").toString("base64"),
    atob: (s) => Buffer.from(s, "base64").toString("binary"),
    CSS: { escape: (s) => String(s) },
    document,
    navigator: { userAgent: "harness", language: "zh-CN", languages: ["zh-CN"] },
    history: { replaceState() {} },
    sessionStorage: {
      getItem(key) { return session.has(key) ? session.get(key) : null; },
      setItem(key, value) { session.set(key, String(value)); },
      removeItem(key) { session.delete(key); },
    },
    localStorage: { getItem() { return null; }, setItem() {} },
    location: { host: "cpa.luode.dev", search: "", href: "https://cpa.luode.dev/panel" },
    matchMedia: () => ({ matches: false, addEventListener() {}, addListener() {} }),
    fetch: fetchStub,
  };
  ctx.window = ctx; ctx.self = ctx; ctx.top = ctx; ctx.globalThis = ctx;
  vm.createContext(ctx);
  bodies.forEach((body, i) => vm.runInContext(body, ctx, { filename: "wb-panel-script-" + i + ".js" }));
  return { ctx, els: getEl, summaryEls, fetches };
}

const failures = [];
function check(label, actual, expected) {
  const ok = String(actual) === String(expected);
  if (!ok) failures.push(label + "：期望 " + expected + "，实际 " + actual);
  console.log("   " + (ok ? "PASS" : "FAIL") + "  " + label + " = " + actual + "（期望 " + expected + "）");
}
const flush = () => new Promise((r) => setTimeout(r, 60));

(async () => {
  console.log("[回归] 面板 = " + panelPath);
  const html = fs.readFileSync(panelPath, "utf8");

  console.log("场景1 面板结构（成长中心按钮已接线）：");
  check("卡片模板含 data-action=growth", /data-action="growth"/.test(html), true);
  check("bindCardActions 分发 growthOne", /act==="growth"\)\s*growthOne\(/.test(html), true);
  check("存在 growthOne 函数", /async function growthOne\(/.test(html), true);
  check("摘要行容器存在", html.indexOf("data-growth-summary=") >= 0, true);

  console.log("场景2 CN 卡片渲染成长中心按钮、Global 卡片不渲染：");
  const s = loadPanelContext({ results: [{ auth_index: "a1", success: true, credits_gained: 30, energy_gained: 4, tasks_accepted: 1, tasks_claimed: 2 }], summary: {} });
  const cnHTML = s.ctx.card(JSON.parse(JSON.stringify(cnAccount)));
  const glHTML = s.ctx.card(JSON.parse(JSON.stringify(globalAccount)));
  check("CN 卡片含成长中心按钮", /data-action="growth"/.test(cnHTML), true);
  check("Global 卡片不含成长中心按钮", /data-action="growth"/.test(glHTML), false);
  check("Global 卡片仍是领取专家加油包", glHTML.indexOf("领取专家加油包") >= 0, true);

  console.log("场景3 点击后 POST /growth 并写入收益摘要：");
  await s.ctx.growthOne("a1", { dataset: {}, disabled: false });
  await flush();
  const growthCalls = s.fetches.filter((f) => f.url.indexOf("/growth") >= 0);
  check("已发起 /growth", growthCalls.length >= 1, true);
  check("请求携带 auth_index=a1", growthCalls.length ? String(growthCalls[0].options.body || "").indexOf('"auth_index":"a1"') >= 0 : false, true);
  check("摘要写入收益文案", s.summaryEls.get("a1").textContent, "成长中心：+30 积分 / +4 能量 / 3 任务");

  console.log("场景4 无收益时摘要显示「今日已领完」：");
  const s2 = loadPanelContext({ results: [{ auth_index: "a1", success: true, credits_gained: 0, energy_gained: 0 }], summary: {} });
  await s2.ctx.growthOne("a1", { dataset: {}, disabled: false });
  await flush();
  check("摘要写入今日已领完", s2.summaryEls.get("a1").textContent, "成长中心：今日已领完");

  console.log("场景5 返回 error 时摘要显示失败：");
  const s3 = loadPanelContext({ error: "no matching account" });
  await s3.ctx.growthOne("a1", { dataset: {}, disabled: false });
  await flush();
  check("摘要写入失败", s3.summaryEls.get("a1").textContent, "成长中心：失败");

  if (failures.length) {
    console.error("\n[回归] FAIL（" + failures.length + " 项）");
    failures.forEach((f) => console.error("   - " + f));
    process.exit(1);
  }
  console.log("\n[回归] PASS 全部断言通过");
  process.exit(0);
})().catch((e) => {
  console.error("[回归] 脚本异常：" + (e && e.stack ? e.stack : e));
  process.exit(1);
});
