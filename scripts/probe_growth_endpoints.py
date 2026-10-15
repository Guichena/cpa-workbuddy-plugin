#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""WorkBuddy 国内版成长中心端点只读探针。

用途：拿本机真实 CN 账号凭据逐个探针成长中心端点，输出各端点的 HTTP 状态码与
响应顶层字段名，用于在写业务代码前证实接口契约（路径 / 鉴权头 / 字段名）。

安全约定（强制）：
  1. 默认只执行 GET 只读端点；写端点（POST）必须显式传 --write 且会真实领取奖励。
  2. 任何情况下都不打印 token / UID / enterpriseId / 域名原值，只打印字段名与状态。
  3. 只读取本机 auth 文件；不连接任何 test / prod 环境。

用法：
  python scripts/probe_growth_endpoints.py --readonly
  python scripts/probe_growth_endpoints.py --readonly --auth-file <path>
  python scripts/probe_growth_endpoints.py --write          # 需用户当轮授权

退出码：0=全部端点返回预期状态；1=存在端点异常或凭据缺失。
"""

from __future__ import annotations

import argparse
import json
import os
import ssl
import sys
import urllib.error
import urllib.request

# 成长中心路由前缀；每日签到状态在 billing/meter 下（与插件 billing.go 同源）。
GROWTH_PREFIX = "/v2/activity/growth"
CHECKIN_STATUS_PATH = "/v2/billing/meter/checkin-activity-status"

# 只读端点：(标签, 方法, 相对路径)
READONLY_ENDPOINTS = [
    ("签到状态", "POST", CHECKIN_STATUS_PATH),
    ("连登状态", "GET", GROWTH_PREFIX + "/streak"),
    ("签到热力图", "GET", GROWTH_PREFIX + "/heatmap"),
    ("能量余额", "GET", GROWTH_PREFIX + "/energy"),
    ("成长任务列表", "GET", GROWTH_PREFIX + "/tasks"),
    ("兑换状态", "GET", GROWTH_PREFIX + "/redeem/summary"),
    ("抽奖次数", "GET", GROWTH_PREFIX + "/lottery/chances"),
    ("Buddy 旅行状态", "GET", GROWTH_PREFIX + "/buddy/travel/status"),
    ("Buddy 旅行配置", "GET", GROWTH_PREFIX + "/buddy/travel/config"),
    ("Buddy 盲盒额度", "GET", GROWTH_PREFIX + "/buddy/quota"),
]

# 写端点：仅在 --write 下执行，会真实改变账号状态。
WRITE_ENDPOINTS = [
    ("Buddy 收礼", "POST", GROWTH_PREFIX + "/buddy/travel/claim", {"record_id": "__from_status__"}),
    ("Buddy 派出", "POST", GROWTH_PREFIX + "/buddy/travel/depart", {"location_id": "__from_config__"}),
    ("补登卡使用", "POST", GROWTH_PREFIX + "/makeup-cards/use", {"target_date": "__from_streak__"}),
    ("连登兑换", "POST", GROWTH_PREFIX + "/redeem", {"tier": "7d", "client_token": "u-00000000-0000-4000-8000-000000000000"}),
    ("抽奖", "POST", GROWTH_PREFIX + "/lottery/draw", {"client_token": "u-00000000-0000-4000-8000-000000000000"}),
    ("Buddy 开盒", "POST", GROWTH_PREFIX + "/buddy/open", {"count": 1, "client_token": "u-00000000-0000-4000-8000-000000000000"}),
]

DEFAULT_ENDPOINT = "https://copilot.tencent.com"
DEFAULT_AUTH_DIR = os.path.join(os.path.expanduser("~"), ".antigravity_cockpit", "workbuddy_accounts")


def load_auth(path: str) -> dict:
    """读取一个 auth JSON 文件，返回规范化后的凭据字段（值仅供内部使用，不打印）。"""
    with open(path, "r", encoding="utf-8") as fh:
        doc = json.load(fh)
    if isinstance(doc.get("auth"), dict):
        auth, account = doc["auth"], doc.get("account", {})
    else:
        auth, account = doc, doc
    return {
        "access_token": auth.get("accessToken", ""),
        "domain": auth.get("domain", ""),
        "uid": account.get("uid", ""),
        "enterprise_id": account.get("enterpriseId", ""),
    }


def build_headers(cred: dict) -> dict:
    """构造与插件 billingHeaders 同族的鉴权头。"""
    headers = {
        "Accept": "application/json",
        "Content-Type": "application/json",
        "User-Agent": "WorkBuddy",
    }
    if cred.get("access_token"):
        headers["Authorization"] = "Bearer " + cred["access_token"]
    if cred.get("uid"):
        headers["X-User-Id"] = cred["uid"]
    if cred.get("enterprise_id"):
        headers["X-Enterprise-Id"] = cred["enterprise_id"]
        headers["X-Tenant-Id"] = cred["enterprise_id"]
    if cred.get("domain"):
        headers["X-Domain"] = cred["domain"]
    return headers


def endpoint_base(cred: dict) -> str:
    """按账号 domain 判定区域 base；CN 用默认 endpoint。"""
    domain = (cred.get("domain") or "").strip()
    if domain.endswith("workbuddy.ai"):
        return "https://www.workbuddy.ai"
    return DEFAULT_ENDPOINT


def probe(base: str, headers: dict, label: str, method: str, path: str, body: dict | None) -> tuple[int, list[str], str]:
    """执行一次探针请求，返回 (状态码, 顶层字段名列表, 备注)。不打印任何凭据。"""
    url = base + path
    data = json.dumps(body).encode("utf-8") if body is not None else b"{}"
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    ctx = ssl.create_default_context()
    try:
        with urllib.request.urlopen(req, timeout=20, context=ctx) as resp:
            status = resp.status
            raw = resp.read()
    except urllib.error.HTTPError as exc:
        status = exc.code
        raw = exc.read()
    except Exception as exc:  # noqa: BLE001 - 探针需把网络错误转为可读状态
        return 0, [], f"transport error: {type(exc).__name__}"

    try:
        doc = json.loads(raw.decode("utf-8", "replace"))
    except json.JSONDecodeError:
        return status, [], "non-JSON response"

    # 只取顶层字段名：envelope 用 data 内的字段，非 envelope 直接用顶层。
    keys: list[str] = []
    if isinstance(doc, dict):
        payload = doc.get("data") if isinstance(doc.get("data"), (dict, list)) else doc
        if isinstance(payload, dict):
            keys = sorted(payload.keys())
        elif isinstance(payload, list):
            keys = ["<list>"]
    code = doc.get("code") if isinstance(doc, dict) else None
    note = f"code={code}" if code is not None else ""
    return status, keys, note


def main() -> int:
    parser = argparse.ArgumentParser(description="WorkBuddy 成长中心端点只读探针")
    parser.add_argument("--readonly", action="store_true", help="只探只读端点（默认行为）")
    parser.add_argument("--write", action="store_true", help="执行写端点（会真实领取奖励，需当轮授权）")
    parser.add_argument("--auth-file", default="", help="指定单个 auth JSON 文件；缺省扫描 auth 目录")
    args = parser.parse_args()

    auth_paths: list[str] = []
    if args.auth_file:
        auth_paths = [args.auth_file]
    elif os.path.isdir(DEFAULT_AUTH_DIR):
        auth_paths = [
            os.path.join(DEFAULT_AUTH_DIR, n)
            for n in sorted(os.listdir(DEFAULT_AUTH_DIR))
            if n.lower().endswith(".json")
        ]

    if not auth_paths:
        print(f"[probe] 未找到可用凭据：{DEFAULT_AUTH_DIR} 为空。")
        print("[probe] 请通过 --auth-file 指定本机 CN 账号 auth 文件，或把 auth 文件放入该目录后重试。")
        print("[probe] 环境阻断：按本地连接调试红线，不得改用 test/prod 凭据。")
        return 1

    # 只取第一个账号做契约探针，避免多账号重复打点。
    cred = load_auth(auth_paths[0])
    if not cred.get("access_token"):
        print("[probe] 凭据缺少 accessToken，终止。")
        return 1
    base = endpoint_base(cred)
    headers = build_headers(cred)
    print(f"[probe] 账号来源：{os.path.basename(auth_paths[0])}；区域 base：{base}")
    print(f"[probe] 凭据字段已就绪（token/uid/enterpriseId 一律不打印）")
    print()

    failures = 0
    print("== 只读端点 ==")
    for label, method, path in READONLY_ENDPOINTS:
        status, keys, note = probe(base, headers, label, method, path, None)
        ok = 200 <= status < 300
        if not ok:
            failures += 1
        print(f"  [{status}] {label:<14} {method:<5} {path}")
        print(f"        顶层字段: {', '.join(keys) if keys else '(无)'} {note}")
        sys.stdout.flush()

    if args.write:
        print()
        print("== 写端点（真实领取） ==")
        for label, method, path, body in WRITE_ENDPOINTS:
            status, keys, note = probe(base, headers, label, method, path, body)
            ok = 200 <= status < 300
            if not ok:
                failures += 1
            print(f"  [{status}] {label:<14} {method:<5} {path}")
            print(f"        顶层字段: {', '.join(keys) if keys else '(无)'} {note}")
            sys.stdout.flush()
    elif not args.readonly:
        print()
        print("[probe] 提示：写端点未执行（需显式 --write 且用户当轮授权）。")

    print()
    print(f"[probe] 完成：{'全部通过' if failures == 0 else f'{failures} 个端点异常'}")
    return 0 if failures == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
