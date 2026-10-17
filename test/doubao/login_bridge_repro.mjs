#!/usr/bin/env node
/**
 * 豆包登录桥接页回归脚本（Node vm + DOM 桩，真实执行 login.go 渲染出的内联 JS）。
 *
 * 覆盖桥接页契约：
 *   1. 页面不需要管理密钥，也不渲染密钥输入框；
 *   2. 未粘贴 Cookie 时点提交给出提示且不发请求；
 *   3. 提交走免密钥 resource 路由 /login/import，Cookie 放 X-Doubao-Cookie 头；
 *   4. 导入成功展示成功提示，失败展示上游原因；
 *   5. 请求进行中禁用按钮，结束后恢复。
 *
 * 用法：node test/doubao/login_bridge_repro.mjs
 * 退出码：0 = 全部断言通过；1 = 存在断言失败。
 */
import fs from "fs";
import path from "path";
import vm from "vm";
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
  // 每行 b.WriteString(<expr>) 里可能含 Go 侧字符串拼接（如 data-state 属性），
  // 因此先按整行取表达式，再抽出其中的字面量并把变量占位符替换成实际值。
  const re = /b\.WriteString\((.+)\)\s*$/gm;
  let m;
  while ((m = re.exec(src)) !== null) {
    const expr = m[1];
    if (!expr.includes('"')) continue;
    let text = "";
    const lit = /("(?:[^"\\]|\\.)*")/g;
    let l;
    while ((l = lit.exec(expr)) !== null) {
      text += JSON.parse(l[1]);
    }
    text = text.replace(/htmlEscape\(providerName\)/g, "").replace(/htmlEscape\(state\)/g, "");
    parts.push(text);
  }
  let html = parts.join("");
  html = html.replace('data-provider=""', `data-provider="${plugin}-provider"`);
  html = html.replace('data-state=""', `data-state="${state}"`);
  return html;
}

function loadBridge(state) {
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

  const requests = [];
  let respond = () => ({ status: 200, data: { ok: true, label: "测试号" } });
  const ctx = {
    console, Date, Math, JSON, Number, String, Object, Array, Boolean, RegExp, Error, Promise,
    isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
    TextEncoder, TextDecoder, setTimeout, clearTimeout,
    btoa: (s) => Buffer.from(s, "binary").toString("base64"),
    atob: (s) => Buffer.from(s, "base64").toString("binary"),
    document,
    fetch: async (url, opts) => {
      requests.push({ path: String(url), opts: opts || {} });
      const r = await respond();
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
    ctx, document, html, requests,
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

  // 阶段1：页面不再出现管理密钥输入框与密钥相关文案。
  console.log("阶段1 无管理密钥：");
  const first = loadBridge("state-1");
  checkTrue("页面无密钥输入框", !first.html.includes('id="key"'));
  checkTrue("页面无管理密钥文案", !first.html.includes("管理密钥"));

  // 阶段2：未粘贴 Cookie 时不发请求。
  console.log("阶段2 空 Cookie 拦截：");
  first.document.getElementById("submit")._fire("click");
  await tick();
  check("未发请求", first.requests.length, 0);
  checkTrue("提示要求粘贴 Cookie", first.document.getElementById("result").textContent.includes("Cookie"));

  // 阶段3：提交走 resource 路由，Cookie 放请求头，不带 Authorization。
  console.log("阶段3 免密钥提交：");
  first.document.getElementById("cookie").value = "sessionid=abc; flow_cur_user_sec_id=xyz";
  first.setRespond(() => ({ status: 200, data: { ok: true, label: "测试号" } }));
  first.document.getElementById("submit")._fire("click");
  await tick(); await tick();
  const req = first.requests[0];
  checkTrue("路径为 resource /login/import", req && req.path.startsWith("/v0/resource/plugins/doubao-provider/login/import"));
  checkTrue("URL 携带 state", req && req.path.includes("state=state-1"));
  check("方法为 GET", req.opts.method, "GET");
  check("Cookie 走自定义头", req.opts.headers["X-Doubao-Cookie"], "sessionid=abc; flow_cur_user_sec_id=xyz");
  checkTrue("不带 Authorization", !(req.opts.headers.Authorization));
  checkTrue("不带请求体", !req.opts.body);
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

  // 阶段5：会话过期提示可操作。
  console.log("阶段5 会话过期：");
  const third = loadBridge("state-3");
  third.document.getElementById("cookie").value = "sessionid=abc";
  third.setRespond(() => ({ status: 400, data: { error: "登录会话不存在或已过期，请回到管理页重新发起" } }));
  third.document.getElementById("submit")._fire("click");
  await tick(); await tick();
  checkTrue("提示回到管理页重新发起", third.document.getElementById("result").textContent.includes("重新发起"));

  if (failures.length) {
    console.log(`\n[结果] ${failures.length} 项失败`);
    failures.forEach((f) => console.log("   - " + f));
    process.exit(1);
  }
  console.log("\n[结果] 全部通过");
})();
