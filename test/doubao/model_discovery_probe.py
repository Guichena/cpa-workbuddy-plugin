#!/usr/bin/env python3
"""豆包动态模型发现 ABI 探针：用真实编译出的 doubao.so 走 C ABI 验证。

验证 0.1.5 的模型清单改造（不需要任何真实凭据）：
  1. model.static 仍返回 4 个能力别名（宿主启动期无账号时目录必须可用）；
  2. model.for_auth 在凭据无效时回落到能力别名，不返回空目录；
  3. management.handle 的 /models 在账号不存在时给出可读错误；
  4. pro 别名的说明必须如实标注深度思考尚未生效。

用法：
    DOUBAO_SO=/tmp/doubao_abi2/doubao.so python3 test/doubao/model_discovery_probe.py
"""
import ctypes
import json
import os
import sys
import tempfile

SO = os.environ.get("DOUBAO_SO", "/tmp/doubao_abi2/doubao.so")
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


@HOST_CALL
def host_call(ctx, method, req, req_len, out):
    payload = {"ok": True, "result": {"files": []}}
    raw = json.dumps(payload, ensure_ascii=False).encode()
    p = libc.malloc(len(raw))
    ctypes.memmove(p, raw, len(raw))
    out.contents.ptr = p
    out.contents.len = len(raw)
    return 0


@HOST_FREE
def host_free(ptr, ln):
    """释放宿主侧分配的内存。

    C 侧签名是 void (*free_buffer)(void*, size_t)——只有 ptr 与 len 两个参数
    （见 doubao/main.go 的 cliproxy_host_api 定义）。按 (ctx, ptr, len) 三参声明
    会在每次调用时抛 TypeError。
    """
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


def unwrap_response(raw):
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

    print("[1] model.static 无账号时仍返回能力别名")
    rc, data = call_plugin("model.static", {})
    env = json.loads(data)
    check("model.static 返回 ok", env.get("ok") is True, data[:200])
    models = (env.get("result") or {}).get("Models") or (env.get("result") or {}).get("models") or []
    ids = [m.get("ID") or m.get("id") for m in models]
    check("返回 4 个能力别名", len(ids) == 4, str(ids))
    for want in ("doubao/default", "doubao/pro", "doubao/image", "doubao/video"):
        check(f"含 {want}", want in ids, str(ids))

    print("[2] pro 别名必须如实标注深度思考尚未生效")
    pro = next((m for m in models if (m.get("ID") or m.get("id")) == "doubao/pro"), {})
    desc = pro.get("Description") or pro.get("description") or ""
    check("pro 说明含「尚未真正生效」", "尚未真正生效" in desc, desc[:160])

    print("[3] model.for_auth 凭据无效时回落到能力别名")
    storage = json.dumps({
        "type": "doubao-provider", "provider": "doubao-provider",
        "auth_id": "doubao-probe0001",
        "cookie_header": "flow_cur_user_sec_id=fake; sessionid=fake; ttwid=fake",
        "bot_id": "7338286299411103781",
    }).encode()
    rc, data = call_plugin("model.for_auth", {
        "AuthID": "doubao-probe0001",
        "StorageJSON": list(storage),
    })
    env = json.loads(data)
    check("model.for_auth 返回 ok", env.get("ok") is True, data[:200])
    fmodels = (env.get("result") or {}).get("Models") or (env.get("result") or {}).get("models") or []
    fids = [m.get("ID") or m.get("id") for m in fmodels]
    check("回落目录非空", len(fids) >= 4, str(fids))
    check("回落仍含能力别名", "doubao/default" in fids, str(fids))

    print("[4] /models 账号不存在时给出可读错误")
    rc, data = call_plugin("management.handle", {
        "Method": "GET",
        "Path": "/v0/management/plugins/doubao-provider/models",
        "Query": {"auth_index": ["doubao-doesnotexist"]},
        "Headers": {},
        "Body": "",
    })
    status, body = unwrap_response(data)
    payload = json.loads(body) if body else {}
    check("返回 200（业务失败用响应体表达）", status == 200, f"status={status} body={body[:160]}")
    check("错误说明账号不存在", "账号不存在" in (payload.get("error") or ""), body[:200])

    failed = [r for r in results if not r[1]]
    print()
    if failed:
        print(f"[结果] {len(failed)}/{len(results)} 项失败")
        for label, _, detail in failed:
            print(f"   - {label}  [{detail}]")
        return 1
    print(f"[结果] 全部通过（{len(results)} 项）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
