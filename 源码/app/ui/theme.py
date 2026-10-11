"""深色主题 QSS，复刻参考截图的视觉风格：深蓝紫底 + 橙色标题 + 卡片式布局。"""

MAIN_QSS = """
* {
    font-family: "Microsoft YaHei UI", "Microsoft YaHei", "PingFang SC",
                 "Noto Sans CJK SC", "Segoe UI", sans-serif;
    font-size: 13px;
    color: #dbe2f1;
}

QMainWindow, QWidget#root {
    background: qlineargradient(x1:0, y1:0, x2:1, y2:1,
        stop:0 #131a2b, stop:0.5 #1a2138, stop:1 #141b2e);
}

/* ---------- 顶部标题栏 ---------- */
QLabel#appTitle {
    font-size: 24px;
    font-weight: 800;
    color: #f4f7ff;
    padding: 4px 2px;
}
QLabel#appBadge {
    font-size: 12px;
    color: #9aa7c7;
    padding: 2px 4px;
}
QLabel#cardTitle {
    font-size: 14px;
    font-weight: 700;
    color: #ff9f43;
    padding: 2px 0 6px 0;
}

/* ---------- 卡片 ---------- */
QFrame#card {
    background: rgba(45, 54, 82, 0.55);
    border: 1px solid #38466a;
    border-radius: 10px;
}
QLabel#infoKey { color: #8f9bbd; }
QLabel#infoValue { color: #ffd9a0; font-weight: 600; }
QLabel#proxyValue { color: #7fd4ff; font-weight: 600; }

/* ---------- 检查项行 ---------- */
QFrame#checkRow {
    background: rgba(52, 62, 94, 0.45);
    border: 1px solid #333f60;
    border-radius: 8px;
}
QLabel#checkName { font-weight: 600; color: #e8edf9; }
QLabel#checkDetail { color: #aab6d6; }
QLabel#statusOk   { color: #2ee6a8; font-weight: 700; }
QLabel#statusWarn { color: #ffc94d; font-weight: 700; }
QLabel#statusFail { color: #ff6b6b; font-weight: 700; }

/* ---------- 按钮 ---------- */
QPushButton {
    border: none;
    border-radius: 8px;
    padding: 10px 14px;
    font-weight: 700;
    color: #ffffff;
}
QPushButton:hover  { background: rgba(255, 255, 255, 25); }
QPushButton:pressed { background: rgba(0, 0, 0, 40); }
QPushButton:disabled { color: #6b7694; background: #2c3550; }

QPushButton#btnCheck    { background: #1f9d6b; }
QPushButton#btnEnv      { background: #e8833a; }
QPushButton#btnCN       { background: #b03a48; }
QPushButton#btnUS       { background: #2470c2; }
QPushButton#btnClean    { background: #4a3f6b; }
QPushButton#btnNeutral  { background: #2f3a5c; }
QPushButton#btnDoc      { background: #26314e; }
QPushButton#btnRestart  { background: #6b4a2f; }

/* ---------- 下拉 / 勾选 ---------- */
QComboBox {
    background: #232c47;
    border: 1px solid #3c4a72;
    border-radius: 6px;
    padding: 5px 10px;
    color: #ffd9a0;
    font-weight: 600;
}
QComboBox::drop-down { border: none; width: 22px; }
QComboBox QAbstractItemView {
    background: #232c47;
    border: 1px solid #3c4a72;
    selection-background-color: #3a4a75;
}
QCheckBox { color: #c6d0ea; font-weight: 600; }
QCheckBox::indicator {
    width: 16px; height: 16px;
    border-radius: 4px;
    border: 1px solid #4a5a85;
    background: #232c47;
}
QCheckBox::indicator:checked {
    background: #2f9e6e;
    border-color: #2f9e6e;
}

/* ---------- 日志 ---------- */
QPlainTextEdit#logView {
    background: rgba(16, 21, 36, 0.85);
    border: 1px solid #333f60;
    border-radius: 8px;
    color: #9fe6c3;
    font-family: "Cascadia Code", "Consolas", "Courier New", monospace;
    font-size: 12px;
    padding: 6px;
}

QScrollBar:vertical {
    background: transparent; width: 10px; margin: 2px;
}
QScrollBar::handle:vertical {
    background: #3c4a72; border-radius: 5px; min-height: 30px;
}
QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical { height: 0; }

QLabel#sectionTitle {
    font-size: 14px;
    font-weight: 700;
    color: #ff9f43;
    padding-top: 6px;
}
"""

STATUS_LABEL_STYLE = {
    "ok": "statusOk",
    "warn": "statusWarn",
    "fail": "statusFail",
}
STATUS_TEXT = {"ok": "正常", "warn": "需注意", "fail": "异常"}
STATUS_DOT = {"ok": "●", "warn": "●", "fail": "●"}
STATUS_COLOR = {"ok": "#2ee6a8", "warn": "#ffc94d", "fail": "#ff6b6b"}
