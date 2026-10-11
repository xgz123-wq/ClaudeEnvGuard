"""系统信息读取原语。

所有函数都做到「非 Windows / 读取失败」时优雅降级，返回 None 或空结构，
由上层决定如何展示。绝不在检查阶段抛出未捕获异常。
"""
from __future__ import annotations

import json
import locale
import os
import platform
import re
import socket
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Optional

IS_WINDOWS = platform.system() == "Windows"


def _run(cmd: list[str], timeout: int = 15) -> Optional[str]:
    """运行命令并返回 stdout；任何失败返回 None。"""
    try:
        out = subprocess.run(
            cmd, capture_output=True, text=True, timeout=timeout,
            encoding="utf-8", errors="replace",
        )
        return out.stdout
    except Exception:
        return None


def run_powershell(script: str, timeout: int = 60, args: Optional[list[str]] = None) -> tuple[bool, str]:
    """执行 PowerShell 片段，返回 (是否成功, 合并输出)。"""
    cmd = [
        "powershell.exe", "-NoProfile", "-NonInteractive",
        "-ExecutionPolicy", "Bypass", "-Command", script,
    ] + (args or [])
    try:
        out = subprocess.run(
            cmd, capture_output=True, text=True, timeout=timeout,
            encoding="utf-8", errors="replace",
        )
        text = (out.stdout or "") + (out.stderr or "")
        return out.returncode == 0, text.strip()
    except FileNotFoundError:
        return False, "未找到 powershell.exe（非 Windows 环境）"
    except Exception as exc:  # noqa: BLE001
        return False, f"PowerShell 执行异常：{exc}"


# ---------------------------------------------------------------- 时区 / 语言

REGION_TIMEZONES = {
    "us_west": "Pacific Standard Time",
    "us_east": "Eastern Standard Time",
    "us_central": "Central Standard Time",
    "uk": "GMT Standard Time",
    "jp": "Tokyo Standard Time",
    "sg": "Singapore Standard Time",
    "cn": "China Standard Time",
}


def get_timezone() -> str:
    """当前系统时区名（Windows 用 tzutil，其他用 time.tzname 组合）。"""
    if IS_WINDOWS:
        out = _run(["tzutil", "/g"])
        if out:
            return out.strip()
    import time
    return " / ".join(time.tzname)


def get_system_language() -> str:
    """系统界面语言，形如 en-US。"""
    if IS_WINDOWS:
        out = _run([
            "powershell.exe", "-NoProfile", "-Command",
            "(Get-WinUserLanguageList | Select-Object -First 1).LanguageTag",
        ])
        if out and out.strip():
            return out.strip().splitlines()[0].strip()
    try:
        loc = locale.getlocale()[0]
        return loc or "unknown"
    except Exception:
        return "unknown"


def _read_json_file(path: str) -> Optional[dict]:
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as fh:
            return json.load(fh)
    except Exception:
        return None


def get_browser_language(browser: str) -> Optional[str]:
    """从浏览器本地配置里读界面语言。browser: chrome / edge。"""
    if not IS_WINDOWS:
        return None
    roots = {
        "chrome": os.path.join(os.environ.get("LOCALAPPDATA", ""), "Google", "Chrome", "User Data"),
        "edge": os.path.join(os.environ.get("LOCALAPPDATA", ""), "Microsoft", "Edge", "User Data"),
    }
    root = roots.get(browser)
    if not root or not os.path.isdir(root):
        return None
    state = _read_json_file(os.path.join(root, "Local State"))
    if state:
        try:
            lang = state.get("intl", {}).get("app_locale")
            if lang:
                return lang.replace("_", "-")
        except AttributeError:
            pass
    prefs = _read_json_file(os.path.join(root, "Default", "Preferences"))
    if prefs:
        try:
            langs = prefs.get("intl", {}).get("accept_languages")
            if langs:
                return langs.split(",")[0].strip()
        except AttributeError:
            pass
    return None


# ---------------------------------------------------------------- DNS

def get_dns_servers() -> list[str]:
    """本机 IPv4 DNS 服务器列表。"""
    if IS_WINDOWS:
        script = (
            "Get-DnsClientServerAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue "
            "| Where-Object { $_.ServerAddresses } "
            "| ForEach-Object { $_.ServerAddresses } | Sort-Object -Unique"
        )
        ok, out = run_powershell(script)
        if ok and out:
            servers = [line.strip() for line in out.splitlines() if re.match(r"^\d{1,3}(\.\d{1,3}){3}$", line.strip())]
            if servers:
                return servers
    else:
        try:
            with open("/etc/resolv.conf", "r", encoding="utf-8") as fh:
                servers = re.findall(r"^nameserver\s+(\S+)", fh.read(), flags=re.M)
                return [s for s in servers if ":" not in s]
        except Exception:
            pass
    return []


# ---------------------------------------------------------------- 环境变量

def _claude_settings_env(filename: str) -> dict:
    """读取 ~/.claude/<filename> 的 env 块；缺失或 JSON 无效返回空字典。"""
    data = _read_json_file(os.path.join(os.path.expanduser("~"), ".claude", filename))
    env = data.get("env") if isinstance(data, dict) else None
    return env if isinstance(env, dict) else {}


def get_anthropic_base_url() -> Optional[str]:
    """ANTHROPIC_BASE_URL：进程环境 > 用户/机器环境 > Claude Code settings；未设置返回 None。"""
    value = os.environ.get("ANTHROPIC_BASE_URL")
    if value:
        return value
    if IS_WINDOWS:
        script = (
            "$u=[Environment]::GetEnvironmentVariable('ANTHROPIC_BASE_URL','User');"
            "$m=[Environment]::GetEnvironmentVariable('ANTHROPIC_BASE_URL','Machine');"
            "if($u){'user:'+$u}elseif($m){'machine:'+$m}else{''}"
        )
        ok, out = run_powershell(script)
        if ok and out.strip():
            return out.strip().splitlines()[0]
    for name in ("settings.json", "settings.local.json"):
        val = _claude_settings_env(name).get("ANTHROPIC_BASE_URL")
        if val and isinstance(val, str):
            return f"{name}:{val}"
    return None


# ---------------------------------------------------------------- IPv6

def get_ipv6_status() -> tuple[str, list[str]]:
    """返回 (状态描述, 启用 IPv6 的网卡名列表)。"""
    if IS_WINDOWS:
        script = (
            "Get-NetAdapterBinding -ComponentID ms_tcpip6 -ErrorAction SilentlyContinue "
            "| Where-Object { $_.Enabled } | ForEach-Object { $_.Name }"
        )
        ok, out = run_powershell(script)
        if ok:
            enabled = [ln.strip() for ln in out.splitlines() if ln.strip()]
            if enabled:
                return f"仍有 {len(enabled)} 张网卡启用 IPv6", enabled
            return "已全部禁用", []
        return "无法检测（需要 PowerShell）", []
    try:
        with open("/proc/sys/net/ipv6/conf/all/disable_ipv6", "r") as fh:
            disabled = fh.read().strip() == "1"
        return ("已全部禁用", []) if disabled else ("系统已启用 IPv6", ["system"])
    except Exception:
        return "无法检测", []


# ---------------------------------------------------------------- WebRTC 策略

_WEBRTC_POLICIES = {
    "chrome": r"SOFTWARE\Policies\Google\Chrome",
    "edge": r"SOFTWARE\Policies\Microsoft\Edge",
}


def get_webrtc_policy(browser: str) -> Optional[str]:
    """读取浏览器 WebRTC IP 处理策略；未安装/未配置返回 None。"""
    if not IS_WINDOWS:
        return None
    key = _WEBRTC_POLICIES.get(browser)
    if not key:
        return None
    script = (
        f"$v=$null;"
        f"foreach($root in 'HKLM:','HKCU:'){{"
        f"try{{$p=Get-ItemProperty -Path (Join-Path $root '{key}') -Name 'WebRtcIPHandling' -ErrorAction Stop;"
        f"if($p.WebRtcIPHandling){{$v=$p.WebRtcIPHandling;break}}}}catch{{}}}}"
        f"if($v){{$v}}else{{''}}"
    )
    ok, out = run_powershell(script)
    if ok and out.strip():
        return out.strip().splitlines()[0]
    return None


def browser_installed(browser: str) -> bool:
    if not IS_WINDOWS:
        return False
    paths = {
        "chrome": os.path.join(os.environ.get("ProgramFiles", r"C:\Program Files"), "Google", "Chrome", "Application", "chrome.exe"),
        "edge": os.path.join(os.environ.get("ProgramFiles(x86)", r"C:\Program Files (x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
    }
    p = paths.get(browser)
    return bool(p and os.path.exists(p))


# ---------------------------------------------------------------- 系统代理

@dataclass
class ProxyInfo:
    enabled: bool = False
    server: str = ""          # 形如 127.0.0.1:10808
    auto_detect: bool = False
    pac_url: str = ""


def get_system_proxy() -> ProxyInfo:
    info = ProxyInfo()
    if not IS_WINDOWS:
        # 兼容类 Unix 上的环境变量代理
        for var in ("HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"):
            val = os.environ.get(var)
            if val:
                m = re.search(r"https?://([^/]+)", val)
                if m:
                    info.enabled = True
                    info.server = m.group(1)
                    return info
        return info
    script = (
        "$r=Get-ItemProperty -Path 'HKCU:Software\Microsoft\Windows\CurrentVersion\Internet Settings';"
        "if($r.ProxyEnable -eq 1){'enabled'}else{'disabled'};"
        "if($r.ProxyServer){$r.ProxyServer}else{''};"
        "if($r.AutoDetect){'auto'}else{''};"
        "if($r.AutoConfigURL){$r.AutoConfigURL}else{''}"
    )
    ok, out = run_powershell(script)
    if not ok or not out:
        return info
    lines = out.splitlines() + [""] * (4 - len(out.splitlines()))
    info.enabled = lines[0].strip().lower() == "enabled"
    info.server = lines[1].strip()
    info.auto_detect = lines[2].strip() == "auto"
    info.pac_url = lines[3].strip()
    return info


_PROXY_VARS = ("HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY")


def get_env_proxies() -> list[str]:
    """检测 HTTPS_PROXY/HTTP_PROXY/ALL_PROXY（不分大小写），返回 'NAME=值 (来源)' 列表。"""
    found: list[str] = []
    seen: set[tuple[str, str]] = set()

    def add(name: str, val: str, src: str) -> None:
        key = (name.upper(), val)
        if val and key not in seen:
            seen.add(key)
            found.append(f"{name.upper()}={val} ({src})")

    for k, v in os.environ.items():
        if k.upper() in _PROXY_VARS:
            add(k, v, "进程环境")
    if IS_WINDOWS:
        script = (
            "foreach($s in 'User','Machine'){foreach($n in 'HTTPS_PROXY','HTTP_PROXY','ALL_PROXY'){"
            "$v=[Environment]::GetEnvironmentVariable($n,$s);if($v){$s+'|'+$n+'|'+$v}}}"
        )
        ok, out = run_powershell(script)
        if ok:
            for line in out.splitlines():
                parts = line.strip().split("|", 2)
                if len(parts) == 3:
                    add(parts[1], parts[2], "用户环境" if parts[0] == "User" else "机器环境")
    for k, v in _claude_settings_env("settings.json").items():
        if k.upper() in _PROXY_VARS and isinstance(v, str):
            add(k, v, "settings.json")
    return found


# ---------------------------------------------------------------- 连通性

# 探测结果状态
PROBE_OK = "ok"              # 可达且正常
PROBE_CHALLENGE = "challenge"  # 可达，但被 Cloudflare 验证页拦住（浏览器通常可通过）
PROBE_BLOCKED = "blocked"    # 地区受限 / 被拒绝
PROBE_FAIL = "fail"          # 连接失败（超时 / TLS / DNS 等）

_PROBE_LABELS = {
    PROBE_OK: "可达",
    PROBE_CHALLENGE: "可达（Cloudflare 验证）",
    PROBE_BLOCKED: "地区受限(403)",
    PROBE_FAIL: "连接失败",
}


@dataclass
class Connectivity:
    claude_ai: bool = False          # 兼容旧字段：链路可达且未被地区封锁
    api_anthropic: bool = False
    claude_state: str = PROBE_FAIL
    api_state: str = PROBE_FAIL
    detail: str = ""


def classify_probe(host: str, status: int, headers: dict, body: str, final_url: str = "") -> str:
    """根据响应判断状态（纯函数，便于离线测试）。headers 的键需为小写。"""
    low = (body or "").lower()
    url = (final_url or "").lower()
    # Cloudflare 验证页：不是地区封锁，浏览器里通常能过
    is_challenge = (headers.get("cf-mitigated", "").lower() == "challenge"
                    or "just a moment" in low)
    if host == "claude.ai":
        if is_challenge:
            return PROBE_CHALLENGE
        # 地区不可用页：最终 URL 或正文含 unavailable
        if "unavailable" in url or "unavailable" in low or "app-unavailable-in-region" in low:
            return PROBE_BLOCKED
        if status == 403:
            return PROBE_BLOCKED
        return PROBE_OK
    # api.anthropic.com：无 key 正常返回 401；403 才视为封锁
    if status == 403:
        return PROBE_CHALLENGE if is_challenge else PROBE_BLOCKED
    return PROBE_OK


def _https_probe(url: str, timeout: int = 10) -> str:
    """探测 URL，返回 PROBE_* 状态；最多读取 64KB 正文。"""
    host = urllib.parse.urlparse(url).hostname or ""
    try:
        req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read(65536).decode("utf-8", "replace")
            headers = {k.lower(): v for k, v in resp.headers.items()}
            return classify_probe(host, resp.status, headers, body, resp.geturl())
    except urllib.error.HTTPError as exc:
        # 4xx/5xx 也有响应，按状态码与正文分类
        try:
            body = exc.read(65536).decode("utf-8", "replace")
        except Exception:
            body = ""
        headers = {k.lower(): v for k, v in (exc.headers.items() if exc.headers else [])}
        return classify_probe(host, exc.code, headers, body, exc.geturl() or "")
    except Exception:
        return PROBE_FAIL


def check_claude_connectivity(timeout: int = 10) -> Connectivity:
    conn = Connectivity()
    conn.claude_state = _https_probe("https://claude.ai/", timeout)
    conn.api_state = _https_probe("https://api.anthropic.com/v1/models", timeout)
    conn.claude_ai = conn.claude_state in (PROBE_OK, PROBE_CHALLENGE)
    conn.api_anthropic = conn.api_state in (PROBE_OK, PROBE_CHALLENGE)
    conn.detail = (f"claude.ai: {_PROBE_LABELS[conn.claude_state]}；"
                   f"api.anthropic.com: {_PROBE_LABELS[conn.api_state]}")
    return conn


# ---------------------------------------------------------------- 出口 IP

@dataclass
class ExitInfo:
    ip: str = ""
    country: str = ""        # 中文名
    country_code: str = ""
    region: str = ""
    city: str = ""
    org: str = ""            # ASN + 运营商，形如 AS15169 Google LLC
    ok: bool = False
    error: str = ""


_COUNTRY_ZH = {
    "US": "美国", "CN": "中国", "HK": "中国香港", "TW": "中国台湾", "JP": "日本",
    "SG": "新加坡", "KR": "韩国", "GB": "英国", "DE": "德国", "FR": "法国",
    "AU": "澳大利亚", "CA": "加拿大", "NL": "荷兰", "IN": "印度", "RU": "俄罗斯",
}


def fetch_exit_info(timeout: int = 12) -> ExitInfo:
    """通过公共 IP 信息服务和当前（代理）出口获取公网信息。"""
    info = ExitInfo()
    try:
        req = urllib.request.Request(
            "https://ipinfo.io/json",
            headers={"User-Agent": "Mozilla/5.0"},
        )
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode("utf-8", "replace"))
        info.ok = True
        info.ip = data.get("ip", "")
        info.country_code = data.get("country", "")
        info.country = _COUNTRY_ZH.get(info.country_code, info.country_code)
        info.region = data.get("region", "")
        info.city = data.get("city", "")
        info.org = data.get("org", "")
        return info
    except Exception as exc:  # noqa: BLE001
        info.error = str(exc)
    # 兜底：ip-api.com
    try:
        req = urllib.request.Request(
            "http://ip-api.com/json/?fields=status,country,countryCode,regionName,city,isp,org,as,query",
            headers={"User-Agent": "Mozilla/5.0"},
        )
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode("utf-8", "replace"))
        if data.get("status") == "success":
            info.ok = True
            info.ip = data.get("query", "")
            info.country_code = data.get("countryCode", "")
            info.country = _COUNTRY_ZH.get(info.country_code, data.get("country", ""))
            info.region = data.get("regionName", "")
            info.city = data.get("city", "")
            info.org = data.get("as") or data.get("isp", "")
            info.error = ""
            return info
    except Exception as exc:  # noqa: BLE001
        info.error = str(exc)
    return info


def split_org(org: str) -> tuple[str, str]:
    """把 'AS701 Verizon Business' 拆成 (ASN, 运营商)。"""
    m = re.match(r"^(AS\d+)\s+(.*)$", org.strip())
    if m:
        return m.group(1), m.group(2)
    return "", org
