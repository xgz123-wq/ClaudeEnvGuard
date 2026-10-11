#!/usr/bin/env python3
"""打包成 Windows 单文件 exe。

用法（在 Windows 上）：
    pip install -r requirements.txt pyinstaller
    python packaging/build.py

产物：dist/ClaudeEnvGuard.exe
"""
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def main() -> int:
    cmd = [
        sys.executable, "-m", "PyInstaller",
        "--noconfirm", "--clean",
        "--onefile", "--windowed",
        "--name", "ClaudeEnvGuard",
        "--icon", str(ROOT / "app" / "resources" / "icon.ico"),
        "--add-data", f"{ROOT / 'app' / 'scripts'};app/scripts",
        "--add-data", f"{ROOT / 'app' / 'resources'};app/resources",
        "--collect-submodules", "app",
        str(ROOT / "main.py"),
    ]
    print("运行:", " ".join(cmd))
    return subprocess.call(cmd, cwd=ROOT)


if __name__ == "__main__":
    sys.exit(main())
