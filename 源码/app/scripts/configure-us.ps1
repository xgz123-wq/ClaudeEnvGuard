# Claude 环境卫士 · 一键配置美国环境
# 需要管理员权限运行。所有改动写日志到 %LOCALAPPDATA%\ClaudeEnvGuard\logs。
[CmdletBinding()]
param(
  [string]$Region = 'us_west',
  [int]$ProxyPort = 10808,
  [switch]$DisableIPv6
)

$ErrorActionPreference = 'Continue'
$logDir = Join-Path $env:LOCALAPPDATA 'ClaudeEnvGuard\logs'
$null = New-Item -ItemType Directory -Path $logDir -Force
$logFile = Join-Path $logDir ('configure-us-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.log')

function Log([string]$msg) {
  $line = '[{0}] {1}' -f (Get-Date -Format 'HH:mm:ss'), $msg
  Add-Content -LiteralPath $logFile -Value $line -Encoding utf8
  Write-Host $line
}

Log '===== 一键配置美国环境开始 ====='

# 1. 时区 ------------------------------------------------------------------
$tzMap = @{
  'us_west'    = 'Pacific Standard Time'
  'us_east'    = 'Eastern Standard Time'
  'us_central' = 'Central Standard Time'
}
$tz = $tzMap[$Region]
if (-not $tz) { $tz = 'Pacific Standard Time' }
try {
  tzutil /s $tz
  Log "时区已设置为 $tz"
} catch { Log "时区设置失败：$($_.Exception.Message)" }

# 2. 系统语言 ----------------------------------------------------------------
try {
  Set-WinUserLanguageList -LanguageList (New-WinUserLanguageList -Language en-US) -Force
  Set-Culture -CultureInfo en-US
  Set-WinUILanguageOverride -Language en-US
  Log '系统语言已设置为 en-US'
} catch { Log "语言设置失败：$($_.Exception.Message)" }

# 3. 系统代理 -----------------------------------------------------------------
$proxy = "127.0.0.1:$ProxyPort"
try {
  $key = 'HKCU:Software\Microsoft\Windows\CurrentVersion\Internet Settings'
  Set-ItemProperty -Path $key -Name ProxyEnable -Value 1
  Set-ItemProperty -Path $key -Name ProxyServer -Value $proxy
  Set-ItemProperty -Path $key -Name ProxyOverride -Value '<local>'
  $sig = @'
using System;
using System.Runtime.InteropServices;
public class WinINet {
  [DllImport("wininet.dll", SetLastError = true)]
  public static extern bool InternetSetOption(IntPtr h, int opt, IntPtr buf, int size);
}
'@
  Add-Type -TypeDefinition $sig
  [WinINet]::InternetSetOption([IntPtr]::Zero, 39, [IntPtr]::Zero, 0) | Out-Null
  [WinINet]::InternetSetOption([IntPtr]::Zero, 37, [IntPtr]::Zero, 0) | Out-Null
  Log "系统代理已启用：$proxy"
} catch { Log "代理设置失败：$($_.Exception.Message)" }

# 4. 禁用 IPv6 ----------------------------------------------------------------
if ($DisableIPv6) {
  try {
    Get-NetAdapterBinding -ComponentID ms_tcpip6 -ErrorAction Stop |
      Where-Object Enabled | ForEach-Object {
        Disable-NetAdapterBinding -Name $_.Name -ComponentID ms_tcpip6 -Confirm:$false
        Log "已禁用 IPv6：$($_.Name)"
      }
  } catch { Log "IPv6 禁用失败：$($_.Exception.Message)" }
} else {
  Log '按设置跳过 IPv6 禁用'
}

# 5. WebRTC 防泄露 ------------------------------------------------------------
$policies = @(
  'HKLM:SOFTWARE\Policies\Google\Chrome',
  'HKLM:SOFTWARE\Policies\Microsoft\Edge'
)
foreach ($p in $policies) {
  try {
    $null = New-Item -Path $p -Force
    Set-ItemProperty -Path $p -Name 'WebRtcIPHandling' -Value 'disable_non_proxied_udp'
    Log "已写入 WebRTC 策略：$p"
  } catch { Log "WebRTC 策略写入失败 $p：$($_.Exception.Message)" }
}

# 6. 清理 ANTHROPIC_BASE_URL（官方直连应保持未设置） ----------------------------
foreach ($scope in 'User', 'Machine') {
  try {
    $v = [Environment]::GetEnvironmentVariable('ANTHROPIC_BASE_URL', $scope)
    if ($v) {
      [Environment]::SetEnvironmentVariable('ANTHROPIC_BASE_URL', $null, $scope)
      Log "已清除 $scope 级 ANTHROPIC_BASE_URL（原值：$v）"
    }
  } catch { Log "清理 ANTHROPIC_BASE_URL($scope) 失败：$($_.Exception.Message)" }
}

# 7. 刷新 DNS -----------------------------------------------------------------
try {
  Clear-DnsClientCache
  ipconfig /flushdns | Out-Null
  Log 'DNS 缓存已刷新'
} catch { Log "DNS 刷新失败：$($_.Exception.Message)" }

Log '===== 一键配置美国环境完成 ====='
