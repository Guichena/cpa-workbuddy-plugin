#!/usr/bin/env python3
"""用真实生产账号验证动态模型发现（只读，不改任何账号文件）。

把生产账号 JSON 放到隔离的 HOME 账号目录，再通过 ABI 调 /models，
确认插件真的从上游拉到了模型清单而不是回落到能力别名。

用法：
    DOUBAO_SO=/tmp/doubao_abi2/doubao.so \\
    DOUBAO_ACCOUNT=/tmp/doubao_abi2/account.json \\
    python3 test/doubao/model_discovery_live.py
"""
import ctypes
import json
import os
import shutil
import sys
import tempfile

SO = os.environ.get("DOUBAO_SO", "/tmp/doubao_abi2/doubao.so")
ACCOUNT = os.environ.get("DOUBAO_ACCOUNT", "")
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


libc = ctypes.CDLL("libc.so.6")
libc.malloc.restype = ctypes.c_void_p
libc.malloc.argtypes = [ctypes.c_size_t]
libc.free.restype = None
libc.free.argtypes = [ctypes.c_void_p]

# 宿主 host.auth.list 返回该账号，插件据此定位本地文件。
HOST_AUTH_INDEX = "probe-index-0001"
HOST_AUTH_PATH = ""


@HOST_CALL
def host_call(ctx, method, req, req_len, out):
    name = method.decode()
    if name == "host.auth.list":
        payload = {"ok": True, "result": {"files": [{
            "auth_index": HOST_AUTH_INDEX,
            "name": os.path.basename(HOST_AUTH_PATH),
            "path": HOST_AUTH_PATH,
            "type": "doubao-provider",
            "provider": "doubao-provider",
        }]}}
    elif name == "host.auth.get":
        try:
            raw = json.loads(ctypes.string_at(req, req_len).decode())
        except Exception:
            raw = {}
        data = open(HOST_AUTH_PATH, "rb").read()
        payload = {"ok": True, "result": {
            "auth_index": raw.get("auth_index", HOST_AUTH_INDEX),
            "name": os.path.basename(HOST_AUTH_PATH),
            "path": HOST_AUTH_PATH,
            "json": json.loads(data.decode()),
        }}
    else:
        payload = {"ok": True, "result": {}}
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


lib.cliproxy_plugin_init.restype = ctypes.c_int
lib.cliproxy_plugin_init.argtypes = [ctypes.POINTER(HostAPI), ctypes.POINTER(PluginAPI)]
lib.cliproxyPluginCall.restype = ctypes.c_int
lib.cliproxyPluginCall.argtypes = [ctypes.c_char_p, ctypes.c_void_p,
                                   ctypes.c_size_t, ctypes.POINTER(Buffer)]
lib.cliproxyPluginFree.restype = None
lib.cliproxyPluginFree.argtypes = [ctypes.c_void_p, ctypes.c_size_t]


def call_plugin(method, payload=None):
    raw = json.dumps(payload).encode() if payload is not None else b""
    buf = ctypes.create_string_buffer(raw, len(raw)) if raw else None
    out = Buffer()
    rc = lib.cliproxyPluginCall(method.encode(), buf, len(raw), ctypes.byref(out))
    data = ctypes.string_at(out.ptr, out.len).decode() if out.ptr and out.len else ""
    if out.ptr and out.len:
        lib.cliproxyPluginFree(ctypes.c_void_p(out.ptr), out.len)
    return rc, data


def unwrap(raw):
    import base64
    env = json.loads(raw)
    resp = env.get("result") or {}
    body = resp.get("Body") or resp.get("body") or ""
    if isinstance(body, list):
        body = bytes(body).decode()
    elif isinstance(body, str):
        try:
            body = base64.b64decode(body).decode()
        except Exception:
            pass
    return resp.get("StatusCode") or resp.get("status_code") or 0, body


def main():
    global HOST_AUTH_PATH
    if not ACCOUNT or not os.path.exists(ACCOUNT):
        print("未提供 DOUBAO_ACCOUNT，跳过真实发现验证")
        return 0

    home = tempfile.mkdtemp(prefix="doubao-live-")
    os.environ["HOME"] = home
    accounts_dir = os.path.join(home, ".antigravity_cockpit", "doubao_accounts")
    os.makedirs(accounts_dir, exist_ok=True)

    data = json.load(open(ACCOUNT, encoding="utf-8"))
    auth_id = data.get("auth_id") or "doubao-liveprobe"
    HOST_AUTH_PATH = os.path.join(accounts_dir, auth_id + ".json")
    shutil.copyfile(ACCOUNT, HOST_AUTH_PATH)

    api = HostAPI(abi_version=1, host_ctx=None, call=host_call, free_buffer=host_free)
    plug = PluginAPI()
    if lib.cliproxy_plugin_init(ctypes.byref(api), ctypes.byref(plug)) != 0:
        print("FAIL: init 非 0")
        return 1

    print(f"[账号] 使用隔离目录中的生产账号副本，auth_id={auth_id}")
    rc, raw = call_plugin("management.handle", {
        "Method": "GET",
        "Path": "/v0/management/plugins/doubao-provider/models",
        "Query": {"auth_index": [auth_id]},
        "Headers": {},
        "Body": "",
    })
    status, body = unwrap(raw)
    payload = json.loads(body) if body else {}
    models = payload.get("models") or []
    source = payload.get("source")
    notice = payload.get("notice") or ""

    print(f"[结果] HTTP {status}  source={source}  模型数={len(models)}")
    if notice:
        print(f"[提示] {notice[:200]}")
    for m in models[:30]:
        print("   " + m)
    if len(models) > 30:
        print(f"   ... 另有 {len(models) - 30} 个")

    if source != "upstream":
        print("\nFAIL: 未从上游取得模型清单（回落到能力别名）")
        return 1
    if len(models) < 10:
        print(f"\nFAIL: 上游模型数过少（{len(models)}），疑似解析异常")
        return 1
    print(f"\n[结果] 动态发现通过：从上游取得 {len(models)} 个模型")
    shutil.rmtree(home, ignore_errors=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
