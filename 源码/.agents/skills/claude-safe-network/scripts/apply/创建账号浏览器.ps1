#requires -Version 7.0
param(
  [Parameter(Mandatory)]
  [ValidatePattern('^[\p{L}\p{N}_-]{1,40}$')]
  [string] $Alias,

  [ValidatePattern('^[\p{L}\p{N}_-]{1,40}$')]
  [string] $ShortcutName,

  [string] $StartUrl = 'https://claude.ai/',

  [switch] $Launch
)

$ErrorActionPreference = 'Stop'
$chromeCandidates = @(
  (Join-Path $env:ProgramFiles 'Google\Chrome\Application\chrome.exe'),
  (Join-Path ${env:ProgramFiles(x86)} 'Google\Chrome\Application\chrome.exe'),
  (Join-Path $env:LOCALAPPDATA 'Google\Chrome\Application\chrome.exe')
)
$chrome = $chromeCandidates | Where-Object { $_ -and (Test-Path -LiteralPath $_) } | Select-Object -First 1
if (-not $chrome) { throw '没有找到 Google Chrome。' }

$listener = Get-NetTCPConnection -State Listen -LocalPort 43866 -ErrorAction SilentlyContinue
if (-not $listener) { throw '专用 SOCKS 端口 43866 未监听。' }

$root = Join-Path $env:LOCALAPPDATA (Join-Path 'ClaudeAccountBrowsers' $Alias)
$userData = Join-Path $root 'UserData'
$shortcutFileName = if ($ShortcutName) { "$ShortcutName.lnk" } else { "Claude-$Alias-专用Chrome.lnk" }
$shortcutPath = Join-Path ([Environment]::GetFolderPath('Desktop')) $shortcutFileName
if (Test-Path -LiteralPath $shortcutPath) {
  throw "快捷方式已存在，不覆盖: $shortcutPath"
}
if (Test-Path -LiteralPath $userData) {
  $existing = @(Get-ChildItem -LiteralPath $userData -Force -ErrorAction SilentlyContinue)
  if ($existing.Count -gt 0) { throw "User Data 目录不是空目录，不接管: $userData" }
} else {
  New-Item -ItemType Directory -Path $userData -Force | Out-Null
}

$arguments = @(
  "--user-data-dir=`"$userData`"",
  '--proxy-server="socks5://127.0.0.1:43866"',
  '--host-resolver-rules="MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost"',
  '--lang=en-US',
  '--accept-lang="en-US,en"',
  '--force-webrtc-ip-handling-policy=disable_non_proxied_udp',
  $StartUrl
) -join ' '

$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = $chrome
$shortcut.Arguments = $arguments
$shortcut.WorkingDirectory = Split-Path -Parent $chrome
$shortcut.IconLocation = "$chrome,0"
$shortcut.Description = "Claude 独立账号浏览器：$Alias"
$shortcut.Save()

$verify = $shell.CreateShortcut($shortcutPath)
if ($verify.Arguments -notlike '*--user-data-dir=*' -or $verify.Arguments -notlike '*127.0.0.1:43866*') {
  throw '快捷方式创建后的参数校验失败。'
}

if ($Launch) {
  Start-Process -FilePath $chrome -ArgumentList $arguments
}

[pscustomobject]@{
  Success = $true
  Alias = $Alias
  UserData = $userData
  Shortcut = $shortcutPath
  Launched = [bool]$Launch
  Notice = '未复制任何 Cookie、Login Data 或旧 Profile；首次使用需人工登录。'
} | ConvertTo-Json -Depth 4
