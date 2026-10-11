"""单个检查项行：状态圆点 + 名称 + 状态标签 + 右侧详情。"""
from __future__ import annotations

from PySide6.QtCore import Qt
from PySide6.QtWidgets import QFrame, QHBoxLayout, QLabel

from .theme import STATUS_COLOR, STATUS_TEXT

_DOT = "●"


class CheckRow(QFrame):
    def __init__(self, key: str, name: str, parent=None):
        super().__init__(parent)
        self.key = key
        self.setObjectName("checkRow")
        self.setMinimumHeight(38)

        layout = QHBoxLayout(self)
        layout.setContentsMargins(14, 4, 14, 4)
        layout.setSpacing(12)

        self.dot = QLabel(_DOT)
        self.dot.setFixedWidth(16)
        self.dot.setStyleSheet(f"color: {STATUS_COLOR['warn']}; font-size: 12px;")

        self.name_label = QLabel(name)
        self.name_label.setObjectName("checkName")
        self.name_label.setMinimumWidth(170)

        self.status_label = QLabel("待检查")
        self.status_label.setObjectName("statusWarn")
        self.status_label.setMinimumWidth(56)

        self.detail_label = QLabel("—")
        self.detail_label.setObjectName("checkDetail")
        self.detail_label.setTextInteractionFlags(Qt.TextSelectableByMouse)

        layout.addWidget(self.dot)
        layout.addWidget(self.name_label)
        layout.addWidget(self.status_label)
        layout.addStretch(1)
        layout.addWidget(self.detail_label, 1)

    def set_result(self, status: str, detail: str) -> None:
        self.status_label.setObjectName(
            {"ok": "statusOk", "warn": "statusWarn", "fail": "statusFail"}.get(status, "statusWarn"))
        # 强制刷新样式
        self.status_label.style().unpolish(self.status_label)
        self.status_label.style().polish(self.status_label)
        self.status_label.setText(STATUS_TEXT.get(status, status))
        self.detail_label.setText(detail)
        self.dot.setStyleSheet(f"color: {STATUS_COLOR.get(status, STATUS_COLOR['warn'])}; font-size: 12px;")

    def reset(self) -> None:
        self.set_result("warn", "检查中…")
