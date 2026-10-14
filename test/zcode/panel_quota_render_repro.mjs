#!/usr/bin/env node
/**
 * ZCode 面板额度渲染回归脚本（Node vm + DOM 桩，真实执行 panel.html 内联 JS）。
 *
 * 回归对象：账号面板的额度可视化链路（0.1.12 参照 workbuddy 新增），
 * 以及 OAuth 授权站点选择链路（2026-10-14 新增 zai / bigmodel 双站）。
 * 覆盖断言：
 *   1. 两个 script 块语法可编译；
 *   2. 无额度缓存的账号卡片渲染「未领取」提示而非报错；
 *   3. 有额度快照的账号按每个模型渲染独立进度条、计划名徽章与用量文案；
 *   4. 汇总卡按「额度已获取 / 未知」口径统计，授予/剩余合计等于各账号之和；
 *   5. 额度排序（quota_asc / quota_desc）按 total_grant 生效，未知额度排最少端；
 *   6. 筛选标签计数与状态口径一致；
 *   7. 卡片按 provider_family 渲染站点归属徽标（国际站 / 中国区，缺省国际站）；
 *   8. 发起授权时把下拉框选中的 provider 透传给 /activate-start（缺省中国区）；
 *   9. 卡片不展示「过期」行（冗余信息，2026-10-15 移除）。
 *
 * 用法：node test/zcode/panel_quota_render_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { fileURLToPath } from "url";

const plugin = "zcode";
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const panelPath = path.join(repoRoot, plugin, "panel.html");

// 夹具：3 个账号 —— A 有额度且带真实用量(balance 通道，300万+500万)、
// B 有额度但无用量(current 回退通道，100万，中国区授权)、C 无额度缓存。
// A/C 不带 provider_family，用于验证历史账号缺省按国际站展示。
const ACCOUNTS = [
  {
    auth_id: "zcode-aaa", auth_index: "zcode-aaa", label: "账号A", disabled: false,
    test_failed: false, cooling_down: false, created_at: "2026-10-01T00:00:00Z",
    models: ["GLM-5.3", "GLM-5.3-Flash"],
    quota: {
      plan_name: "ZCode Start Plan", plan_id: "p1", plan_status: "active",
      total_grant: 8000000, total_used: 5000019, total_remain: 2999981,
      has_usage: true, source: "balance", fetched_at: "2026-10-12T05:00:00Z",
      entitlements: [
        { show_name: "GLM-5.3", grant_units: 3000000, remaining_units: 2999981, used_units: 19, has_usage: true, period: "daily", capability: "model:glm-5.3" },
        { show_name: "GLM-5.3-Flash", grant_units: 5000000, remaining_units: 0, used_units: 5000000, has_usage: true, period: "daily", capability: "model:glm-5.3-flash" },
      ],
    },
  },
  {
    auth_id: "zcode-bbb", auth_index: "zcode-bbb", label: "账号B", disabled: true,
    test_failed: false, cooling_down: false, created_at: "2026-10-02T00:00:00Z",
    provider_family: "bigmodel",
    models: ["GLM-5.2"],
    quota: {
      plan_name: "ZCode Start Plan", plan_id: "p1", plan_status: "active",
      total_grant: 1000000, has_usage: false, source: "current", fetched_at: "2026-10-12T05:00:00Z",
      entitlements: [{ show_name: "GLM-5.2", grant_units: 1000000, has_usage: false, period: "daily", capability: "model:glm-5.2" }],
    },
  },
  {
    auth_id: "zcode-ccc", auth_index: "zcode-ccc", label: "账号C", disabled: false,
    test_failed: true, cooling_down: false, created_at: "2026-10-03T00:00:00Z",
    models: ["GLM-5.3"],
    quota: null,
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
    // toast() 依赖 box.children.length 与 box.firstChild.remove() 做上限裁剪。
    children: [],
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
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
      // toast() 会 el.querySelector(".t")/.d 取子节点，桩需返回可用元素。
      e.querySelector = () => mkEl("child");
      e.firstChild = { remove() {}, classList: { add() {}, remove() {} } };
      return e;
    },
    addEventListener() {}, removeEventListener() {},
    body: { appendChild() {}, removeChild() {} },
    documentElement: mkEl("html"),
  };

  let payload;
  // capturedRequests 记录 fetch 实际发出的请求，供断言 /activate-start 的 provider 载荷。
  const capturedRequests = [];
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
    navigator: { userAgent: "harness" },
    history: { replaceState() {} },
    sessionStorage: { getItem() { return "KEY"; }, setItem() {}, removeItem() {} },
    localStorage: { getItem() { return null; }, setItem() {} },
    location: { host: "cpa.luode.dev", search: "", href: "https://cpa.luode.dev/panel" },
    fetch: async (url, opts) => {
      const path = String(url);
      capturedRequests.push({ path, opts: opts || {} });
      // payload 由 setPayload 注入（测试在 loadPanelContext 之后调用）；boot 阶段的
      // enterPanel 会先打一次 /accounts，此时 payload 仍为空，返回空账号列表兜底，
      // 避免桩环境下未捕获异常掩盖真实断言结果。
      let body;
      if (path.includes("/accounts")) body = payload || { accounts: [], count: 0 };
      else if (path.includes("/activate-start")) body = { ok: true, state: "st-1", authorize_url: "https://example.test/auth?x=1" };
      else body = { error: "n/a" };
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
  return { ctx, document, setPayload: (p) => { payload = p; }, capturedRequests };
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
  const { ctx, document, setPayload, capturedRequests } = loadPanelContext();
  const el = (id) => document.getElementById(id);

  setPayload({ accounts: ACCOUNTS, count: ACCOUNTS.length });
  await ctx.load();
  console.log("阶段1 加载后：");

  // 卡片渲染：有额度的账号必须按每个模型渲染独立进度条与计划名徽章；无额度的显示未领取提示。
  const cardA = ctx.card(ACCOUNTS[0]);
  const cardB = ctx.card(ACCOUNTS[1]);
  const cardC = ctx.card(ACCOUNTS[2]);
  checkTrue("有额度卡片含进度条", cardA.includes('class="pb"'));
  checkTrue("有额度卡片含额度段 GLM-5.3", cardA.includes("GLM-5.3"));
  checkTrue("有额度卡片含计划名徽章", cardA.includes('class="badge plan"'));
  // 逐模型独立进度条：A 有 2 个 entitlement 且都带用量 → 2 条 pb-item。
  check("A 逐模型进度条数", (cardA.match(/class="pb-item"/g) || []).length, "2");
  checkTrue("A 展示 GLM-5.3 剩余/授予", cardA.includes("剩 3.00M / 3.00M"));
  checkTrue("A 展示 GLM-5.3-Flash 剩余/授予", cardA.includes("剩 0 / 5.00M"));
  checkTrue("A 耗尽模型进度条转红", cardA.includes('class="pb-bar err"'));
  // 回退通道（无用量）模型单独一条半透明满格条，不误读为满额。
  check("B 回退通道进度条数", (cardB.match(/class="pb-item"/g) || []).length, "1");
  checkTrue("B 无用量标注授予量", cardB.includes("授予 1.00M"));
  checkTrue("B 无用量不展示剩余", !cardB.includes("剩 "));
  checkTrue("无额度卡片显示未领取提示", cardC.includes("未领取"));
  checkTrue("无额度卡片不含计划名徽章", !cardC.includes('class="badge plan"'));
  // 冗余信息移除：卡片不再渲染「过期」行。
  checkTrue("卡片不展示过期行", !cardA.includes(">过期<"));

  // 汇总：额度已获取 2 / 未知 1，授予合计 900 万；剩余合计仅统计有用量账号（A: 2999981）。
  console.log("阶段2 汇总卡：");
  const summary = el("summaryBox").innerHTML;
  checkTrue("汇总含「今日授予合计」", summary.includes("今日授予合计"));
  checkTrue("汇总显示合计 9.00M", summary.includes("9.00M"));
  checkTrue("汇总显示已获取 2 / 未知 1", summary.includes("2 / 未知 1"));
  checkTrue("汇总含今日剩余合计", summary.includes("今日剩余合计"));

  // 筛选计数：可用 1（A）、禁用 1（B）、测试 1（C）。
  console.log("阶段3 筛选计数：");
  check("cntAll", el("cntAll").textContent, "3");
  check("cntAvailable", el("cntAvailable").textContent, "1");
  check("cntDisabled", el("cntDisabled").textContent, "1");
  check("cntTest", el("cntTest").textContent, "1");

  // 排序：额度升序时未知额度(-1)排最前，其次 B(100万)，最后 A(800万)。
  console.log("阶段4 额度排序：");
  vm.runInContext("currentSort='quota_asc'", ctx);
  const asc = ctx.sortedAccounts(ACCOUNTS).map((a) => a.auth_id);
  check("quota_asc 首位", asc[0], "zcode-ccc");
  check("quota_asc 末位", asc[2], "zcode-aaa");
  vm.runInContext("currentSort='quota_desc'", ctx);
  const desc = ctx.sortedAccounts(ACCOUNTS).map((a) => a.auth_id);
  check("quota_desc 首位", desc[0], "zcode-aaa");
  check("quota_desc 末位", desc[2], "zcode-ccc");

  // 额度数字格式化边界。
  console.log("阶段5 数字格式化：");
  check("fmtCompact 8000000", ctx.fmtCompact(8000000), "8.00M");
  check("fmtCompact 1500", ctx.fmtCompact(1500), "1.5K");
  check("fmtCompact 999", ctx.fmtCompact(999), "999");
  check("fmtNum 3000000", ctx.fmtNum(3000000), "3,000,000");

  // 站点归属徽标：B 为中国区，A 缺省（历史账号）按国际站。
  console.log("阶段6 授权站点归属徽标：");
  checkTrue("中国区卡片含归属徽标", cardB.includes('class="badge provider bigmodel"'));
  checkTrue("中国区卡片标注「中国区」", cardB.includes("中国区"));
  checkTrue("国际站卡片含归属徽标", cardA.includes('class="badge provider"'));
  checkTrue("国际站卡片标注「国际站」", cardA.includes("国际站"));
  checkTrue("历史账号缺省不渲染中国区", !cardA.includes("bigmodel"));
  check("providerBadge(bigmodel)", ctx.providerBadge({ provider_family: "bigmodel" }).includes("中国区"), "true");
  check("providerBadge(缺省)", ctx.providerBadge({}).includes("国际站"), "true");

  // 授权站点透传：下拉框选中值必须进入 /activate-start 的 JSON 载荷；空值缺省中国区。
  console.log("阶段7 授权 provider 透传：");
  el("oauthProvider").value = "zai";
  await ctx.openOAuthBridge();
  const startReq = capturedRequests.filter((r) => r.path.includes("/activate-start")).pop();
  checkTrue("已发出 /activate-start 请求", !!startReq);
  check("授权载荷 provider(zai)", JSON.parse(startReq.opts.body).provider, "zai");
  el("oauthProvider").value = "";
  await ctx.openOAuthBridge();
  const startReq2 = capturedRequests.filter((r) => r.path.includes("/activate-start")).pop();
  check("空选值缺省 provider", JSON.parse(startReq2.opts.body).provider, "bigmodel");

  console.log("");
  if (failures.length) {
    console.log(`[结果] FAIL（${failures.length} 项）`);
    failures.forEach((f) => console.log("   - " + f));
    process.exit(1);
  }
  console.log("[结果] PASS 全部断言通过");
})();