#requires -Version 7.0
[CmdletBinding()]
param(
  [ValidateSet('Check','Install')][string] $Action = 'Check',
  [switch] $Json
)

$ErrorActionPreference = 'Stop'
$ruleName = 'Claude-Anthropic-Direct-Block-160.79.104.0-23'
$cidr = '160.79.104.0/23'

function Test-Rule {
  $problems = [System.Collections.Generic.List[string]]::new()
  $active = @(Get-NetFirewallRule -Name $ruleName -PolicyStore ActiveStore -ErrorAction SilentlyContinue)
  $stored = @(Get-NetFirewallRule -Name $ruleName -PolicyStore PersistentStore -ErrorAction SilentlyContinue)
  if ($active.Count -ne 1) { $problems.Add("生效规则数量为 $($active.Count)，应为 1") }
  if ($stored.Count -ne 1) { $problems.Add("持久规则数量为 $($stored.Count)，应为 1") }
  if ($active.Count -eq 1) {
    $rule = $active[0]
    $address = $rule | Get-NetFirewallAddressFilter
    $application = $rule | Get-NetFirewallApplicationFilter
    $port = $rule | Get-NetFirewallPortFilter
    if ([string]$rule.Enabled -ne 'True' -or [string]$rule.Direction -ne 'Outbound' -or
        [string]$rule.Action -ne 'Block' -or [string]$rule.Profile -ne 'Any') {
      $problems.Add('规则未在全部网络配置文件中启用出站阻断')
    }
    if (@($address.RemoteAddress).Count -ne 1 -or
        [string]$address.RemoteAddress -notin @($cidr,'160.79.104.0/255.255.254.0') -or
        [string]$address.LocalAddress -ne 'Any') {
      $problems.Add('目标网段或本地地址范围不符')
    }
    if ([string]$application.Program -ne 'Any' -or [string]$port.Protocol -ne 'Any' -or
        [string]$port.LocalPort -ne 'Any' -or [string]$port.RemotePort -ne 'Any') {
      $problems.Add('规则没有覆盖所有程序和端口')
    }
  }
  [pscustomobject]@{
    Succeeded = ($problems.Count -eq 0)
    RuleName = $ruleName
    RemoteAddress = $cidr
    Evidence = if ($problems.Count) { $problems -join '；' } else { '持久生效；全部程序、端口和网络配置文件；出站阻断 160.79.104.0/23' }
  }
}

try {
  if ($Action -eq 'Install') {
    $current = Test-Rule
    if (-not $current.Succeeded) {
      $existing = @(Get-NetFirewallRule -Name $ruleName -PolicyStore PersistentStore -ErrorAction SilentlyContinue)
      if ($existing.Count) { throw "同名规则已存在但配置不符：$($current.Evidence)。请先人工核对，脚本不会覆盖。" }
      $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
      $principal = [Security.Principal.WindowsPrincipal]::new($identity)
      if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw '安装需要以管理员身份运行 PowerShell 7。'
      }
      New-NetFirewallRule -Name $ruleName -DisplayName 'Claude/Anthropic 直连阻断 160.79.104.0/23' -Description 'Block direct outbound connections to Anthropic published API/Console IPv4 range; local Clash proxy remains available.' -Direction Outbound -Action Block -RemoteAddress $cidr -Protocol Any -Profile Any -Enabled True | Out-Null
    }
  }
  $result = Test-Rule
} catch {
  $result = [pscustomobject]@{Succeeded=$false;RuleName=$ruleName;RemoteAddress=$cidr;Evidence=$_.Exception.Message}
}

if ($Json) { $result | ConvertTo-Json -Compress }
elseif ($result.Succeeded) { Write-Host "[PASS] $($result.Evidence)" -ForegroundColor Green }
else { Write-Host "[FAIL] $($result.Evidence)" -ForegroundColor Red }
if (-not $result.Succeeded) { exit 1 }
