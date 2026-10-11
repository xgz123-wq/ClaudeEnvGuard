"""主窗口：复刻参考截图的整体布局与交互。"""
from __future__ import annotations

import time
from pathlib import Path

from PySide6.QtCore import Qt
from PySide6.QtWidgets import (
    QGridLayout, QHBoxLayout, QLabel, QMainWindow, QMessageBox,
    QPlainTextEdit, QPushButton, QScrollArea, QVBoxLayout, QWidget, QDialog,
)

from .. import APP_TITLE, __version__
from ..core import actions, cleanup, system
from ..core.actions import load_settings, save_settings
from .cards import InfoCard, TargetCard
from .check_row import CheckRow
from .theme import MAIN_QSS
from .workers import ActionWorker, ChecksWorker

ROWS = [
    ("timezone", "系统时区"),
    ("language", "系统/浏览器语言"),
    ("dns", "DNS 泄露检测"),
    ("base_url", "ANTHROPIC_BASE_URL"),
    ("ipv6", "IPv6"),
    ("webrtc_chrome", "WebRTC (Chrome)"),
    ("webrtc_edge", "WebRTC (Edge)"),
    ("proxy", "系统代理"),
    ("connectivity", "Claude 连通性"),
]

DOCS_DIR = Path(__file__).resolve().parent.parent / "resources" / "docs"


class MainWindow(QMainWindow):
    def __init__(self):
        super().__init__()
        self.setWindowTitle(APP_TITLE)
        self.setMinimumSize(980, 780)
        self.resize(1020, 840)

        self.settings = load_settings()
        self.checks_worker: ChecksWorker | None = None
        self.action_worker: ActionWorker | None = None

        root = QWidget()
        root.setObjectName("root")
        self.setCentralWidget(root)
        outer = QVBoxLayout(root)
        outer.setContentsMargins(18, 14, 18, 14)
        outer.setSpacing(10)

        outer.addWidget(self._build_header())

        top = QHBoxLayout()
        top.setSpacing(12)
        self.info_card = InfoCard()
        self.target_card = TargetCard()
        top.addWidget(self.info_card, 3)
        top.addWidget(self.target_card, 2)
        outer.addLayout(top)

        section = QLabel("📋 环境检查项")
        section.setObjectName("sectionTitle")
        outer.addWidget(section)

        self.rows: dict[str, CheckRow] = {}
        rows_host = QWidget()
        rows_layout = QVBoxLayout(rows_host)
        rows_layout.setContentsMargins(0, 0, 0, 0)
        rows_layout.setSpacing(6)
        for key, name in ROWS:
            row = CheckRow(key, name)
            self.rows[key] = row
            rows_layout.addWidget(row)
        outer.addWidget(rows_host)

        outer.addWidget(self._build_action_buttons())
        outer.addWidget(self._build_bottom_bar())

        log_title = QLabel("执行日志")
        log_title.setObjectName("infoKey")
        outer.addWidget(log_title)
        self.log_view = QPlainTextEdit()
        self.log_view.setObjectName("logView")
        self.log_view.setReadOnly(True)
        self.log_view.setMaximumBlockCount(2000)
        outer.addWidget(self.log_view, 1)

        # 目标地区联动
        self.target_card.set_region(self.settings.get("target_region", "us_west"))
        self.target_card.ipv6_check.setChecked(bool(self.settings.get("disable_ipv6", True)))
        self.target_card.combo.currentIndexChanged.connect(self._save_target)
        self.target_card.ipv6_check.toggled.connect(self._save_target)

        self.log(f"{APP_TITLE}（开源复刻版）已启动。点击「检查环境」开始全面检测。")

    # ------------------------------------------------------------ 构建

    def _build_header(self) -> QWidget:
        w = QWidget()
        h = QHBoxLayout(w)
        h.setContentsMargins(2, 0, 2, 4)
        title = QLabel("🛡  Claude 环境卫士")
        title.setObjectName("appTitle")
        badge = QLabel(f"v{__version__} · 检查·配置·恢复｜开源复刻版")
        badge.setObjectName("appBadge")
        h.addWidget(title)
        h.addStretch(1)
        h.addWidget(badge, 0, Qt.AlignmentFlag.AlignBottom)
        return w

    def _build_action_buttons(self) -> QWidget:
        w = QWidget()
        grid = QGridLayout(w)
        grid.setContentsMargins(0, 4, 0, 0)
        grid.setHorizontalSpacing(10)
        grid.setVerticalSpacing(8)

        self.btn_check = QPushButton("🔍  检查环境")
        self.btn_check.setObjectName("btnCheck")
        self.btn_env = QPushButton("⚡  一键配置环境")
        self.btn_env.setObjectName("btnEnv")
        self.btn_cn = QPushButton("CN  一键配置中国")
        self.btn_cn.setObjectName("btnCN")
        self.btn_us = QPushButton("US  一键配置美国")
        self.btn_us.setObjectName("btnUS")

        self.btn_clean_code = QPushButton("🧹  清理Claude Code凭据")
        self.btn_clean_desktop = QPushButton("🧹  清理Claude桌面端")
        self.btn_clean_web = QPushButton("🧹  清理Claude网页版")
        for b in (self.btn_clean_code, self.btn_clean_desktop, self.btn_clean_web):
            b.setObjectName("btnClean")

        grid.addWidget(self.btn_check, 0, 0)
        grid.addWidget(self.btn_env, 0, 1)
        grid.addWidget(self.btn_cn, 0, 2)
        grid.addWidget(self.btn_us, 0, 3)
        grid.addWidget(self.btn_clean_code, 1, 0)
        grid.addWidget(self.btn_clean_desktop, 1, 1)
        grid.addWidget(self.btn_clean_web, 1, 2)

        self.btn_check.clicked.connect(self.run_checks)
        self.btn_env.clicked.connect(lambda: self.run_configure("env"))
        self.btn_cn.clicked.connect(lambda: self.run_configure("cn"))
        self.btn_us.clicked.connect(lambda: self.run_configure("us"))
        self.btn_clean_code.clicked.connect(lambda: self.run_cleanup("code"))
        self.btn_clean_desktop.clicked.connect(lambda: self.run_cleanup("desktop"))
        self.btn_clean_web.clicked.connect(lambda: self.run_cleanup("web"))
        return w

    def _build_bottom_bar(self) -> QWidget:
        w = QWidget()
        h = QHBoxLayout(w)
        h.setContentsMargins(0, 2, 0, 0)
        h.setSpacing(8)

        self.btn_envvar = QPushButton("📋  环境变量检查")
        self.btn_tzlang = QPushButton("🕒  时区语言检查")
        self.btn_dns = QPushButton("🌐  DNS 解析检查")
        self.btn_restart = QPushButton("🔄  重启电脑")
        for b in (self.btn_envvar, self.btn_tzlang, self.btn_dns):
            b.setObjectName("btnNeutral")
        self.btn_restart.setObjectName("btnRestart")

        self.btn_doc_check = QPushButton("📄  环境检查说明")
        self.btn_doc_help = QPushButton("📄  软件操作说明")
        self.btn_doc_check.setObjectName("btnDoc")
        self.btn_doc_help.setObjectName("btnDoc")

        h.addWidget(self.btn_envvar)
        h.addWidget(self.btn_tzlang)
        h.addWidget(self.btn_dns)
        h.addWidget(self.btn_restart)
        h.addStretch(1)
        h.addWidget(self.btn_doc_check)
        h.addWidget(self.btn_doc_help)

        self.btn_envvar.clicked.connect(self.show_envvars)
        self.btn_tzlang.clicked.connect(self.show_tzlang)
        self.btn_dns.clicked.connect(self.show_dns)
        self.btn_restart.clicked.connect(self.restart_pc)
        self.btn_doc_check.clicked.connect(lambda: self.show_doc("环境检查说明.md"))
        self.btn_doc_help.clicked.connect(lambda: self.show_doc("软件操作说明.md"))
        return w

    # ------------------------------------------------------------ 基础

    def log(self, msg: str) -> None:
        self.log_view.appendPlainText(f"[{time.strftime('%H:%M:%S')}] {msg}")

    def _save_target(self, *_args) -> None:
        self.settings["target_region"] = self.target_card.region()
        self.settings["disable_ipv6"] = self.target_card.disable_ipv6()
        save_settings(self.settings)

    def _set_busy(self, busy: bool, what: str = "") -> None:
        buttons = (
            self.btn_check, self.btn_env, self.btn_cn, self.btn_us,
            self.btn_clean_code, self.btn_clean_desktop, self.btn_clean_web,
        )
        for b in buttons:
            b.setEnabled(not busy)
        self.btn_check.setText("⏳  检查中…" if busy else "🔍  检查环境")
        if busy:
            self.log(f"{what}…")

    def confirm(self, title: str, text: str) -> bool:
        btn = QMessageBox.question(self, title, text)
        return btn == QMessageBox.StandardButton.Yes

    # ------------------------------------------------------------ 检查

    def run_checks(self) -> None:
        if self.checks_worker and self.checks_worker.isRunning():
            return
        self._save_target()
        self._set_busy(True, "开始全面环境检查")
        for row in self.rows.values():
            row.reset()

        self.checks_worker = ChecksWorker(dict(self.settings), self)
        self.checks_worker.exit_info.connect(self._on_exit_info)
        self.checks_worker.progressed.connect(self._on_check_progress)
        self.checks_worker.finished_all.connect(self._on_checks_done)
        self.checks_worker.start()

    def _on_exit_info(self, info) -> None:
        proxy = system.get_system_proxy()
        proxy_text = proxy.server if (proxy.enabled and proxy.server) else (
            "PAC 模式" if proxy.pac_url else ("自动检测" if proxy.auto_detect else "未启用"))
        self.info_card.set_exit_info(info, proxy_text)

    def _on_check_progress(self, result) -> None:
        row = self.rows.get(result.key)
        if row:
            row.set_result(result.status, result.detail)
        self.log(f"{result.name}：{result.detail}")

    def _on_checks_done(self, results) -> None:
        self._set_busy(False)
        ok = sum(1 for r in results if r.status == "ok")
        warn = sum(1 for r in results if r.status == "warn")
        fail = sum(1 for r in results if r.status == "fail")
        if fail:
            self.log(f"检查完成：{ok} 项正常，{warn} 项需注意，{fail} 项异常。")
        elif warn:
            self.log(f"检查完成：{ok} 项正常，{warn} 项需注意。")
        else:
            self.log(f"检查完成：全部 {ok} 项正常 ✔")
        self.checks_worker = None

    # ------------------------------------------------------------ 配置 / 清理

    def run_configure(self, mode: str) -> None:
        self._save_target()
        labels = {"env": "一键配置环境", "cn": "一键配置中国", "us": "一键配置美国"}
        label = labels[mode]
        if mode == "env":
            mode = "cn" if self.settings.get("target_region", "").startswith("cn") else "us"
        if not self.confirm(label, f"将按「{self.settings.get('target_region')}」执行系统配置：\n"
                                   f"• 修改系统时区与语言\n"
                                   f"• {'启用' if mode == 'us' else '关闭'}系统代理（端口 {self.settings.get('proxy_port', 10808)}）\n"
                                   f"• {'禁用 IPv6、写入 WebRTC 防泄露策略' if mode == 'us' else '恢复 IPv6、还原 WebRTC 策略'}\n\n"
                                   f"需要管理员权限，继续？"):
            return
        self._run_action(
            lambda log: actions.run_configure(mode, dict(self.settings), log),
            label,
        )

    def run_cleanup(self, which: str) -> None:
        labels = {"code": "清理Claude Code凭据", "desktop": "清理Claude桌面端", "web": "清理Claude网页版"}
        label = labels[which]
        if which == "desktop":
            text = (f"{label}只清除登录与缓存（Cookies、Local Storage、IndexedDB、Cache 等），"
                    "保留 MCP 配置（claude_desktop_config.json）。\n"
                    "清理前会先备份，清理后需要重新登录；请先完全退出 Claude 桌面端。\n\n确认继续？")
        else:
            text = f"{label}会先备份再删除，且删除后需要重新登录。\n\n确认继续？"
        if not self.confirm(label, text):
            return
        fn = cleanup.CLEANUP_FUNCS[which]
        self._run_action(lambda log: fn(log, yes=True), label)

    def _run_action(self, fn, title: str) -> None:
        if self.action_worker and self.action_worker.isRunning():
            QMessageBox.information(self, "请稍候", "上一个任务还没执行完。")
            return
        self._set_busy(True, f"开始执行：{title}")
        self.action_worker = ActionWorker(fn, title, self)
        self.action_worker.logged.connect(self.log)
        self.action_worker.done.connect(self._on_action_done)
        self.action_worker.start()

    def _on_action_done(self, ok: bool, title: str) -> None:
        self._set_busy(False)
        self.log(f"{title}：{'完成' if ok else '未完成（见上方日志）'}")
        self.action_worker = None

    # ------------------------------------------------------------ 底部小工具

    def show_envvars(self) -> None:
        lines = []
        for name in sorted(k for k in system.os.environ if "ANTHROPIC" in k.upper() or "CLAUDE" in k.upper() or "PROXY" in k.upper()):
            lines.append(f"{name} = {system.os.environ[name]}")
        base = system.get_anthropic_base_url()
        text = "\n".join(lines) if lines else "（当前进程无 ANTHROPIC/CLAUDE/PROXY 相关环境变量）"
        text += f"\n\nANTHROPIC_BASE_URL（持久层）: {base or '未设置'}"
        self.log("已导出环境变量检查。")
        self._info_dialog("环境变量检查", text)

    def show_tzlang(self) -> None:
        text = (
            f"系统时区：{system.get_timezone()}\n"
            f"系统语言：{system.get_system_language()}\n"
            f"Chrome 语言：{system.get_browser_language('chrome') or '未检测到'}\n"
            f"Edge 语言：{system.get_browser_language('edge') or '未检测到'}"
        )
        self.log("已导出时区语言检查。")
        self._info_dialog("时区语言检查", text)

    def show_dns(self) -> None:
        servers = system.get_dns_servers()
        try:
            import socket
            host = socket.gethostname()
            resolved = socket.gethostbyname(host)
        except Exception:
            host, resolved = "?", "?"
        text = (
            f"本机 IPv4 DNS：{'，'.join(servers) if servers else '未读取到'}\n"
            f"主机名：{host}\n"
            f"本机解析 IP：{resolved}"
        )
        self.log("已导出 DNS 解析检查。")
        self._info_dialog("DNS 解析检查", text)

    def restart_pc(self) -> None:
        if self.confirm("重启电脑", "60 秒后重启电脑，确定？\n（取消可运行 shutdown /a）"):
            self.log("已发起 60 秒后重启。")
            actions.restart_computer(self.log)

    def show_doc(self, filename: str) -> None:
        path = DOCS_DIR / filename
        try:
            text = path.read_text(encoding="utf-8")
        except Exception:
            text = f"（未找到文档：{path}）"
        self._info_dialog(filename.replace(".md", ""), text, monospace=False)

    def _info_dialog(self, title: str, text: str, monospace: bool = True) -> None:
        dlg = QDialog(self)
        dlg.setWindowTitle(title)
        dlg.resize(560, 420)
        layout = QVBoxLayout(dlg)
        view = QPlainTextEdit()
        view.setReadOnly(True)
        view.setPlainText(text)
        if monospace:
            view.setStyleSheet("font-family: Consolas, monospace; font-size: 12px;")
        layout.addWidget(view)
        self._style_dialog(dlg)
        dlg.exec()

    @staticmethod
    def _style_dialog(dlg: QDialog) -> None:
        dlg.setStyleSheet(MAIN_QSS)
