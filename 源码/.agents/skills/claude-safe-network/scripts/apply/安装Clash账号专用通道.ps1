#requires -Version 7.0
$ErrorActionPreference = 'Stop'

$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$globalScript = Join-Path $base 'profiles\Script.js'
$runtimeConfig = Join-Path $base 'clash-verge.yaml'
$checkConfig = Join-Path $base 'clash-verge-check.yaml'
$vergeConfig = Join-Path $base 'verge.yaml'
# 本脚本在 scripts\apply\，全局扩展脚本在 scripts\clash\（搬迁后同级拆分，不能再用 $PSScriptRoot 直取）
$sourceScript = Join-Path (Split-Path $PSScriptRoot -Parent) 'clash\Clash全局账号通道.js'
$core = 'C:\Program Files\Clash Verge\verge-mihomo.exe'
$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backupDir = Join-Path $base (Join-Path 'account-routing-backups' ($timestamp + '-阶段2-账号通道'))
$previewConfig = Join-Path $backupDir 'clash-verge-preview.yaml'
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$groupName = 'ACCOUNT-US'
$listenerName = 'account-us-socks'
$listenerPort = 43866
$inboundRule = "IN-NAME,$listenerName,$groupName"

function ConvertFrom-ChunkedBody {
  param([Parameter(Mandatory)] [string] $Body)

  $position = 0
  $builder = [System.Text.StringBuilder]::new()
  while ($position -lt $Body.Length) {
    $lineEnd = $Body.IndexOf("`r`n", $position)
    if ($lineEnd -lt 0) { break }
    $sizeText = $Body.Substring($position, $lineEnd - $position).Split(';')[0]
    $size = [Convert]::ToInt32($sizeText, 16)
    if ($size -eq 0) { break }
    $position = $lineEnd + 2
    [void] $builder.Append($Body.Substring($position, $size))
    $position += $size + 2
  }
  return $builder.ToString()
}

function Invoke-MihomoPipe {
  param(
    [Parameter(Mandatory)] [string] $Method,
    [Parameter(Mandatory)] [string] $Path,
    [string] $Body = ''
  )

  $pipe = [System.IO.Pipes.NamedPipeClientStream]::new('.', 'verge-mihomo', [System.IO.Pipes.PipeDirection]::InOut)
  try {
    $pipe.Connect(3000)
    $bodyBytes = $utf8NoBom.GetBytes($Body)
    $request = $Method + ' ' + $Path + " HTTP/1.1`r`nHost: localhost`r`nConnection: close`r`nContent-Type: application/json`r`nContent-Length: " + $bodyBytes.Length + "`r`n`r`n" + $Body
    $requestBytes = $utf8NoBom.GetBytes($request)
    $pipe.Write($requestBytes, 0, $requestBytes.Length)
    $pipe.Flush()

    $reader = [System.IO.StreamReader]::new($pipe, [System.Text.Encoding]::UTF8, $false, 4096, $true)
    $response = $reader.ReadToEnd()
    $parts = $response -split "`r?`n`r?`n", 2
    $status = ($parts[0] -split "`r?`n")[0]
    $responseBody = if ($parts.Count -gt 1) { $parts[1] } else { '' }
    if ($parts[0] -match '(?im)^Transfer-Encoding:\s*chunked\s*$') {
      $responseBody = ConvertFrom-ChunkedBody -Body $responseBody
    }
    return [pscustomobject]@{ Status = $status; Body = $responseBody }
  } finally {
    $pipe.Dispose()
  }
}

function Add-AccountChannelYaml {
  param([Parameter(Mandatory)] [string] $Path)

  $lines = [System.Collections.Generic.List[string]]::new()
  [System.IO.File]::ReadAllLines($Path) | ForEach-Object { [void] $lines.Add($_) }

  if ($lines -contains "- name: $groupName" -or
      $lines -contains "- name: $listenerName" -or
      $lines -contains "- $inboundRule") {
    throw "临时或运行配置已经包含账号通道，停止以避免覆盖: $Path"
  }

  $groupIndex = $lines.IndexOf('proxy-groups:')
  $rulesIndex = $lines.IndexOf('rules:')
  if ($groupIndex -lt 0 -or $rulesIndex -lt 0) {
    throw "有效配置缺少 proxy-groups 或 rules: $Path"
  }

  $groupBlock = [System.Collections.Generic.List[string]]::new()
  @(
    "- name: $groupName",
    '  type: select',
    '  include-all-proxies: true',
    '  exclude-type: Direct|Reject'
  ) | ForEach-Object { [void] $groupBlock.Add($_) }
  $lines.InsertRange($groupIndex + 1, $groupBlock)

  $rulesIndex = $lines.IndexOf('rules:')
  $listenerBlock = [System.Collections.Generic.List[string]]::new()
  @(
    'listeners:',
    "- name: $listenerName",
    '  type: socks',
    '  listen: 127.0.0.1',
    "  port: $listenerPort",
    '  udp: false'
  ) | ForEach-Object { [void] $listenerBlock.Add($_) }
  $lines.InsertRange($rulesIndex, $listenerBlock)

  $rulesIndex = $lines.IndexOf('rules:')
  $lines.Insert($rulesIndex + 1, "- $inboundRule")
  [System.IO.File]::WriteAllLines($Path, $lines, $utf8NoBom)
}

$required = @($globalScript, $runtimeConfig, $checkConfig, $vergeConfig, $sourceScript, $core)
foreach ($path in $required) {
  if (-not (Test-Path -LiteralPath $path)) {
    throw "缺少必需文件: $path"
  }
}

$vergeText = [System.IO.File]::ReadAllText($vergeConfig)
if ($vergeText -notmatch '(?m)^enable_builtin_enhanced:\s*true\s*$') {
  throw 'Clash Verge 全局扩展未启用，停止安装。'
}

$existingListener = Get-NetTCPConnection -State Listen -LocalPort $listenerPort -ErrorAction SilentlyContinue
if ($existingListener) {
  throw "端口 $listenerPort 已被 PID $($existingListener.OwningProcess -join ',') 占用。"
}

$proxiesResponse = Invoke-MihomoPipe -Method 'GET' -Path '/proxies'
if ($proxiesResponse.Status -notmatch '^HTTP/1\.1 200') {
  throw '无法读取 Mihomo 代理列表，停止安装。'
}
$allProxies = ($proxiesResponse.Body | ConvertFrom-Json).proxies
$proxyNames = @($allProxies.PSObject.Properties.Name)
# 初始节点：优先用 ACCOUNT-US 已选值，否则退回任意已选策略组（不依赖订阅自带的组名）。
$selectedNode = $null
if ($proxyNames -contains 'ACCOUNT-US' -and -not [string]::IsNullOrWhiteSpace($allProxies.'ACCOUNT-US'.now)) {
  $selectedNode = $allProxies.'ACCOUNT-US'.now
}
if ([string]::IsNullOrWhiteSpace($selectedNode)) {
  $selectedNode = @($allProxies.PSObject.Properties |
    Where-Object { $_.Value.now -and $_.Value.type -in @('Selector', 'URLTest', 'Fallback', 'LoadBalance') } |
    ForEach-Object { $_.Value.now })[0]
}
if ([string]::IsNullOrWhiteSpace($selectedNode)) {
  throw '运行配置里没有任何已选节点，请先在 Clash Verge 里选一个节点再安装。'
}

New-Item -ItemType Directory -Path $backupDir -Force | Out-Null
Copy-Item -LiteralPath $runtimeConfig -Destination $previewConfig
Add-AccountChannelYaml -Path $previewConfig

$previewOutput = @(& $core -t -d $base -f $previewConfig 2>&1)
if ($LASTEXITCODE -ne 0) {
  throw ('临时账号通道配置校验失败，未修改运行配置: ' + ($previewOutput -join ' | '))
}

$filesToBackup = @($globalScript, $runtimeConfig, $checkConfig)
foreach ($path in $filesToBackup) {
  Copy-Item -LiteralPath $path -Destination (Join-Path $backupDir (Split-Path -Leaf $path))
}

$manifest = [pscustomobject]@{
  CreatedAt = (Get-Date).ToString('o')
  BackupDirectory = $backupDir
  SelectedNodeBeforeInstall = $selectedNode
  Files = $filesToBackup
}
[System.IO.File]::WriteAllText((Join-Path $backupDir 'manifest.json'), ($manifest | ConvertTo-Json -Depth 5), $utf8NoBom)

$reloaded = $false
try {
  Copy-Item -LiteralPath $sourceScript -Destination $globalScript -Force
  Add-AccountChannelYaml -Path $runtimeConfig
  Add-AccountChannelYaml -Path $checkConfig

  $validationOutput = @(& $core -t -d $base -f $runtimeConfig 2>&1)
  if ($LASTEXITCODE -ne 0) {
    throw ('安装后的有效配置校验失败: ' + ($validationOutput -join ' | '))
  }

  $reloadBody = @{ path = $runtimeConfig; payload = '' } | ConvertTo-Json -Compress
  $reload = Invoke-MihomoPipe -Method 'PUT' -Path '/configs?force=true' -Body $reloadBody
  if ($reload.Status -notmatch '^HTTP/1\.1 204') {
    throw ('Mihomo 热加载失败: ' + $reload.Status + ' ' + $reload.Body)
  }
  $reloaded = $true

  $selectionBody = @{ name = $selectedNode } | ConvertTo-Json -Compress
  $selection = Invoke-MihomoPipe -Method 'PUT' -Path "/proxies/$groupName" -Body $selectionBody
  if ($selection.Status -notmatch '^HTTP/1\.1 204') {
    throw ('ACCOUNT-US 节点选择失败: ' + $selection.Status + ' ' + $selection.Body)
  }

  Start-Sleep -Milliseconds 500
  $listener = Get-NetTCPConnection -State Listen -LocalPort $listenerPort -ErrorAction SilentlyContinue
  if (-not $listener) {
    throw "专用端口 $listenerPort 未监听。"
  }

  $accountResponse = Invoke-MihomoPipe -Method 'GET' -Path "/proxies/$groupName"
  if ($accountResponse.Status -notmatch '^HTTP/1\.1 200') {
    throw '无法读取 ACCOUNT-US 运行状态。'
  }
  $account = $accountResponse.Body | ConvertFrom-Json
  if ($account.now -ne $selectedNode) {
    throw "ACCOUNT-US 选择不符合预期: $($account.now)"
  }

  [pscustomobject]@{
    Success = $true
    BackupDirectory = $backupDir
    ListenerName = $listenerName
    ListenerPort = $listenerPort
    ListenerPid = @($listener.OwningProcess | Select-Object -Unique)
    Group = $groupName
    SelectedNode = $account.now
    Validation = ($validationOutput -join ' | ')
  } | ConvertTo-Json -Depth 5
} catch {
  foreach ($path in $filesToBackup) {
    $backup = Join-Path $backupDir (Split-Path -Leaf $path)
    if (Test-Path -LiteralPath $backup) {
      Copy-Item -LiteralPath $backup -Destination $path -Force
    }
  }

  if ($reloaded) {
    try {
      $restoreBody = @{ path = $runtimeConfig; payload = '' } | ConvertTo-Json -Compress
      [void] (Invoke-MihomoPipe -Method 'PUT' -Path '/configs?force=true' -Body $restoreBody)
    } catch {
      Write-Warning '文件已恢复，但运行态恢复失败，请重启 Clash Verge。'
    }
  }
  throw
}
