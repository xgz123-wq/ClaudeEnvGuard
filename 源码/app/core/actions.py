"""配置动作：一键配置环境 / 配置中国 / 配置美国。

Windows 上调用内嵌 PowerShell 脚本（需要管理员时会请求提权），
非 Windows 环境给出明确提示。所有输出实时回显到 GUI 日志。
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Callable, Optional

LogFn = Callable[[str], None]

SCRIPT_DIR = Path(__file__).resolve().parent.parent / "scripts"
SETTINGS_PATH = Path(__file__).resolve().parent.parent / "resources" / "settings.json"

DEFAULT_SETTINGS = {
    "target_region": "us_west",
    "disable_ipv6": True,
    "proxy_port": 10808,       # 系统代理端口，v2rayN 默认 10808；Clash 多为 7890
    "probe_timeout": 10,
}


def load_settings() -> dict:
    try:
        data = json.loads(SETTINGS_PATH.read_text(encoding="utf-8"))
        merged = dict(DEFAULT_SETTINGS)
        merged.update({k: v for k, v in data.items() if k in DEFAULT_SETTINGS})
        return merged
    except Exception:
        return dict(DEFAULT_SETTINGS)


def save_settings(settings: dict) -> None:
    try:
        SETTINGS_PATH.parent.mkdir(parents=True, exist_ok=True)
        SETTINGS_PATH.write_text(
            json.dumps(settings, ensure_ascii=False, indent=2), encoding="utf-8")
    except Exception:
        pass


def _is_admin() -> bool:
    if os.name != "nt":
        return False
    try:
        import ctypes
        return bool(ctypes.windll.shell32.IsUserAnAdmin())
    except Exception:
        return False


def _stream_process(cmd: list[str], log: LogFn) -> int:
    """运行进程并把 stdout/stderr 逐行送进日志。"""
    try:
        proc = subprocess.Popen(
            cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
            text=True, encoding="utf-8", errors="replace",
        )
    except FileNotFoundError as exc:
        log(f"启动失败：{exc}")
        return 127
    assert proc.stdout is not None
    for line in proc.stdout:
        line = line.rstrip()
        if line:
            log(line)
    return proc.wait()


def _elevated_powershell(script_path: Path, args: list[str], log: LogFn) -> int:
    """Windows 上以管理员身份运行 PowerShell 脚本（会弹 UAC）。"""
    import ctypes
    params = " ".join([f"-NoProfile -ExecutionPolicy Bypass -File \"{script_path}\""] + args)
    rc = ctypes.windll.shell32.ShellExecuteW(None, "runas", "powershell.exe", params, None, 1)
    if rc <= 32:
        log("用户取消了提权，或提权失败。")
        return int(rc) if rc > 0 else 1
    log("已请求管理员权限执行，请在 UAC 弹窗中允许。")
    return 0


def run_configure(mode: str, settings: dict, log: LogFn) -> bool:
    """mode: env（按当前目标地区）/ cn / us。"""
    if os.name != "nt":
        log("一键配置目前仅支持 Windows（需要修改系统时区/语言/注册表）。")
        return False

    if mode == "env":
        mode = "cn" if settings.get("target_region", "").startswith("cn") else "us"

    script_map = {"us": "configure-us.ps1", "cn": "configure-cn.ps1"}
    script = SCRIPT_DIR / script_map[mode]
    if not script.exists():
        log(f"缺少脚本：{script}")
        return False

    args = [
        "-Region", str(settings.get("target_region", "us_west")),
        "-ProxyPort", str(settings.get("proxy_port", 10808)),
    ]
    if settings.get("disable_ipv6", True):
        args.append("-DisableIPv6")

    log(f"开始一键配置（模式：{mode.upper()}）...")
    if _is_admin():
        rc = _stream_process(
            ["powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass",
             "-File", str(script)] + args, log)
    else:
        rc = _elevated_powershell(script, args, log)
        # 提权后输出不在本进程，脚本内部写日志文件
        log("配置脚本已在提权窗口中运行；完成后请回到本界面重新「检查环境」。")
        return rc == 0
    if rc == 0:
        log("一键配置完成，建议重新「检查环境」确认全部正常。")
    else:
        log(f"配置脚本退出码 {rc}，请查看上方输出。")
    return rc == 0


def restart_computer(log: LogFn) -> bool:
    if os.name != "nt":
        log("重启功能仅支持 Windows。")
        return False
    log("60 秒后重启电脑……（可运行 shutdown /a 取消）")
    return _stream_process(["shutdown.exe", "/r", "/t", "60", "/c", "Claude 环境卫士：配置完成，即将重启"], log) == 0
