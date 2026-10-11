"""把 system.py 的原语组合成截图里的 9 项环境检查。"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Optional

from . import system

# 状态：ok / warn / fail
STATUS_OK = "ok"
STATUS_WARN = "warn"
STATUS_FAIL = "fail"


@dataclass
class CheckResult:
    key: str
    name: str
    status: str
    detail: str


CheckProgress = Callable[[CheckResult], None]


def _lang_target(region: str) -> str:
    return "zh-CN" if region.startswith("cn") else "en-US"


def run_all_checks(settings: dict, progress: Optional[CheckProgress] = None) -> list[CheckResult]:
    """按截图顺序执行全部检查；progress 回调用于 UI 逐项刷新。"""
    region = settings.get("target_region", "us_west")
    results: list[CheckResult] = []

    def emit(key: str, name: str, status: str, detail: str) -> CheckResult:
        res = CheckResult(key, name, status, detail)
        results.append(res)
        if progress:
            progress(res)
        return res

    # 1. 系统时区 ------------------------------------------------------
    tz = system.get_timezone()
    expected_tz = system.REGION_TIMEZONES.get(region, "")
    if expected_tz and tz == expected_tz:
        emit("timezone", "系统时区", STATUS_OK, tz)
    elif expected_tz:
        emit("timezone", "系统时区", STATUS_WARN, f"当前：{tz}；目标：{expected_tz}")
    else:
        emit("timezone", "系统时区", STATUS_OK, tz)

    # 2. 系统/浏览器语言 ----------------------------------------------
    sys_lang = system.get_system_language()
    target_lang = _lang_target(region)
    chrome_lang = system.get_browser_language("chrome")
    edge_lang = system.get_browser_language("edge")
    parts = [f"系统:{sys_lang}"]
    if chrome_lang:
        parts.append(f"Chrome:{chrome_lang}")
    if edge_lang:
        parts.append(f"Edge:{edge_lang}")
    lang_detail = "语言：" + "，".join(parts)
    if sys_lang.lower().startswith(target_lang.lower()):
        emit("language", "系统/浏览器语言", STATUS_OK, lang_detail)
    else:
        emit("language", "系统/浏览器语言", STATUS_WARN,
             lang_detail + f"（目标 {target_lang}）")

    # 3. DNS 泄露检测 ---------------------------------------------------
    dns = system.get_dns_servers()
    if dns:
        emit("dns", "DNS 泄露检测", STATUS_OK, "DNS: " + "，".join(dns))
    else:
        emit("dns", "DNS 泄露检测", STATUS_WARN, "未读取到系统 DNS")

    # 4. ANTHROPIC_BASE_URL --------------------------------------------
    base_url = system.get_anthropic_base_url()
    if base_url:
        emit("base_url", "ANTHROPIC_BASE_URL", STATUS_WARN,
             f"已设置：{base_url}（官方直连应为空）")
    else:
        emit("base_url", "ANTHROPIC_BASE_URL", STATUS_OK, "未设置（正常）")

    # 5. IPv6 ------------------------------------------------------------
    ipv6_desc, ipv6_enabled = system.get_ipv6_status()
    if not ipv6_enabled:
        emit("ipv6", "IPv6", STATUS_OK, ipv6_desc)
    else:
        want_disable = bool(settings.get("disable_ipv6", True))
        status = STATUS_WARN if want_disable else STATUS_OK
        emit("ipv6", "IPv6", status, ipv6_desc)

    # 6/7. WebRTC --------------------------------------------------------
    for key, browser, label in (
        ("webrtc_chrome", "chrome", "WebRTC (Chrome)"),
        ("webrtc_edge", "edge", "WebRTC (Edge)"),
    ):
        if not system.browser_installed(browser):
            emit(key, label, STATUS_WARN, "未检测到浏览器")
            continue
        policy = system.get_webrtc_policy(browser)
        if policy == "disable_non_proxied_udp":
            emit(key, label, STATUS_OK, "已配置（禁用非代理 UDP）")
        elif policy:
            emit(key, label, STATUS_WARN, f"当前策略：{policy}")
        else:
            emit(key, label, STATUS_WARN, "未配置（可能泄露真实 IP）")

    # 8. 系统代理 --------------------------------------------------------
    proxy = system.get_system_proxy()
    want_proxy = not region.startswith("cn")
    env_text = "；".join(system.get_env_proxies())
    if proxy.enabled and proxy.server:
        detail = f"系统代理已启用（{proxy.server}）"
        emit("proxy", "系统代理", STATUS_OK, detail + ("；" + env_text if env_text else ""))
    elif proxy.auto_detect or proxy.pac_url:
        emit("proxy", "系统代理", STATUS_OK, "自动检测/PAC 模式" + ("；" + env_text if env_text else ""))
    else:
        detail = "系统代理未启用"
        if env_text:
            # 环境变量 / settings.json 里已有代理，Claude Code 会走它
            emit("proxy", "系统代理", STATUS_OK, detail + "；" + env_text)
        elif want_proxy:
            emit("proxy", "系统代理", STATUS_WARN,
                 detail + "（目标地区需要代理；若使用 TUN 模式可忽略）")
        else:
            emit("proxy", "系统代理", STATUS_OK, detail + "（国内直连模式）")

    # 9. Claude 连通性 ---------------------------------------------------
    conn = system.check_claude_connectivity(
        timeout=int(settings.get("probe_timeout", 10)),
    )
    states = (conn.claude_state, conn.api_state)
    if system.PROBE_BLOCKED in states:
        emit("connectivity", "Claude 连通性", STATUS_FAIL, conn.detail)
    elif system.PROBE_FAIL in states or system.PROBE_CHALLENGE in states:
        emit("connectivity", "Claude 连通性", STATUS_WARN, conn.detail)
    else:
        emit("connectivity", "Claude 连通性", STATUS_OK, conn.detail)

    return results
