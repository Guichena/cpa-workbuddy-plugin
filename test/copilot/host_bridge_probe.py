#!/usr/bin/env python3
"""本地宿主桥驱动：用真实编译出的 copilot.so 走 ABI，验证宿主侧协议。

作用等同「把插件装进 CPA 宿主」的最小复刻：
构造 cliproxy_host_api 函数指针表 → cliproxy_plugin_init → cliproxyPluginCall。

用法：
    COPILOT_SO=/tmp/copilot_build/copilot.so python3 test/copilot/host_bridge_probe.py
真实凭据经 COPILOT_GH_TOKEN 环境变量传入，脚本不回显其原值。
"""
import ctypes, json, os, sys

SO = os.environ.get("COPILOT_SO", "/tmp/copilot_build/copilot.so")
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


# 记录宿主收到的 RPC 调用，供证据输出
host_calls = []
HOST_RESPONSES = {}
# 记录流式事件：host.stream.emit / host.stream.close 的 payload。
stream_events = []


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
    if m in ("host.stream.emit", "host.stream.close"):
        try:
            stream_events.append((m, json.loads(body)))
        except Exception:
            stream_events.append((m, body[:200]))
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


def main():
    api = HostAPI(abi_version=1, host_ctx=None, call=host_call, free_buffer=host_free)
    plug = PluginAPI()
    rc = lib.cliproxy_plugin_init(ctypes.byref(api), ctypes.byref(plug))
    print(f"[init] rc={rc} plugin.abi_version={plug.abi_version}")
    if rc != 0:
        print("FAIL: init 非 0"); return 1

    print(f"[init] 宿主 RPC 调用数={len(host_calls)}")
    for m, b in host_calls:
        print(f"   - {m} body={b[:120]}")

    # 真实凭据经环境变量传入（不回显原值），供 model.for_auth / executor.* 使用。
    gh_token = os.environ.get("COPILOT_GH_TOKEN", "").strip()

    probes = [
        ("plugin.register", None),
        ("model.static", None),
        ("executor.identifier", None),
        ("auth.identifier", None),
        ("scheduler.pick", {}),
    ]
    if gh_token:
        import base64
        sa_obj = {"type": "copilot-provider", "provider": "copilot-provider",
                  "github_token": gh_token, "auth_id": "copilot-verify", "label": "verify"}
        payload_obj = {"model": "gpt-4o", "max_tokens": 16, "stream": False,
                       "messages": [{"role": "user", "content": "reply with exactly: PONG"}]}
        # ExecutorRequest.Payload / StorageJSON 均为 []byte，JSON 编码必须走 base64 字符串。
        b64 = lambda o: base64.b64encode(json.dumps(o).encode()).decode()
        probes += [
            ("model.for_auth", {"AuthID": "copilot-verify", "StorageJSON": b64(sa_obj)}),
            ("executor.execute", {
                "Model": "gpt-4o",
                "AuthID": "copilot-verify",
                "StorageJSON": b64(sa_obj),
                "Payload": b64(payload_obj),
            }),
            ("executor.execute_stream", {
                "Model": "gpt-4o",
                "AuthID": "copilot-verify",
                "StorageJSON": b64(sa_obj),
                "Payload": b64({**payload_obj, "stream": True}),
                # 字段 tag 是 stream_id（非 StreamID）：Go 的 JSON 匹配不会跨下划线，
                # 传错键会让插件拿不到 stream id，streamEmit 直接早退且无任何流事件。
                "stream_id": "verify-stream-1",
            }),
        ]
    else:
        print("\n[跳过] 未提供 COPILOT_GH_TOKEN，跳过真实凭据相关探测")

    results = {}
    for method, payload in probes:
        try:
            rc, data = call_plugin(method, payload)
            ok = '"ok":true' in data or '"ok": true' in data
            results[method] = ok
            print(f"\n[{method}] rc={rc} ok={ok} len={len(data)}")
            print("   " + data[:500].replace("\n", " "))
        except Exception as e:
            results[method] = False
            print(f"\n[{method}] EXC {type(e).__name__}: {e}")

    # CYCLE-3：管理路由（面板页面 + 账号列表 + 额度查询）。
    # 管理响应体由宿主原样写回 HTTP 客户端，故判定看 StatusCode 与 Body，而非 ok 信封。
    print("\n=== CYCLE-3 管理路由 ===")
    mgmt = [
        ("GET", "/plugins/copilot-provider/panel", None, "面板页"),
        ("GET", "/v0/management/plugins/copilot-provider/accounts", None, "账号列表"),
        ("GET", "/v0/management/plugins/copilot-provider/status", None, "状态别名"),
        ("GET", "/v0/management/plugins/copilot-provider/credits", None, "额度查询(全部)"),
        ("POST", "/v0/management/plugins/copilot-provider/enable-all", b"{}", "批量启用"),
        ("POST", "/v0/management/plugins/copilot-provider/disable-all", b"{}", "批量禁用"),
    ]
    mgmt_results = {}
    import base64 as _b64m
    for method, path, body, label in mgmt:
        req = {"Method": method, "Path": path}
        if body is not None:
            # ManagementRequest.Body 是 []byte，JSON 编码必须走 base64 字符串。
            req["Body"] = _b64m.b64encode(body).decode()
        rc, data = call_plugin("management.handle", req)
        env = json.loads(data) if data else {}
        res = env.get("result", {}) if isinstance(env, dict) else {}
        # ManagementResponse 字段无 json tag，序列化后用 Go 字段名 StatusCode。
        status = res.get("StatusCode") if isinstance(res, dict) else None
        mgmt_results[label] = status == 200
        print(f"  {label:16s} {method:4s} status={status} {'PASS' if status == 200 else 'FAIL'}")

    # 注册表：面板资源与关键路由必须在位。
    rc, data = call_plugin("management.register", None)
    reg = json.loads(data).get("result", {}) if data else {}
    routes = [r.get("path", "") for r in reg.get("routes", [])]
    resources = [r.get("path", "") for r in reg.get("resources", [])]
    print(f"  路由注册数       = {len(routes)}")
    print(f"  资源注册         = {resources}")
    required = ["/plugins/copilot-provider/enable", "/plugins/copilot-provider/disable",
                "/plugins/copilot-provider/delete", "/plugins/copilot-provider/credits"]
    missing = [r for r in required if r not in routes]
    print(f"  关键路由缺失     = {missing if missing else '无'}")
    mgmt_results["路由注册"] = not missing and "/panel" in resources

    print("\n=== 汇总 ===")
    for k, v in results.items():
        print(f"  {k:26s} {'PASS' if v else 'FAIL'}")
    for k, v in mgmt_results.items():
        print(f"  {k:26s} {'PASS' if v else 'FAIL'}")

    # 流式转发在插件侧是异步 goroutine，需等待其完成才能观察到 emit/close 事件。
    if gh_token:
        import time as _t
        deadline = _t.time() + 45
        while _t.time() < deadline:
            if any(m == "host.stream.close" for m, _ in stream_events):
                break
            _t.sleep(0.5)

    # 流式契约证据：必须看到 [DONE] 帧且最终收到 stream.close。
    # host.stream.emit 的 payload 字段是 []byte，JSON 里以 base64 传输，须解码后再匹配。
    if stream_events:
        import base64 as _b64
        emits = [e for m, e in stream_events if m == "host.stream.emit"]
        closes = [m for m, _ in stream_events if m == "host.stream.close"]
        done_frames = 0
        for e in emits:
            if not isinstance(e, dict):
                continue
            raw = e.get("payload", "")
            try:
                text = _b64.b64decode(raw).decode("utf-8", "replace")
            except Exception:
                text = str(raw)
            if "[DONE]" in text:
                done_frames += 1
        print("\n=== 流式契约 ===")
        print(f"  emit 事件数   = {len(emits)}")
        print(f"  close 事件数  = {len(closes)}")
        print(f"  含 [DONE] 帧  = {done_frames}")
        print(f"  契约判定      = {'PASS' if done_frames >= 1 and closes else 'FAIL'}")
    elif gh_token:
        print("\n=== 流式契约 ===")
        print("  契约判定      = FAIL（未观察到任何流事件）")

    return 0 if (all(results.values()) and all(mgmt_results.values())) else 1


if __name__ == "__main__":
    sys.exit(main())
