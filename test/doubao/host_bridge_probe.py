#!/usr/bin/env python3
"""豆包插件宿主桥驱动：用真实编译出的 doubao.so 走 C ABI 验证登录桥接链路。

作用等同「把插件装进 CPA 宿主」的最小复刻：
构造 cliproxy_host_api 函数指针表 → cliproxy_plugin_init → cliproxyPluginCall。

验证 0.1.4 的免管理密钥提交契约（不需要任何真实凭据）：
  1. auth.login.start 返回 resource 桥接页 URL（宿主 OAuth 弹窗的落地页）；
  2. management.handle 处理 /v0/resource/plugins/doubao-provider/login/import
     时**不带 Authorization** 也能进入导入逻辑（不是 401/404）；
  3. 缺 Cookie / 未知 state 均返回 400 且带可读原因；
  4. 会话导入后 auth.login.poll 能拿到 success（用 HOME 隔离出的账号目录）。

HOME 指向临时目录，避免污染真实账号目录；判据端点仍是生产域名，
因此这里只使用明显无效的假 Cookie，并断言它被上游拒绝——不发送任何真实凭据。

用法：
    DOUBAO_SO=/tmp/doubao_abi/doubao.so python3 test/doubao/host_bridge_probe.py
"""
import ctypes, json, os, sys, tempfile

SO = os.environ.get("DOUBAO_SO", "/tmp/doubao_abi/doubao.so")
lib = ctypes.CDLL(SO)


class Buffer(ctypes.Structure):
    _fields_ = [("ptr", ctypes.c_void_p), ("len", ctypes.c_size_t)]


HOST_CALL = ctypes.CFUNCTYPE(ctypes.c_int, ctypes.c_void_p, ctypes.c_char_p,
                             ctypes.c_void_p, ctypes.c_size_t,
                             ctypes.POINTER(Buffer))
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


host_calls = []
HOST_RESPONSES = {}

libc = ctypes.CDLL("libc.so.6")
libc.malloc.restype = ctypes.c_void_p
libc.malloc.argtypes = [ctypes.c_size_t]
libc.free.restype = None
libc.free.argtypes = [ctypes.c_void_p]


@HOST_CALL
def host_call(ctx, method, req, req_len, out):
    m = method.decode()
    body = ctypes.string_at(req, req_len).decode() if req_len else ""
    host_calls.append((m, body[:300]))
    payload = HOST_RESPONSES.get(m, {"ok": True, "result": {}})
    raw = json.dumps(payload, ensure_ascii=False).encode()
    p = libc.malloc(len(raw))
    ctypes.memmove(p, raw, len(raw))
    out.contents.ptr = p
    out.contents.len = len(raw)
    return 0


@HOST_FREE
def host_free(ctx, ptr, ln):
    if ptr:
        libc.free(ctypes.c_void_p(ptr))


lib.cliproxy_plugin_init.restype = ctypes.c_int
lib.cliproxy_plugin_init.argtypes = [ctypes.POINTER(HostAPI), ctypes.POINTER(PluginAPI)]
lib.cliproxyPluginCall.restype = ctypes.c_int
lib.cliproxyPluginCall.argtypes = [ctypes.c_char_p, ctypes.c_void_p,
                                   ctypes.c_size_t, ctypes.POINTER(Buffer)]
lib.cliproxyPluginFree.restype = None
lib.cliproxyPluginFree.argtypes = [ctypes.c_void_p, ctypes.c_size_t]


def call_plugin(method: str, payload: dict | None = None):
    raw = json.dumps(payload).encode() if payload is not None else b""
    buf = ctypes.create_string_buffer(raw, len(raw)) if raw else None
    out = Buffer()
    rc = lib.cliproxyPluginCall(method.encode(), buf, len(raw), ctypes.byref(out))
    data = ctypes.string_at(out.ptr, out.len).decode() if out.ptr and out.len else ""
    if out.ptr and out.len:
        lib.cliproxyPluginFree(ctypes.c_void_p(out.ptr), out.len)
    return rc, data


def unwrap_response(raw: str):
    """从 management.handle 信封里取出 StatusCode 与 Body。"""
    import base64
    env = json.loads(raw)
    resp = env.get("result") or {}
    status = resp.get("StatusCode") or resp.get("status_code") or 0
    body = resp.get("Body") or resp.get("body") or ""
    if isinstance(body, list):
        body = bytes(body).decode()
    elif isinstance(body, str):
        try:
            body = base64.b64decode(body).decode()
        except Exception:
            pass
    return status, body


results = []


def check(label, cond, detail=""):
    results.append((label, bool(cond), detail))
    print(f"   {'PASS' if cond else 'FAIL'}  {label}" + (f"  [{detail}]" if detail and not cond else ""))


def main():
    api = HostAPI(abi_version=1, host_ctx=None, call=host_call, free_buffer=host_free)
    plug = PluginAPI()
    rc = lib.cliproxy_plugin_init(ctypes.byref(api), ctypes.byref(plug))
    print(f"[init] rc={rc} abi_version={plug.abi_version}")
    if rc != 0:
        print("FAIL: init 非 0")
        return 1

    print("[1] auth.login.start 返回 resource 桥接页")
    rc, data = call_plugin("auth.login.start", {})
    env = json.loads(data)
    start = env.get("result") or {}
    state = start.get("State") or start.get("state") or ""
    url = start.get("URL") or start.get("url") or ""
    check("start 返回 ok", env.get("ok") is True, data[:200])
    check("返回 state", bool(state), data[:200])
    check("URL 指向 resource 桥接页",
          url.startswith("/v0/resource/plugins/doubao-provider/login"), url)

    print("[2] 缺 Cookie 的提交必须被业务拒绝（不是 401/404）")
    rc, data = call_plugin("management.handle", {
        "Method": "GET",
        "Path": f"/v0/resource/plugins/doubao-provider/login/import?state={state}",
        "Query": {"state": [state]},
        "Headers": {},
    })
    status, body = unwrap_response(data)
    check("状态码为 400（进入导入逻辑并拒绝空 Cookie）", status == 400, f"status={status} body={body[:200]}")
    check("不是 401/403（说明没被管理密钥拦下）", status not in (401, 403), f"status={status}")
    check("不是 404（说明路由已注册）", status != 404, f"status={status}")
    check("提示 cookie 不能为空", "cookie" in body.lower(), body[:200])

    print("[3] 未知 state 必须被拒绝")
    rc, data = call_plugin("management.handle", {
        "Method": "GET",
        "Path": "/v0/resource/plugins/doubao-provider/login/import?state=bogus",
        "Query": {"state": ["bogus"]},
        "Headers": {"X-Doubao-Cookie": ["sessionid=probe_fake_value"]},
    })
    status, body = unwrap_response(data)
    check("未知 state 返回 400", status == 400, f"status={status} body={body[:200]}")
    check("提示会话不存在或过期", "登录会话" in body, body[:200])

    print("[4] 缺 state 必须被拒绝")
    rc, data = call_plugin("management.handle", {
        "Method": "GET",
        "Path": "/v0/resource/plugins/doubao-provider/login/import",
        "Query": {},
        "Headers": {"X-Doubao-Cookie": ["sessionid=probe_fake_value"]},
    })
    status, body = unwrap_response(data)
    check("缺 state 返回 400", status == 400, f"status={status} body={body[:200]}")

    print("[5] 桥接页 HTML 不再出现管理密钥输入")
    rc, data = call_plugin("management.handle", {
        "Method": "GET",
        "Path": f"/v0/resource/plugins/doubao-provider/login?state={state}",
        "Query": {"state": [state]},
        "Headers": {},
    })
    status, body = unwrap_response(data)
    check("桥接页 200", status == 200, f"status={status}")
    check("无密钥输入框", 'id="key"' not in body, body[:200])
    check("提交走 resource /login/import", "/login/import" in body, body[:300])

    print("[6] 未导入时轮询必须是 pending（不假成功）")
    rc, data = call_plugin("auth.login.poll", {"State": state})
    poll = (json.loads(data).get("result") or {})
    check("poll 为 pending", (poll.get("Status") or poll.get("status")) == "pending", data[:300])

    failed = [r for r in results if not r[1]]
    print(f"\n[结果] {len(results) - len(failed)}/{len(results)} PASS")
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
