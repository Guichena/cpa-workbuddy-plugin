#!/usr/bin/env python3
"""ABI 端到端探针：验证 copilot 模型发现与测试模型端点在真实 .so 中可用。

覆盖增量（相对 host_bridge_probe.py）：
  1. plugin.register 的管理路由表包含 /test-active；
  2. model.for_auth 在账号 models 字段为空时仍能返回非空清单（动态优先语义）；
  3. POST /test-active 路由可被宿主调用并返回结构化结果（无凭据时返回失败而非崩溃）。

用法：COPILOT_SO=/tmp/copilot.so python3 test/copilot/models_and_test_probe.py
"""
import base64
import ctypes
import json
import os
import sys

SO = os.environ.get("COPILOT_SO", "/tmp/copilot_012.so")
lib = ctypes.CDLL(SO)


class Buffer(ctypes.Structure):
    _fields_ = [("ptr", ctypes.c_void_p), ("len", ctypes.c_size_t)]


HOST_CALL = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_void_p, ctypes.c_char_p,
                             ctypes.c_void_p, ctypes.c_size_t,
                             ctypes.POINTER(Buffer))
# 插件侧 wb_free_host_buffer 以 (ptr, len) 两参数调用 free_buffer（无 ctx）。
HOST_FREE = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_size_t)
class HostAPI(ctypes.Structure):
    _fields_ = [("abi_version", ctypes.c_uint32), ("host_ctx", ctypes.c_void_p),
                ("call", HOST_CALL), ("free_buffer", HOST_FREE)]


PLUGIN_CALL = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_char_p, ctypes.POINTER(ctypes.c_uint8),
                               ctypes.c_size_t, ctypes.POINTER(Buffer))
PLUGIN_FREE = ctypes.CFUNCTYPE(None, ctypes.c_void_p, ctypes.c_size_t)
PLUGIN_SHUTDOWN = ctypes.CFUNCTYPE(None)


class PluginAPI(ctypes.Structure):
    _fields_ = [("abi_version", ctypes.c_uint32), ("call", PLUGIN_CALL),
                ("free_buffer", PLUGIN_FREE), ("shutdown", PLUGIN_SHUTDOWN)]


libc = ctypes.CDLL("libc.so.6")
libc.malloc.restype = ctypes.c_void_p
libc.malloc.argtypes = [ctypes.c_size_t]
libc.free.restype = None
libc.free.argtypes = [ctypes.c_void_p]

host_calls = []
# 账号文件的 models 字段留空，用于验证动态优先语义不会读旧兜底清单。
EMPTY_MODELS_ACCOUNT = {
    "type": "copilot-provider",
    "provider": "copilot-provider",
    "auth_id": "copilot-probetest0000",
    "github_token": "ghu_probe_placeholder_token",
    "copilot_token": "tid=probe;exp=9999999999;sku=free_limited_copilot",
    "api_key": "tid=probe;exp=9999999999;sku=free_limited_copilot",
    "account_type": "individual",
    "api_endpoint": "https://api.individual.githubcopilot.com",
    "disabled": False,
    "label": "Probe Account",
}
HOST_RESPONSES = {
    "host.auth.list": {
        "ok": True,
        "result": {"files": [{
            "auth_index": "copilot-probetest0000",
            "name": "copilot-probetest0000.json",
            "path": "/tmp/probe/copilot-probetest0000.json",
            "disabled": False,
        }]},
    },
    "host.auth.get": {
        "ok": True,
        "result": {
            "auth_index": "copilot-probetest0000",
            "name": "copilot-probetest0000.json",
            "path": "/tmp/probe/copilot-probetest0000.json",
            "json": EMPTY_MODELS_ACCOUNT,
        },
    },
}


@HOST_CALL
def host_call(ctx, method, req, req_len, out):
    m = method.decode()
    body = ctypes.string_at(req, req_len).decode() if req_len else ""
    host_calls.append(m)
    payload = HOST_RESPONSES.get(m, {"ok": True, "result": {}})
    raw = json.dumps(payload, ensure_ascii=False).encode()
    p = libc.malloc(len(raw))
    ctypes.memmove(p, raw, len(raw))
    out.contents.ptr = p
    out.contents.len = len(raw)
    return 0


@HOST_FREE
def host_free(ptr, ln):
    if ptr:
        libc.free(ctypes.c_void_p(ptr))


host_api = HostAPI()
host_api.abi_version = 1
host_api.host_ctx = None
host_api.call = host_call
host_api.free_buffer = host_free

plugin_api = PluginAPI()
lib.cliproxy_plugin_init(ctypes.byref(host_api), ctypes.byref(plugin_api))


def call_plugin(method, payload=None):
    raw = json.dumps(payload, ensure_ascii=False).encode() if payload is not None else b""
    buf = (ctypes.c_uint8 * len(raw))(*raw) if raw else None
    out = Buffer()
    rc = plugin_api.call(method.encode(), buf, len(raw), ctypes.byref(out))
    body = ctypes.string_at(out.ptr, out.len).decode() if out.len else ""
    if out.ptr:
        plugin_api.free_buffer(out.ptr, out.len)
    return rc, body


def envelope_result(body):
    env = json.loads(body)
    assert env.get("ok"), f"envelope not ok: {body[:300]}"
    return env.get("result")


def mgmt_body(result):
    """解出管理响应的 Body（[]byte 经 JSON 传输走 base64）。"""
    raw = result.get("Body") or ""
    if not raw:
        return ""
    return base64.b64decode(raw).decode("utf-8", errors="replace")


results = []
failures = []

# ---- 用例 1：管理路由注册表包含 /test-active ----
rc, body = call_plugin("management.register", {})
res = envelope_result(body)
routes = [r.get("path", "") for r in (res.get("routes") or [])]
has_test = any(p.endswith("/test-active") for p in routes)
results.append(("routes include /test-active", has_test, f"routes={len(routes)}"))
if not has_test:
    failures.append(f"management.register routes missing /test-active: {routes}")

# ---- 用例 2：model.for_auth 在账号 models 为空时返回非空（动态优先 / 兜底链） ----
rc, body = call_plugin("model.for_auth", {
    "AuthID": "copilot-probetest0000",
    "Host": {},
})
res = envelope_result(body)
models = [m.get("ID") for m in (res.get("Models") or [])]
ok2 = len(models) > 0
results.append(("model.for_auth returns non-empty", ok2, f"count={len(models)}"))
if not ok2:
    failures.append("model.for_auth returned empty list")

# ---- 用例 3：model.static 出口非空且无上游兜底污染 ----
rc, body = call_plugin("model.static", {"Host": {}})
res = envelope_result(body)
static_models = [m.get("ID") for m in (res.get("Models") or [])]
ok3 = len(static_models) > 0
results.append(("model.static returns non-empty", ok3, f"count={len(static_models)}"))
if not ok3:
    failures.append("model.static returned empty list")

# ---- 用例 4：管理端点 /test-active 可调用且缺 auth_index 时返回结构化错误 ----
# Body 是 []byte，经 JSON 传输走 base64（与宿主 ABI 一致）。
rc, body = call_plugin("management.handle", {
    "method": "POST",
    "path": "/plugins/copilot-provider/test-active",
    "body": base64.b64encode(b"{}").decode(),
})
res4 = envelope_result(body)
body4 = mgmt_body(res4)
ok4 = rc == 0 and "auth_index" in body4
results.append(("POST /test-active validates input", ok4, f"status={res4.get('StatusCode')}"))
if not ok4:
    failures.append(f"POST /test-active unexpected response: {body4[:200]}")

# ---- 用例 5：管理端点 /models 可调用 ----
rc, body = call_plugin("management.handle", {
    "method": "GET",
    "path": "/plugins/copilot-provider/models?auth_index=copilot-probetest0000",
})
res5 = envelope_result(body)
body5 = mgmt_body(res5)
ok5 = rc == 0 and "models" in body5
results.append(("GET /models reachable", ok5, f"status={res5.get('StatusCode')}"))
if not ok5:
    failures.append(f"GET /models unexpected response: {body5[:200]}")

print("=== ABI probe results ===")
for name, ok, detail in results:
    print(f"{'PASS' if ok else 'FAIL'}  {name}  ({detail})")
print(f"total={len(results)} passed={sum(1 for _, ok, _ in results if ok)} failed={len(failures)}")
if failures:
    for f in failures:
        print("  FAILURE:", f)
    sys.exit(1)
print("ALL PASS")
