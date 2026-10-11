"""后台线程：跑检查 / 跑动作，避免卡死 UI。"""
from __future__ import annotations

from typing import Callable

from PySide6.QtCore import QThread, Signal

from ..core import actions, cleanup
from ..core.checks import CheckResult, run_all_checks
from ..core.system import ExitInfo, fetch_exit_info


class ChecksWorker(QThread):
    progressed = Signal(object)      # CheckResult
    finished_all = Signal(list)      # list[CheckResult]
    exit_info = Signal(object)       # ExitInfo

    def __init__(self, settings: dict, parent=None):
        super().__init__(parent)
        self.settings = settings

    def run(self) -> None:
        info = fetch_exit_info()
        self.exit_info.emit(info)
        results = run_all_checks(
            self.settings,
            progress=lambda res: self.progressed.emit(res),
        )
        self.finished_all.emit(results)


class ActionWorker(QThread):
    logged = Signal(str)
    done = Signal(bool, str)     # ok, title

    def __init__(self, fn: Callable[[Callable[[str], None]], object], title: str, parent=None):
        super().__init__(parent)
        self.fn = fn
        self.title = title

    def run(self) -> None:
        def log(msg: str) -> None:
            self.logged.emit(str(msg))

        try:
            result = self.fn(log)
            ok = bool(result.ok) if isinstance(result, cleanup.CleanupReport) else bool(result)
        except Exception as exc:  # noqa: BLE001
            log(f"执行异常：{exc}")
            ok = False
        self.done.emit(ok, self.title)
