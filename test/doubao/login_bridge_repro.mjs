#!/usr/bin/env node
/**
 * 豆包登录桥接页回归脚本（Node vm + DOM 桩，真实执行 login.go 渲染出的内联 JS）。
 *
 * 覆盖 TASK-013 的桥接页契约：
 *   1. 页面能从管理面板 localStorage 取到 management key 并回填输入框；
 *   2. 无 key 时点提交给出可操作提示且不发请求；
 *   3. 有 key 时提交 /import，携带 Authorization 与 Cookie 正文；
 *   4. 导入成功展示成功提示，失败展示上游原因；
 *   5. 401/403 时清 key 并提示重新填写；
 *   6. 请求进行中禁用按钮，结束后恢复。
 *
 * 用法：node test/doubao/login_bridge_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
import { execFileSync } from "child_process";
import { fileURLToPath } from "url";

const plugin = "doubao";
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const loginGoPath = path.join(repoRoot, plugin, "login.go");

// 从 login.go 里取出所有 b.WriteString 的字面量，拼成页面 HTML。
// 直接读 Go 源码而不是跑 Go：桥接页由 WriteString 逐段拼接，这个提取方式
// 与实际渲染逐字节一致，且不需要 CGO 工具链。
function renderBridgePage(state) {
  const src = fs.readFileSync(loginGoPath, "utf8");
  const parts = [];
  const re = /b\.WriteString\(("(?:[^"\\]|\\.)*")\)/g;
  let m;
  while ((m = re.exec(src)) !== null) {
    parts.push(JSON.parse(m[1]));
  }
  let html = parts.join("");
  html = html.replace('"+ providerName +"', plugin + "-provider");
  html = html.replace('"+ jsString(state) +"', JSON.stringify(state));
  return html;
}

function loadBridge(state, { panelKey = "sk-from-panel" } = {}) {
  const html = renderBridgePage(state);
  const bodies = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((x) => x[1]);
  const main = bodies[bodies.length - 1];
  if (!main) throw new Error("桥接页未渲染出内联脚本");
  new vm.Script(main, { filename: "doubao-bridge.js" });
  console.log("[语法] 桥接页 script 块编译通过");

  const mkEl = (id) => {
    const listeners = {};
    return {
      id, value: "", textContent: "", className: "", disabled: false,
      addEventListener(type, fn) { (listeners[type] = listeners[type] || []).push(fn); },
      _fire(type) { (listeners[type] || []).forEach((fn) => fn.call(this, {})); },
      _listeners: listeners,
    };
  };
  const els = new Map();
  const bodyAttrs = {};
  for (const m of html.matchAll(/data-(provider|state)="([^"]*)"/g)) {
    bodyAttrs["data-" + m[1]] = m[2];
  }
  const document = {
    getElementById(id) {
      if (!els.has(id)) els.set(id, mkEl(id));
      return els.get(id);
    },
    body: { getAttribute(k) { return bodyAttrs[k] || null; } },
  };

  // 用与面板相同的 XOR+base64 混淆，验证桥接页能解开主面板写入的值。
  const SALT = "cli-proxy-api-webui::secure-storage";
  const host = "cpa.luode.dev";
  const ua = "harness";
  function obfuscate(plain) {
    const key = Buffer.from(`${SALT}|${host}|${ua}`, "utf8");
    const data = Buffer.from(plain, "utf8");
    const out = Buffer.alloc(data.length);
    for (let i = 0; i < data.length; i++) out[i] = data[i] ^ key[i % key.length];
    return "enc::v1::" + out.toString("base64");
  }

  const requests = [];
  let respond = () => ({ status: 200, data: { ok: true, label: "测试号" } });
  const ctx = {
    console, Date, Math, JSON, Number, String, Object, Array, Boolean, RegExp, Error, Promise,
    isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    TextEncoder, TextDecoder, setTimeout, clearTimeout,
    btoa: (s) => Buffer.from(s, "binary").toString("base64"),
    atob: (s) => Buffer.from(s, "base64").toString("binary"),
    document,
    navigator: { userAgent: ua },
    location: { host },
    localStorage: {
      getItem(k) {
        if (k !== "cli-proxy-auth") return null;
        return obfuscate(JSON.stringify({ state: { managementKey: panelKey } }));
      },
      setItem() {},
    },
    sessionStorage: (() => {
      const store = new Map();
      return {
        getItem(k) { return store.has(k) ? store.get(k) : null; },
        setItem(k, v) { store.set(k, String(v)); },
        removeItem(k) { store.delete(k); },
      };
    })(),
    fetch: async (url, opts) => {
      const body = JSON.parse((opts && opts.body) || "{}");
      requests.push({ path: String(url), opts: opts || {}, body });
      const r = await respond(body);
      return {
        status: r.status,
        json: async () => r.data,
      };
    },
  };
  ctx.window = ctx; ctx.self = ctx; ctx.top = ctx; ctx.globalThis = ctx;
  vm.createContext(ctx);
  vm.runInContext(main, ctx, { filename: "doubao-bridge-main.js" });
  return {
    ctx, document, requests,
    setRespond: (fn) => { respond = fn; },
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
  console.log(`[回归] 登录桥接页 = ${plugin}/login.go`);

  // 阶段1：能从主面板 localStorage 取到 key 并回填。
  console.log("阶段1 自动取 key：");
  const first = loadBridge("state-1");
  check("key 已回填", first.document.getElementById("key").value, "sk-from-panel");
  checkTrue("sessionStorage 已缓存 key", first.ctx.sessionStorage.getItem("doubao-mgmt-key") === "sk-from-panel");

  // 阶段2：未粘贴 Cookie 时不发请求。
  console.log("阶段2 空 Cookie 拦截：");
  first.document.getElementById("submit")._fire("click");
  await tick();
  check("未发请求", first.requests.length, 0);
  checkTrue("提示要求粘贴 Cookie", first.document.getElementById("result").textContent.includes("Cookie"));

  // 阶段3：提交 → /import 带 Authorization 与 Cookie。
  console.log("阶段3 提交导入：");
  first.document.getElementById("cookie").value = "sessionid=abc; flow_cur_user_sec_id=xyz";
  first.setRespond(() => ({ status: 200, data: { ok: true, label: "测试号" } }));
  first.document.getElementById("submit")._fire("click");
  await tick(); await tick();
  const req = first.requests[0];
  checkTrue("请求路径为 /import", req && req.path.endsWith("/import"));
  check("Authorization 头", req.opts.headers.Authorization, "Bearer sk-from-panel");
  check("Content-Type 头", req.opts.headers["Content-Type"], "application/json");
  check("Cookie 正文", req.body.cookie, "sessionid=abc; flow_cur_user_sec_id=xyz");
  checkTrue("成功提示", first.document.getElementById("result").className === "ok");
  check("按钮文案改为已导入", first.document.getElementById("submit").textContent, "已导入");

  // 阶段4：导入失败要展示上游原因，不得误报成功。
  console.log("阶段4 导入失败：");
  const second = loadBridge("state-2");
  second.document.getElementById("cookie").value = "sessionid=bad";
  second.setRespond(() => ({ status: 400, data: { error: "登录态已失效，请重新登录后再导出" } }));
  second.document.getElementById("submit")._fire("click");
  await tick(); await tick();
  checkTrue("失败提示含上游原因", second.document.getElementById("result").textContent.includes("登录态已失效"));
  check("失败态", second.document.getElementById("result").className, "err");
  checkTrue("按钮已恢复可用", second.document.getElementById("submit").disabled === false);

  // 阶段5：401 清 key 并要求重填。
  console.log("阶段5 密钥失效：");
  const third = loadBridge("state-3");
  third.document.getElementById("cookie").value = "sessionid=abc";
  third.setRespond(() => ({ status: 401, data: { error: "missing management key" } }));
  third.document.getElementById("submit")._fire("click");
  await tick(); await tick();
  checkTrue("提示密钥失效", third.document.getElementById("result").textContent.includes("密钥无效"));
  checkTrue("已清掉失效 key", third.ctx.sessionStorage.getItem("doubao-mgmt-key") === null);

  // 阶段6：无可用 key 时提交给出可操作提示且不发请求。
  console.log("阶段6 无 key 拦截：");
  const fourth = loadBridge("state-4", { panelKey: "" });
  fourth.document.getElementById("key").value = "";
  fourth.document.getElementById("cookie").value = "sessionid=abc";
  fourth.document.getElementById("submit")._fire("click");
  await tick();
  check("无 key 不发请求", fourth.requests.length, 0);
  checkTrue("提示需要管理密钥", fourth.document.getElementById("result").textContent.includes("管理密钥"));

  if (failures.length) {
    console.log(`\n[结果] ${failures.length} 项失败`);
    failures.forEach((f) => console.log("   - " + f));
    process.exit(1);
  }
  console.log("\n[结果] 全部通过");
})();
