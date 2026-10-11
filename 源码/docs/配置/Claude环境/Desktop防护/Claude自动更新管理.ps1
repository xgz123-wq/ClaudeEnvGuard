#requires -Version 7.0
[CmdletBinding()]
param(
  [ValidateSet('Status','Disable','Enable')]
  [string] $Action = 'Status',
  [ValidateSet('Machine','User')]
  [string] $Scope = 'Machine'
)

$ErrorActionPreference = 'Stop'
$userPolicyPath = 'HKCU:\SOFTWARE\Policies\Claude'
$machinePolicyPath = 'HKLM:\SOFTWARE\Policies\Claude'
$valueName = 'disableAutoUpdates'
$proxyValueName = 'egressProxyUrl'
$expectedProxy = 'http://127.0.0.1:43867'
$targetPolicyPath = if ($Scope -eq 'Machine') { $machinePolicyPath } else { $userPolicyPath }

$isAdministrator = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
  [Security.Principal.WindowsBuiltInRole]::Administrator
)
if ($Action -ne 'Status' -and -not $isAdministrator) {
  $engine = (Get-Process -Id $PID).Path
  $argumentLine = '-NoProfile -ExecutionPolicy Bypass -File "' + $PSCommandPath + '" -Action ' + $Action + ' -Scope ' + $Scope
  $process = Start-Process -FilePath $engine -Verb RunAs -ArgumentList $argumentLine -Wait -PassThru -WindowStyle Hidden
  if ($process.ExitCode -ne 0) { throw "管理员操作失败，退出码：$($process.ExitCode)" }
  & $PSCommandPath -Action Status
  exit
}

if ($Action -eq 'Disable') {
  $null = New-Item -Path $targetPolicyPath -Force
  $null = New-ItemProperty -Path $targetPolicyPath -Name $valueName -Value 1 -PropertyType DWord -Force
  $null = New-ItemProperty -Path $targetPolicyPath -Name $proxyValueName -Value $expectedProxy -PropertyType String -Force
}

if ($Action -eq 'Enable' -and (Test-Path -Path $targetPolicyPath)) {
  Remove-ItemProperty -Path $targetPolicyPath -Name $valueName -ErrorAction SilentlyContinue
  Remove-ItemProperty -Path $targetPolicyPath -Name $proxyValueName -ErrorAction SilentlyContinue
}

$machineValue = (Get-ItemProperty -Path $machinePolicyPath -Name $valueName -ErrorAction SilentlyContinue).$valueName
$userValue = (Get-ItemProperty -Path $userPolicyPath -Name $valueName -ErrorAction SilentlyContinue).$valueName
$machineProxy = (Get-ItemProperty -Path $machinePolicyPath -Name $proxyValueName -ErrorAction SilentlyContinue).$proxyValueName
$userProxy = (Get-ItemProperty -Path $userPolicyPath -Name $proxyValueName -ErrorAction SilentlyContinue).$proxyValueName
$effectiveSource = if ($null -ne $machineValue) { 'HKLM' } elseif ($null -ne $userValue) { 'HKCU' } else { 'Default' }
$effectiveValue = if ($effectiveSource -eq 'HKLM') { $machineValue } elseif ($effectiveSource -eq 'HKCU') { $userValue } else { 0 }
$effectiveProxy = if ($effectiveSource -eq 'HKLM') { $machineProxy } elseif ($effectiveSource -eq 'HKCU') { $userProxy } else { $null }

[pscustomobject]@{
  Action = $Action
  RequestedScope = $Scope
  AutoUpdatesDisabled = ([int]$effectiveValue -eq 1)
  EffectiveSource = $effectiveSource
  MachinePolicy = $machineValue
  UserPolicy = $userValue
  EffectiveProxy = $effectiveProxy
  ProxyMatches = ($effectiveProxy -eq $expectedProxy)
  AppliesAt = 'Claude Desktop next start'
}
