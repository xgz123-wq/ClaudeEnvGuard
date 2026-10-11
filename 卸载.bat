@echo off
REM Uninstall Claude Env Guard program files only.
REM Source code folders, git history, docs and backups in %LOCALAPPDATA%\ClaudeEnvGuard are KEPT.
set "APPDIR=%~dp0"
set "APPDIR=%APPDIR:~0,-1%"
echo This will remove:
echo   %APPDIR%\ClaudeEnvGuard.exe
echo   %APPDIR%\ceg.exe
echo   the old v1 exe in %APPDIR%
echo   Desktop and Start menu shortcuts
echo   Windows "Apps" uninstall entry
echo   WebView2 cache %APPDATA%\ClaudeEnvGuard.exe
echo KEPT: source code, git history, docs, settings/backups/logs in %LOCALAPPDATA%\ClaudeEnvGuard
choice /M "Continue"
if errorlevel 2 exit /b 0
taskkill /IM ClaudeEnvGuard.exe /F >nul 2>&1
del /q "%APPDIR%\ClaudeEnvGuard.exe" >nul 2>&1
del /q "%APPDIR%\ceg.exe" >nul 2>&1
powershell -NoProfile -Command "$n=[char]0x73AF+[char]0x5883+[char]0x536B+[char]0x58EB; foreach($d in [Environment]::GetFolderPath('Desktop'), (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs')){ Remove-Item -LiteralPath (Join-Path $d ('Claude '+$n+'.lnk')) -ErrorAction SilentlyContinue }; $old='ClaudeEnvGuard-'+[char]0x65E7+[char]0x7248+'.exe'; Remove-Item -LiteralPath (Join-Path $env:APPDIR $old) -ErrorAction SilentlyContinue"
if exist "%APPDATA%\ClaudeEnvGuard.exe" rmdir /s /q "%APPDATA%\ClaudeEnvGuard.exe"
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\ClaudeEnvGuard" /f >nul 2>&1
echo Done. Source code and backups were kept in %APPDIR% and %LOCALAPPDATA%\ClaudeEnvGuard.
pause
