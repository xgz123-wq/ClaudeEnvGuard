#requires -Version 7.0
$ErrorActionPreference = 'Stop'

$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$dnsConfig = Join-Path $base 'dns_config.yaml'
$runtimeConfig = Join-Path $base 'clash-verge.yaml'
$checkConfig = Join-Path $base 'clash-verge-check.yaml'
$core = 'C:\Program Files\Clash Verge\verge-mihomo.exe'
$timestamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$backupDir = Join-Path $base (Join-Path 'account-routing-backups' ($timestamp + '-阶段1-DNS'))
$previewConfig = Join-Path $backupDir 'clash-verge-preview.yaml'
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$desiredNameservers = @(
  '8.8.8.8',
  'https://doh.pub/dns-query',
  'https://dns.alidns.com/dns-query'
)

function Set-YamlNameserverList {
  param(
    [Parameter(Mandatory)] [string] $Path,
    [Parameter(Mandatory)] [string[]] $Nameservers
  )

  $lines = [System.Collections.Generic.List[string]]::new()
  [System.IO.File]::ReadAllLines($Path) | ForEach-Object { [void] $lines.Add($_) }

  $start = -1
  for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -eq '  nameserver:') {
      $start = $i
      break
    }
  }
  if ($start -lt 0) {
    throw "找不到 dns.nameserver: $Path"
  }

  $end = $start + 1
  while ($end -lt $lines.Count -and $lines[$end] -match '^  -\s+') {
    $end++
  }

  $replacement = [System.Collections.Generic.List[string]]::new()
  [void] $replacement.Add('  nameserver:')
  foreach ($server in $Nameservers) {
    [void] $replacement.Add('  - ' + $server)
  }

  $lines.RemoveRange($start, $end - $start)
  $lines.InsertRange($start, $replacement)
  [System.IO.File]::WriteAllLines($Path, $lines, $utf8NoBom)
}

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

$targets = @($dnsConfig, $runtimeConfig, $checkConfig, $core)
foreach ($path in $targets) {
  if (-not (Test-Path -LiteralPath $path)) {
    throw "缺少必需文件: $path"
  }
}

New-Item -ItemType Directory -Path $backupDir -Force | Out-Null
Copy-Item -LiteralPath $runtimeConfig -Destination $previewConfig
Set-YamlNameserverList -Path $previewConfig -Nameservers $desiredNameservers

$previewOutput = @(& $core -t -d $base -f $previewConfig 2>&1)
if ($LASTEXITCODE -ne 0) {
  throw ('临时配置校验失败，未修改运行配置: ' + ($previewOutput -join ' | '))
}

$filesToBackup = @($dnsConfig, $runtimeConfig, $checkConfig)
foreach ($path in $filesToBackup) {
  Copy-Item -LiteralPath $path -Destination (Join-Path $backupDir (Split-Path -Leaf $path))
}

$manifest = [pscustomobject]@{
  CreatedAt = (Get-Date).ToString('o')
  BackupDirectory = $backupDir
  NameserversBeforeRepair = @(
    [regex]::Matches([System.IO.File]::ReadAllText($dnsConfig), '(?m)^  - (.+)$') |
      ForEach-Object { $_.Groups[1].Value }
  )
  Files = $filesToBackup
}
[System.IO.File]::WriteAllText((Join-Path $backupDir 'manifest.json'), ($manifest | ConvertTo-Json -Depth 5), $utf8NoBom)

$reloaded = $false
try {
  foreach ($path in $filesToBackup) {
    Set-YamlNameserverList -Path $path -Nameservers $desiredNameservers
  }

  $validationOutput = @(& $core -t -d $base -f $runtimeConfig 2>&1)
  if ($LASTEXITCODE -ne 0) {
    throw ('修改后的配置校验失败: ' + ($validationOutput -join ' | '))
  }

  $reloadBody = @{ path = $runtimeConfig; payload = '' } | ConvertTo-Json -Compress
  $reload = Invoke-MihomoPipe -Method 'PUT' -Path '/configs?force=true' -Body $reloadBody
  if ($reload.Status -notmatch '^HTTP/1\.1 204') {
    throw ('Mihomo 热加载失败: ' + $reload.Status + ' ' + $reload.Body)
  }
  $reloaded = $true

  $configResponse = Invoke-MihomoPipe -Method 'GET' -Path '/configs'
  if ($configResponse.Status -notmatch '^HTTP/1\.1 200') {
    throw ('运行状态读取失败: ' + $configResponse.Status)
  }
  $live = $configResponse.Body | ConvertFrom-Json
  if ($live.mode -ne 'rule' -or -not $live.tun.enable -or $live.'mixed-port' -ne 43865) {
    throw "运行状态不符合预期: mode=$($live.mode), tun=$($live.tun.enable), mixed-port=$($live.'mixed-port')"
  }

  [pscustomobject]@{
    Success = $true
    BackupDirectory = $backupDir
    Mode = $live.mode
    TunEnabled = $live.tun.enable
    MixedPort = $live.'mixed-port'
    Nameservers = $desiredNameservers
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
      Write-Warning '文件已恢复，但运行态恢复失败，请在 Clash Verge 中重新激活当前订阅。'
    }
  }
  throw
}
