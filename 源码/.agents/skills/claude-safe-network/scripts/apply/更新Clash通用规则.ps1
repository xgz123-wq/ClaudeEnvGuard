#requires -Version 7.0
$ErrorActionPreference = 'Stop'
if ($env:CLAUDE_ALLOW_CLASH_RESTART -ne '1') { throw '本脚本会停止并重启 Clash 界面与内核，会让 Clash 掉线；日常更新只走热重载。确需运行时先设置 CLAUDE_ALLOW_CLASH_RESTART=1。' }
$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$backupRoot = Join-Path $base 'account-routing-backups'
$sourceScript = Join-Path (Split-Path $PSScriptRoot -Parent) 'clash\Clash全局账号通道.js'
$installedScript = Join-Path $base 'profiles\Script.js'
$runtimeConfig = Join-Path $base 'clash-verge.yaml'
$checkConfig = Join-Path $base 'clash-verge-check.yaml'
$clashApp = 'C:\Program Files\Clash Verge\clash-verge.exe'
$validator = Join-Path (Split-Path $PSScriptRoot -Parent) 'clash\验证Clash账号专用通道.ps1'
# 一律用 PowerShell 7 调子脚本：本套件脚本是无 BOM UTF-8 中文，5.1 会按 ANSI 解析而乱码/失败
$pwshExe = (Get-Process -Id $PID).Path
if (-not $pwshExe -or $pwshExe -notlike '*pwsh.exe') { $pwshExe = Join-Path $env:ProgramFiles 'PowerShell\7\pwsh.exe' }
$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backupDir = Join-Path $backupRoot ($timestamp + '-阶段4-通用Claude规则')
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)

foreach ($path in @($sourceScript, $installedScript, $runtimeConfig, $checkConfig, $clashApp, $validator)) {
  if (-not (Test-Path -LiteralPath $path)) { throw "缺少必需文件: $path" }
}

if (Get-Command node -ErrorAction SilentlyContinue) {
  & node --check $sourceScript
  if ($LASTEXITCODE -ne 0) { throw '全局 JavaScript 扩展语法校验失败。' }
}

New-Item -ItemType Directory -Path $backupDir -Force | Out-Null
foreach ($path in @($installedScript, $runtimeConfig, $checkConfig)) {
  Copy-Item -LiteralPath $path -Destination (Join-Path $backupDir (Split-Path -Leaf $path))
}
$manifest = [pscustomobject]@{
  CreatedAt = (Get-Date).ToString('o')
  BackupDirectory = $backupDir
  Purpose = '更新 Claude 专用规则与经 ACCOUNT-US 的加密 DNS 策略'
  Files = @($installedScript, $runtimeConfig, $checkConfig)
}
[System.IO.File]::WriteAllText((Join-Path $backupDir 'manifest.json'), ($manifest | ConvertTo-Json -Depth 5), $utf8NoBom)

try {
  Copy-Item -LiteralPath $sourceScript -Destination $installedScript -Force
  $ui = Get-Process -Name 'clash-verge' -ErrorAction SilentlyContinue
  if ($ui) {
    $ui | Stop-Process
    $ui | Wait-Process -Timeout 15 -ErrorAction SilentlyContinue
  }
  Start-Process -FilePath $clashApp -WindowStyle Hidden

  $deadline = (Get-Date).AddSeconds(50)
  do {
    Start-Sleep -Milliseconds 750
    $listener = Get-NetTCPConnection -State Listen -LocalPort 43866 -ErrorAction SilentlyContinue
  } until ($listener -or (Get-Date) -ge $deadline)
  if (-not $listener) { throw 'Clash 重启后 43866 未恢复。' }

  $validationOutput = @(& $pwshExe -NoProfile -ExecutionPolicy Bypass -File $validator 2>&1)
  $validationExit = $LASTEXITCODE
  if ($validationExit -ne 0) { throw ('更新后的验收失败: ' + ($validationOutput -join ' | ')) }

  [pscustomobject]@{
    Success = $true
    BackupDirectory = $backupDir
    Validation = (($validationOutput -join "`n") | ConvertFrom-Json)
  } | ConvertTo-Json -Depth 10
} catch {
  Copy-Item -LiteralPath (Join-Path $backupDir 'Script.js') -Destination $installedScript -Force
  Copy-Item -LiteralPath (Join-Path $backupDir 'clash-verge.yaml') -Destination $runtimeConfig -Force
  Copy-Item -LiteralPath (Join-Path $backupDir 'clash-verge-check.yaml') -Destination $checkConfig -Force
  $ui = Get-Process -Name 'clash-verge' -ErrorAction SilentlyContinue
  if ($ui) { $ui | Stop-Process -ErrorAction SilentlyContinue }
  Start-Process -FilePath $clashApp -WindowStyle Hidden
  throw
}
