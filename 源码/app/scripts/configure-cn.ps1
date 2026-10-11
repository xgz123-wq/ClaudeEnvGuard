# Claude 环境卫士 · 一键配置中国环境（恢复国内直连）
# 需要管理员权限运行。所有改动写日志到 %LOCALAPPDATA%\ClaudeEnvGuard\logs。
[CmdletBinding()]
param(
  [string]$Region = 'cn',
  [int]$ProxyPort = 10808
)

$ErrorActionPreference = 'Continue'
$logDir = Join-Path $env:LOCALAPPDATA 'ClaudeEnvGuard\logs'
$null = New-Item -ItemType Directory -Path $logDir -Force
$logFile = Join-Path $logDir ('configure-cn-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.log')

function Log([string]$msg) {
  $line = '[{0}] {1}' -f (Get-Date -Format 'HH:mm:ss'), $msg
  Add-Content -LiteralPath $logFile -Value $line -Encoding utf8
  Write-Host $line
}

Log '===== 一键配置中国环境开始 ====='

# 1. 时区 ----------------------------------------------------------------------
try {
  tzutil /s 'China Standard Time'
  Log '时区已设置为 China Standard Time'
} catch { Log "时区设置失败：$($_.Exception.Message)" }

# 2. 系统语言 -------------------------------------------------------------------
try {
  Set-WinUserLanguageList -LanguageList (New-WinUserLanguageList -Language zh-CN) -Force
  Set-Culture -CultureInfo zh-CN
  Set-WinUILanguageOverride -Language zh-CN
  Log '系统语言已设置为 zh-CN'
} catch { Log "语言设置失败：$($_.Exception.Message)" }

# 3. 关闭系统代理 -----------------------------------------------------------------
try {
  $key = 'HKCU:Software\Microsoft\Windows\CurrentVersion\Internet Settings'
  Set-ItemProperty -Path $key -Name ProxyEnable -Value 0
  $sig = @'
using System;
using System.Runtime.InteropServices;
public class WinINet2 {
  [DllImport("wininet.dll", SetLastError = true)]
  public static extern bool InternetSetOption(IntPtr h, int opt, IntPtr buf, int size);
}
'@
  Add-Type -TypeDefinition $sig
  [WinINet2]::InternetSetOption([IntPtr]::Zero, 39, [IntPtr]::Zero, 0) | Out-Null
  [WinINet2]::InternetSetOption([IntPtr]::Zero, 37, [IntPtr]::Zero, 0) | Out-Null
  Log '系统代理已关闭（国内直连模式）'
} catch { Log "代理关闭失败：$($_.Exception.Message)" }

# 4. 恢复 IPv6 --------------------------------------------------------------------
try {
  Get-NetAdapterBinding -ComponentID ms_tcpip6 -ErrorAction Stop |
    Where-Object { -not $_.Enabled } | ForEach-Object {
      Enable-NetAdapterBinding -Name $_.Name -ComponentID ms_tcpip6 -Confirm:$false
      Log "已恢复 IPv6：$($_.Name)"
    }
} catch { Log "IPv6 恢复失败：$($_.Exception.Message)" }

# 5. 移除 WebRTC 策略（恢复浏览器默认） ----------------------------------------------
foreach ($p in 'HKLM:SOFTWARE\Policies\Google\Chrome', 'HKLM:SOFTWARE\Policies\Microsoft\Edge') {
  try {
    if (Test-Path $p) {
      Remove-ItemProperty -Path $p -Name 'WebRtcIPHandling' -ErrorAction Stop
      Log "已移除 WebRTC 策略：$p"
    }
  } catch { Log "WebRTC 策略移除失败 $p：$($_.Exception.Message)" }
}

# 6. 刷新 DNS -----------------------------------------------------------------------
try {
  Clear-DnsClientCache
  ipconfig /flushdns | Out-Null
  Log 'DNS 缓存已刷新'
} catch { Log "DNS 刷新失败：$($_.Exception.Message)" }

Log '===== 一键配置中国环境完成 ====='
