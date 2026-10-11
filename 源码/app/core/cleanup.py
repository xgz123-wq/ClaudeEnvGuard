"""三项清理：Claude Code 凭据 / Claude 桌面端 / Claude 网页版。

原则：先备份、再清理；能列出清单就先列清单；不删任何不在清单内的东西。
"""
from __future__ import annotations

import json
import os
import shutil
import sqlite3
import subprocess
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Optional

LogFn = Callable[[str], None]

_CLAUDE_CODE_FILES = [".credentials.json"]
_CLAUDE_CODE_KEYS = ["oauthAccount", "primaryApiKey"]


@dataclass
class CleanupReport:
    backed_up: list[str] = field(default_factory=list)
    removed: list[str] = field(default_factory=list)
    skipped: list[str] = field(default_factory=list)
    errors: list[str] = field(default_factory=list)

    @property
    def ok(self) -> bool:
        return not self.errors


def _stamp() -> str:
    return time.strftime("%Y%m%d-%H%M%S")


def _backup(path: Path, log: LogFn) -> Optional[Path]:
    if not path.exists():
        return None
    backup_dir = Path(os.environ.get("LOCALAPPDATA", str(Path.home() / ".cache"))) / "ClaudeEnvGuard" / "backups" / _stamp()
    backup_dir.mkdir(parents=True, exist_ok=True)
    dest = backup_dir / (path.name + ".bak")
    try:
        if path.is_dir():
            shutil.copytree(path, dest)
        else:
            shutil.copy2(path, dest)
        log(f"已备份：{path} -> {dest}")
        return dest
    except Exception as exc:  # noqa: BLE001
        log(f"备份失败：{path}（{exc}），跳过清理")
        return None


# ---------------------------------------------------------------- Code 凭据

def cleanup_code_credentials(log: LogFn, yes: bool = True) -> CleanupReport:
    """清理 Claude Code 的登录凭据与本地会话记录。"""
    report = CleanupReport()
    home = Path.home()
    claude_dir = home / ".claude"
    log(f"目标目录：{claude_dir}")

    if not claude_dir.is_dir():
        report.skipped.append(f"目录不存在：{claude_dir}")
        log("未安装 Claude Code 或目录不存在，无需清理。")
        return report

    for name in _CLAUDE_CODE_FILES:
        target = claude_dir / name
        if target.exists():
            if yes:
                if _backup(target, log):
                    try:
                        target.unlink()
                        report.removed.append(str(target))
                        log(f"已删除：{target}")
                    except Exception as exc:  # noqa: BLE001
                        report.errors.append(f"删除失败 {target}：{exc}")
            else:
                report.skipped.append(f"待删除：{target}")

    # .claude.json 里的会话字段（保留其他设置）
    state_file = home / ".claude.json"
    if state_file.exists() and yes:
        try:
            data = json.loads(state_file.read_text(encoding="utf-8"))
            changed = False
            for key in _CLAUDE_CODE_KEYS:
                if key in data:
                    del data[key]
                    changed = True
            if changed:
                _backup(state_file, log)
                state_file.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
                report.removed.append(f"{state_file}（会话字段）")
                log(f"已清理 {state_file} 中的登录会话字段")
        except Exception as exc:  # noqa: BLE001
            report.errors.append(f"处理 {state_file} 失败：{exc}")

    log("Claude Code 凭据清理完成；重新运行 claude 时需要重新登录。")
    return report


# ---------------------------------------------------------------- 桌面端

# 只允许删除这些登录 / 会话 / 缓存项；其余（claude_desktop_config.json、config.json、
# claude-code、extensions 等）一律不碰
_DESKTOP_ALLOWLIST = [
    "Cookies", "Cookies-journal", "Network", "Local Storage", "Session Storage",
    "IndexedDB", "Partitions", "Cache", "Code Cache", "GPUCache", "blob_storage",
]


def _claude_exe_running() -> bool:
    """用 tasklist 检查是否有 claude.exe 在运行。"""
    try:
        out = subprocess.run(
            ["tasklist", "/FI", "IMAGENAME eq claude.exe"],
            capture_output=True, text=True, timeout=15, errors="replace",
        ).stdout or ""
    except Exception:
        return False
    return "claude.exe" in out.lower()


def cleanup_desktop(log: LogFn, yes: bool = True) -> CleanupReport:
    """只清理 Claude Desktop 的登录 / 会话 / 缓存，保留 MCP 配置（claude_desktop_config.json）。"""
    report = CleanupReport()
    appdata = Path(os.environ.get("APPDATA", str(Path.home() / ".config")))
    local = Path(os.environ.get("LOCALAPPDATA", str(Path.home() / ".local" / "share")))

    if yes and _claude_exe_running():
        msg = "请先完全退出 Claude 桌面端再清理（如 Claude Code 命令行正在运行也需先退出）"
        report.errors.append(msg)
        log(msg)
        return report

    roots = [appdata / "Claude", local / "Claude-3p"]
    for root in roots:
        log(f"检查：{root}")
        if not root.is_dir():
            report.skipped.append(f"不存在：{root}")
            continue
        for name in _DESKTOP_ALLOWLIST:
            target = root / name
            if not target.exists():
                continue
            if not yes:
                report.skipped.append(f"待删除：{target}")
                continue
            if _backup(target, log):
                try:
                    if target.is_dir():
                        shutil.rmtree(target)
                    else:
                        target.unlink()
                    report.removed.append(str(target))
                    log(f"已删除：{target}")
                except Exception as exc:  # noqa: BLE001
                    report.errors.append(f"删除失败 {target}：{exc}")
    log("Claude Desktop 清理完成：仅清除登录与缓存，MCP 配置（claude_desktop_config.json）已保留；需要重新登录。")
    return report


# ---------------------------------------------------------------- 网页版

_COOKIE_HOST_PATTERNS = ("%claude.ai%", "%anthropic.com%", "%claudeusercontent.com%")


def _browser_user_data(browser: str) -> Optional[Path]:
    if os.name == "nt":
        local = Path(os.environ.get("LOCALAPPDATA", ""))
        roots = {
            "chrome": local / "Google" / "Chrome" / "User Data",
            "edge": local / "Microsoft" / "Edge" / "User Data",
        }
    else:
        home = Path.home()
        roots = {
            "chrome": home / ".config" / "google-chrome",
            "edge": home / ".config" / "microsoft-edge",
        }
    return roots.get(browser)


def _iter_cookie_dbs(root: Path) -> list[Path]:
    dbs: list[Path] = []
    if not root.is_dir():
        return dbs
    candidates = [root / "Default"]
    candidates += [p for p in root.glob("Profile *") if p.is_dir()]
    for profile in candidates:
        for rel in ("Network/Cookies", "Cookies"):
            db = profile / rel
            if db.exists():
                dbs.append(db)
    return dbs


def cleanup_web(log: LogFn, yes: bool = True) -> CleanupReport:
    """删除 Chrome / Edge 中 claude.ai / anthropic.com 的 Cookie。"""
    report = CleanupReport()
    for browser in ("chrome", "edge"):
        root = _browser_user_data(browser)
        if not root or not root.is_dir():
            report.skipped.append(f"{browser}: 未安装")
            continue
        dbs = _iter_cookie_dbs(root)
        if not dbs:
            report.skipped.append(f"{browser}: 无 Cookie 数据库")
            continue
        for db in dbs:
            try:
                with sqlite3.connect(f"file:{db}?mode=ro", uri=True) as conn:
                    count = 0
                    for pattern in _COOKIE_HOST_PATTERNS:
                        cur = conn.execute(
                            "SELECT COUNT(*) FROM cookies WHERE host_key LIKE ?", (pattern,))
                        count += cur.fetchone()[0]
                if count == 0:
                    report.skipped.append(f"{browser} {db.name}: 无相关 Cookie")
                    continue
                log(f"{browser} {db}: 发现 {count} 条相关 Cookie")
                if not yes:
                    report.skipped.append(f"待删除：{db}（{count} 条）")
                    continue
                if _backup(db, log):
                    with sqlite3.connect(str(db)) as conn:
                        for pattern in _COOKIE_HOST_PATTERNS:
                            conn.execute("DELETE FROM cookies WHERE host_key LIKE ?", (pattern,))
                        conn.commit()
                    report.removed.append(f"{browser} {db}（{count} 条）")
                    log(f"已删除 {db} 中的 {count} 条 Cookie")
            except sqlite3.OperationalError as exc:
                if "locked" in str(exc).lower():
                    msg = f"{browser} Cookie 数据库被占用：请先完全关闭 {browser} 再清理"
                    report.errors.append(msg)
                    log(msg)
                else:
                    report.errors.append(f"{db}: {exc}")
            except Exception as exc:  # noqa: BLE001
                report.errors.append(f"{db}: {exc}")
    log("网页版 Cookie 清理完成；网页版需要重新登录。")
    return report


CLEANUP_FUNCS = {
    "code": cleanup_code_credentials,
    "desktop": cleanup_desktop,
    "web": cleanup_web,
}
