#requires -Version 7.0
$ErrorActionPreference = 'Stop'

$desktop = [Environment]::GetFolderPath('Desktop')
$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$backupRoot = Join-Path $base 'account-routing-backups'
$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backupDir = Join-Path $backupRoot ($timestamp + '-阶段3-快捷方式')
$oldProxy = 'socks5://127.0.0.1:43865'
$newProxy = 'socks5://127.0.0.1:43866'
$requiredArguments = @(
  '--host-resolver-rules=',
  '--lang=en-US',
  '--accept-lang=',
  '--force-webrtc-ip-handling-policy=disable_non_proxied_udp'
)

$shortcuts = @(Get-ChildItem -LiteralPath $desktop -Filter '*Claude*专用Chrome.lnk' -File)
if ($shortcuts.Count -eq 0) {
  throw '桌面没有找到 Claude 专用 Chrome 快捷方式。'
}

$port = Get-NetTCPConnection -State Listen -LocalPort 43866 -ErrorAction SilentlyContinue
if (-not $port) {
  throw '专用端口 43866 未监听，停止修改快捷方式。'
}

$shell = New-Object -ComObject WScript.Shell
$preview = foreach ($file in $shortcuts) {
  $shortcut = $shell.CreateShortcut($file.FullName)
  if ($shortcut.TargetPath -notmatch '(?i)\\chrome\.exe$') {
    throw "快捷方式目标不是 Chrome: $($file.FullName)"
  }
  if ($shortcut.Arguments -notlike "*$oldProxy*") {
    throw "快捷方式没有预期的旧代理端口，停止覆盖: $($file.FullName)"
  }
  foreach ($argument in $requiredArguments) {
    if ($shortcut.Arguments -notlike "*$argument*") {
      throw "快捷方式缺少防泄漏参数 $argument，停止覆盖: $($file.FullName)"
    }
  }
  [pscustomobject]@{
    Path = $file.FullName
    Before = $shortcut.Arguments
    After = $shortcut.Arguments.Replace($oldProxy, $newProxy)
  }
}

New-Item -ItemType Directory -Path $backupDir -Force | Out-Null
foreach ($item in $preview) {
  Copy-Item -LiteralPath $item.Path -Destination (Join-Path $backupDir (Split-Path -Leaf $item.Path))
}

$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$manifest = [pscustomobject]@{
  CreatedAt = (Get-Date).ToString('o')
  BackupDirectory = $backupDir
  ShortcutChanges = $preview
}
[System.IO.File]::WriteAllText((Join-Path $backupDir 'manifest.json'), ($manifest | ConvertTo-Json -Depth 6), $utf8NoBom)

foreach ($item in $preview) {
  $shortcut = $shell.CreateShortcut($item.Path)
  $shortcut.Arguments = $item.After
  $shortcut.Save()
}

$result = foreach ($item in $preview) {
  $shortcut = $shell.CreateShortcut($item.Path)
  if ($shortcut.Arguments -notlike "*$newProxy*" -or $shortcut.Arguments -like "*$oldProxy*") {
    throw "快捷方式修改后校验失败: $($item.Path)"
  }
  [pscustomobject]@{ Name = Split-Path -Leaf $item.Path; Arguments = $shortcut.Arguments }
}

[pscustomobject]@{
  Success = $true
  BackupDirectory = $backupDir
  Shortcuts = $result
} | ConvertTo-Json -Depth 6
