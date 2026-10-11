@echo off
REM Build dist\ClaudeEnvGuard.exe using the project's own .venv (not conda)
cd /d "%~dp0\.."
if not exist ".venv\Scripts\python.exe" (
    echo [ERROR] .venv not found. Create it first: python -m venv .venv
    pause
    exit /b 1
)
".venv\Scripts\python.exe" -m pip install -r requirements.txt pyinstaller
".venv\Scripts\python.exe" packaging\build.py
pause
