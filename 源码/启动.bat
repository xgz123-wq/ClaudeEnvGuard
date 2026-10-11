@echo off
REM Double-click to open the GUI with the project's own .venv (not conda)
cd /d "%~dp0"
if not exist ".venv\Scripts\pythonw.exe" (
    echo [ERROR] .venv not found. Create it first: python -m venv .venv
    pause
    exit /b 1
)
start "" ".venv\Scripts\pythonw.exe" main.py
