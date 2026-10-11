@echo off
REM Uninstall ClaudeEnvGuard: remove shortcuts, uninstall registry entry, and D:\Tools\ClaudeEnvGuard
REM Backups in %LOCALAPPDATA%\ClaudeEnvGuard are kept.
set "APPDIR=%~dp0"
set "APPDIR=%APPDIR:~0,-1%"
echo This will remove:
echo   %APPDIR%
echo   Desktop and Start menu shortcuts
echo   Windows "Apps" uninstall entry
echo Backups in %LOCALAPPDATA%\ClaudeEnvGuard will be KEPT.
choice /M "Continue"
if errorlevel 2 exit /b 0
taskkill /IM ClaudeEnvGuard.exe /F >nul 2>&1
powershell -NoProfile -Command "$n=[char]0x73AF+[char]0x5883+[char]0x536B+[char]0x58EB; foreach($d in [Environment]::GetFolderPath('Desktop'), (Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs')){ Remove-Item -LiteralPath (Join-Path $d ('Claude '+$n+'.lnk')) -ErrorAction SilentlyContinue }"
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\ClaudeEnvGuard" /f >nul 2>&1
echo Done. Removing program folder...
cd /d "%TEMP%"
start "" /min cmd /c "timeout /t 2 >nul & rmdir /s /q "%APPDIR%""
exit
