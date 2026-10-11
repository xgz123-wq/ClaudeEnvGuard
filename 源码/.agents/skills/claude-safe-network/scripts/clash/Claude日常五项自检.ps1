#requires -Version 7.0
[CmdletBinding()]
param(
  [ValidatePattern('^[\p{L}\p{N}_-]{1,40}$')]
  [string] $Alias = '账号1',

  [switch] $NoBrowser
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

$validator = Join-Path $PSScriptRoot '验证Clash账号专用通道.ps1'
if (-not (Test-Path -LiteralPath $validator)) {
  throw "缺少验证程序: $validator"
}

$engine = (Get-Process -Id $PID).Path
$rawResult = & $engine -NoProfile -ExecutionPolicy Bypass -File $validator 2>&1 | Out-String
$validatorExitCode = $LASTEXITCODE
try {
  $result = $rawResult | ConvertFrom-Json
} catch {
  throw "无法读取验证程序结果。原始输出:`n$rawResult"
}

$desktop = [Environment]::GetFolderPath('Desktop')
$shortcutPath = Join-Path $desktop ("Claude-$Alias-专用Chrome.lnk")
$expectedUserData = Join-Path $env:LOCALAPPDATA (Join-Path 'ClaudeAccountBrowsers' (Join-Path $Alias 'UserData'))
$shortcutArguments = ''
if (Test-Path -LiteralPath $shortcutPath) {
  $shell = New-Object -ComObject WScript.Shell
  $shortcut = $shell.CreateShortcut($shortcutPath)
  $shortcutArguments = [string] $shortcut.Arguments
}

$browserEntryReady =
  (Test-Path -LiteralPath $shortcutPath) -and
  (Test-Path -LiteralPath $expectedUserData) -and
  ($shortcutArguments -like '*--user-data-dir=*') -and
  ($shortcutArguments -like '*127.0.0.1:43866*') -and
  ($shortcutArguments -like '*--lang=en-US*') -and
  ($shortcutArguments -like '*--force-webrtc-ip-handling-policy=disable_non_proxied_udp*')

$runtimePath = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev\clash-verge.yaml'
$runtimeText = if (Test-Path -LiteralPath $runtimePath) {
  Get-Content -Raw -LiteralPath $runtimePath
} else {
  ''
}
$clashIpv6Disabled = $runtimeText -match '(?m)^\s*ipv6:\s*false\s*$'

$activePhysicalIpv6 = @(
  Get-NetAdapter -Physical -ErrorAction SilentlyContinue |
    Where-Object Status -eq 'Up' |
    ForEach-Object {
      Get-NetAdapterBinding -Name $_.Name -ComponentID ms_tcpip6 -ErrorAction SilentlyContinue
    } |
    Where-Object Enabled
)
$ipv6Ready = $clashIpv6Disabled -and ($activePhysicalIpv6.Count -eq 0)

$checks = @(
  [pscustomobject]@{
    Item = '1. 干净住宅 / ISP IP'
    Status = '看网页'
    Evidence = "当前 Claude 出口 $($result.SocksPublicIp)；网页应显示支持地区、低风险、无滥用"
  }
  [pscustomobject]@{
    Item = '2. 禁用 IPv6'
    Status = if ($ipv6Ready) { '自动通过' } else { '需要复核' }
    Evidence = if ($ipv6Ready) { 'Clash ipv6=false；活动实体网卡未启用 IPv6' } else { '检查 Clash ipv6 和当前实体网卡 IPv6' }
  }
  [pscustomobject]@{
    Item = '3. DNS 走代理'
    Status = if ($result.Checks.NoDnsBoundToOtherGroups -and $result.Checks.ClaudeDnsUsesAccountUS -and $browserEntryReady) { '程序通过，确认网页' } else { '不通过' }
    Evidence = 'Claude 域名使用经 ACCOUNT-US 的 DoH；网页不能出现中国本地运营商 DNS。海外递归器因 Anycast 与美国出口不同不等于本地泄露'
  }
  [pscustomobject]@{
    Item = '4. 固定出口，不乱切'
    Status = if ($result.Checks.AccountNodeSelected -and $result.Checks.SocksHasPublicIpv4) { '自动通过' } else { '不通过' }
    Evidence = "ACCOUNT-US = $($result.AccountNode)；当前出口 = $($result.SocksPublicIp)"
  }
  [pscustomobject]@{
    Item = '5. Claude 单独分流且优先'
    Status = if ($result.Checks.AccountRuleExactlyOnce -and $result.Checks.ClaudeRulesUseAccountUS -and $browserEntryReady) { '自动通过' } else { '不通过' }
    Evidence = '43866 专用入口 + 首条 IN-NAME + Claude 规则均指向 ACCOUNT-US + 独立 User Data'
  }
)

$passedCount = @($result.Checks.PSObject.Properties | Where-Object Value).Count
$totalCount = @($result.Checks.PSObject.Properties).Count

Write-Host ''
Write-Host 'Claude 日常五项自检' -ForegroundColor Cyan
Write-Host ("程序层：{0}/{1}；当前节点：{2}；当前出口：{3}" -f $passedCount, $totalCount, $result.AccountNode, $result.SocksPublicIp)
Write-Host ''
$checks | Format-Table -AutoSize -Wrap

if ($validatorExitCode -ne 0 -or -not $result.Success -or -not $browserEntryReady) {
  Write-Host '结论：程序层存在失败项。先不要登录 Claude；按上表和 FailedChecks 修复。' -ForegroundColor Red
  if ($result.FailedChecks.Count -gt 0) {
    Write-Host ("FailedChecks: {0}" -f ($result.FailedChecks -join ', '))
  }
  if (-not $browserEntryReady) {
    Write-Host ("专用浏览器入口异常: {0}" -f $shortcutPath)
  }
  exit 1
}

Write-Host '结论：程序层已通过。浏览器打开后，只看 IP 风险、DNS、WebRTC/IPv6、语言/时区四块；全部为绿再登录。' -ForegroundColor Green
Write-Host '注意：加拿大等海外递归 DNS 可能来自节点上游或 Anycast，不等于本地泄露；出现中国本地运营商 DNS 或真实本地公网 IP 才停止。'

if (-not $NoBrowser) {
  Start-Process -FilePath $shortcutPath
  Write-Host ("已打开专用浏览器入口: {0}" -f $shortcutPath)
}
