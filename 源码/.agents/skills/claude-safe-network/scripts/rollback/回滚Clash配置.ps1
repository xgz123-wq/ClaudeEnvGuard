#requires -Version 7.0
param(
  [Parameter(Mandatory)]
  [string] $BackupDirectory
)

$ErrorActionPreference = 'Stop'
$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$backupRoot = Join-Path $base 'account-routing-backups'
$core = 'C:\Program Files\Clash Verge\verge-mihomo.exe'
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)

function ConvertFrom-ChunkedBody {
  param([string] $Body)
  $position = 0
  $builder = [System.Text.StringBuilder]::new()
  while ($position -lt $Body.Length) {
    $lineEnd = $Body.IndexOf("`r`n", $position)
    if ($lineEnd -lt 0) { break }
    $size = [Convert]::ToInt32($Body.Substring($position, $lineEnd - $position).Split(';')[0], 16)
    if ($size -eq 0) { break }
    $position = $lineEnd + 2
    [void] $builder.Append($Body.Substring($position, $size))
    $position += $size + 2
  }
  $builder.ToString()
}

function Invoke-MihomoReload {
  param([string] $ConfigPath)
  $pipe = [System.IO.Pipes.NamedPipeClientStream]::new('.', 'verge-mihomo', [System.IO.Pipes.PipeDirection]::InOut)
  try {
    $pipe.Connect(3000)
    $body = @{ path = $ConfigPath; payload = '' } | ConvertTo-Json -Compress
    $bodyBytes = $utf8NoBom.GetBytes($body)
    $request = "PUT /configs?force=true HTTP/1.1`r`nHost: localhost`r`nConnection: close`r`nContent-Type: application/json`r`nContent-Length: $($bodyBytes.Length)`r`n`r`n$body"
    $bytes = $utf8NoBom.GetBytes($request)
    $pipe.Write($bytes, 0, $bytes.Length)
    $pipe.Flush()
    $reader = [System.IO.StreamReader]::new($pipe, [System.Text.Encoding]::UTF8, $false, 4096, $true)
    $response = $reader.ReadToEnd()
    $status = ($response -split "`r?`n")[0]
    if ($status -notmatch '^HTTP/1\.1 204') { throw "Mihomo 重载失败: $status" }
  } finally { $pipe.Dispose() }
}

$resolvedRoot = [System.IO.Path]::GetFullPath($backupRoot).TrimEnd('\') + '\'
$resolvedBackup = [System.IO.Path]::GetFullPath($BackupDirectory).TrimEnd('\') + '\'
if (-not $resolvedBackup.StartsWith($resolvedRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
  throw "备份目录必须位于: $backupRoot"
}
if (-not (Test-Path -LiteralPath $BackupDirectory -PathType Container)) {
  throw "备份目录不存在: $BackupDirectory"
}

$targetMap = [ordered]@{
  'dns_config.yaml' = (Join-Path $base 'dns_config.yaml')
  'clash-verge.yaml' = (Join-Path $base 'clash-verge.yaml')
  'clash-verge-check.yaml' = (Join-Path $base 'clash-verge-check.yaml')
  'Script.js' = (Join-Path $base 'profiles\Script.js')
}
$restoreNames = @($targetMap.Keys | Where-Object { Test-Path -LiteralPath (Join-Path $BackupDirectory $_) })
if ($restoreNames.Count -eq 0) { throw '指定目录中没有可识别的 Clash 备份文件。' }

$backupRuntime = Join-Path $BackupDirectory 'clash-verge.yaml'
if (Test-Path -LiteralPath $backupRuntime) {
  $test = @(& $core -t -d $base -f $backupRuntime 2>&1)
  if ($LASTEXITCODE -ne 0) { throw ('备份配置校验失败，停止回滚: ' + ($test -join ' | ')) }
}

$safetyDir = Join-Path $backupRoot ((Get-Date -Format 'yyyyMMdd-HHmmss') + '-回滚前保护')
New-Item -ItemType Directory -Path $safetyDir -Force | Out-Null
foreach ($name in $restoreNames) {
  $target = $targetMap[$name]
  if (Test-Path -LiteralPath $target) { Copy-Item -LiteralPath $target -Destination (Join-Path $safetyDir $name) }
}

foreach ($name in $restoreNames) {
  Copy-Item -LiteralPath (Join-Path $BackupDirectory $name) -Destination $targetMap[$name] -Force
}
Invoke-MihomoReload -ConfigPath (Join-Path $base 'clash-verge.yaml')

[pscustomobject]@{
  Success = $true
  RestoredFrom = $BackupDirectory
  RestoredFiles = $restoreNames
  SafetyBackup = $safetyDir
  Notice = '回滚后请重新运行验证脚本；若恢复了全局 Script.js，重启 Clash Verge 后才算完成持久态验证。'
} | ConvertTo-Json -Depth 5
