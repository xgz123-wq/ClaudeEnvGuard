"""顶部两张卡片：网络出口信息 / 目标地区配置。"""
from __future__ import annotations

from PySide6.QtCore import Qt
from PySide6.QtWidgets import (
    QCheckBox, QComboBox, QFrame, QGridLayout, QHBoxLayout, QLabel, QVBoxLayout,
)

REGIONS = [
    ("us_west", "us_west（美国西部）"),
    ("us_east", "us_east（美国东部）"),
    ("us_central", "us_central（美国中部）"),
    ("uk", "uk（英国）"),
    ("jp", "jp（日本）"),
    ("sg", "sg（新加坡）"),
    ("cn", "cn（中国）"),
]


class InfoCard(QFrame):
    """网络出口信息。"""

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setObjectName("card")
        layout = QVBoxLayout(self)
        layout.setContentsMargins(16, 12, 16, 12)
        layout.setSpacing(8)

        title = QLabel("🌐 网络出口信息")
        title.setObjectName("cardTitle")
        layout.addWidget(title)

        grid = QGridLayout()
        grid.setHorizontalSpacing(18)
        grid.setVerticalSpacing(6)
        layout.addLayout(grid)

        self.ip_value = self._kv(grid, 0, 0, "IP 地址：")
        self.country_value = self._kv(grid, 0, 1, "国家/地区：")
        self.city_value = self._kv(grid, 1, 0, "城市：")
        self.isp_value = self._kv(grid, 1, 1, "ISP/运营商：")
        self.asn_value = self._kv(grid, 2, 0, "ASN：")
        self.proxy_value = self._kv(grid, 2, 1, "代理出口：", style_id="proxyValue")

    def _kv(self, grid, row, col, key, style_id="infoValue") -> QLabel:
        k = QLabel(key)
        k.setObjectName("infoKey")
        v = QLabel("—")
        v.setObjectName(style_id)
        v.setTextInteractionFlags(Qt.TextSelectableByMouse)
        grid.addWidget(k, row, col * 2)
        grid.addWidget(v, row, col * 2 + 1)
        return v

    def set_exit_info(self, info, proxy_text: str) -> None:
        if info and info.ok:
            self.ip_value.setText(info.ip or "—")
            cc = f"{info.country}（{info.country_code}）" if info.country_code else info.country
            self.country_value.setText(cc or "—")
            self.city_value.setText(info.city or "—")
            asn, isp = _split_asn(info.org)
            self.asn_value.setText(asn or "—")
            self.isp_value.setText(isp or "—")
        elif info:
            self.ip_value.setText("获取失败")
            self.country_value.setText("—")
            self.city_value.setText("—")
            self.asn_value.setText("—")
            self.isp_value.setText("—")
        self.proxy_value.setText(proxy_text)


def _split_asn(org: str) -> tuple[str, str]:
    import re
    m = re.match(r"^(AS\d+)\s+(.*)$", (org or "").strip())
    if m:
        return m.group(1), m.group(2)
    return "", org or ""


class TargetCard(QFrame):
    """目标地区配置 + 禁用 IPv6。"""

    def __init__(self, parent=None):
        super().__init__(parent)
        self.setObjectName("card")
        layout = QVBoxLayout(self)
        layout.setContentsMargins(16, 12, 16, 12)
        layout.setSpacing(10)

        title = QLabel("🎯 目标地区配置")
        title.setObjectName("cardTitle")
        layout.addWidget(title)

        row = QHBoxLayout()
        row.setSpacing(10)
        row.addWidget(QLabel("目标地区:"))
        self.combo = QComboBox()
        for key, label in REGIONS:
            self.combo.addItem(label, key)
        row.addWidget(self.combo)
        self.ipv6_check = QCheckBox("禁用 IPv6")
        self.ipv6_check.setChecked(True)
        row.addWidget(self.ipv6_check)
        row.addStretch(1)
        layout.addLayout(row)

        hint = QLabel("提示：「一键配置环境」按此处选择的地区执行。")
        hint.setObjectName("infoKey")
        layout.addWidget(hint)

    def region(self) -> str:
        return self.combo.currentData()

    def set_region(self, region: str) -> None:
        for i in range(self.combo.count()):
            if self.combo.itemData(i) == region:
                self.combo.setCurrentIndex(i)
                return

    def disable_ipv6(self) -> bool:
        return self.ipv6_check.isChecked()
