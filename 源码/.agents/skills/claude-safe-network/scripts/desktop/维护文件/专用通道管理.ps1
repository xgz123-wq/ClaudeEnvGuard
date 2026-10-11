#requires -Version 7.0
param(
  [ValidateSet('Install', 'Verify', 'TestReject', 'Rollback')]
  [string] $Action = 'Verify'
)

$ErrorActionPreference = 'Stop'
$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$runtime = Join-Path $base 'clash-verge.yaml'
$checkFile = Join-Path $base 'clash-verge-check.yaml'
$liveScript = Join-Path $base 'profiles\Script.js'
$sourceScript = Join-Path (Split-Path (Split-Path $PSScriptRoot -Parent) -Parent) 'clash\Clash全局账号通道.js'
$core = 'C:\Program Files\Clash Verge\verge-mihomo.exe'
$policy = 'HKCU:\SOFTWARE\Policies\Claude'
$library = Join-Path $env:LOCALAPPDATA 'Claude-3p\configLibrary'
$metaPath = Join-Path $library '_meta.json'
$proxyUrl = 'http://127.0.0.1:43867'
$statePath = Join-Path $PSScriptRoot '安装状态.json'
$utf8 = [System.Text.UTF8Encoding]::new($false)

function Assert-ClaudeClosed {
  if (@(Get-Process -Name Claude -ErrorAction SilentlyContinue).Count -gt 0) {
    throw 'Claude Desktop 正在运行，操作已停止。本脚本不会启动或强制关闭 Claude。'
  }
}

function Invoke-Core([string] $Method, [string] $Path, $Payload = $null) {
  $pipe = [System.IO.Pipes.NamedPipeClientStream]::new('.', 'verge-mihomo', [System.IO.Pipes.PipeDirection]::InOut)
  try {
    $pipe.Connect(3000)
    $body = if ($null -eq $Payload) { '' } else { $Payload | ConvertTo-Json -Depth 20 -Compress }
    $header = "$Method $Path HTTP/1.1`r`nHost: localhost`r`nConnection: close`r`nContent-Type: application/json`r`nContent-Length: " + $utf8.GetByteCount($body) + "`r`n`r`n"
    $bytes = $utf8.GetBytes($header + $body)
    $pipe.Write($bytes, 0, $bytes.Length)
    $pipe.Flush()
    # HTTP chunk 长度以字节计；Latin1 暂存可避免中文节点名称导致解包越界。
    $reader = [System.IO.StreamReader]::new($pipe, [Text.Encoding]::Latin1, $false, 4096, $true)
    $read = $reader.ReadToEndAsync()
    if (-not $read.Wait(8000)) { throw 'Mihomo 本机接口读取超时。' }
    $parts = $read.Result -split "`r`n`r`n", 2
    if ($parts.Count -ne 2 -or $parts[0] -notmatch '^HTTP/1\.[01] (\d{3})') { throw '无效的本机接口响应。' }
    $status = [int]$Matches[1]
    $content = $parts[1]
    if ($parts[0] -match '(?im)^Transfer-Encoding:\s*chunked') {
      $position = 0
      $builder = [Text.StringBuilder]::new()
      while ($position -lt $content.Length) {
        $end = $content.IndexOf("`r`n", $position)
        if ($end -lt 0) { throw 'HTTP chunk 头不完整。' }
        $size = [Convert]::ToInt32($content.Substring($position, $end - $position).Split(';')[0], 16)
        if ($size -eq 0) { break }
        $position = $end + 2
        if ($position + $size -gt $content.Length) { throw 'HTTP chunk 数据不完整。' }
        [void]$builder.Append($content.Substring($position, $size))
        $position += $size + 2
      }
      $content = $builder.ToString()
    }
    $decoded = $utf8.GetString([Text.Encoding]::Latin1.GetBytes($content))
    if ($status -lt 200 -or $status -ge 300) { throw "Mihomo $Method $Path 返回 $status。" }
    if ($decoded.Trim()) { return ($decoded | ConvertFrom-Json) }
  } finally { $pipe.Dispose() }
}

function Write-JsonFile([string] $Path, $Value) {
  [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 15), $utf8)
}

function New-Preview([string] $InputPath, [string] $OutputPath, [string] $Mode, [string] $Target = 'ACCOUNT-US') {
  @'
import json, sys, yaml
src, dst, mode, target = sys.argv[1:]
with open(src, encoding='utf-8-sig') as f:
    original = yaml.safe_load(f)
import copy
c = copy.deepcopy(original)
entry = {'name':'claude-desktop-http','type':'http','listen':'127.0.0.1','port':43867,'proxy':target,'users':[]}
c['listeners'] = [entry] + [v for v in c.get('listeners',[]) if v.get('name') != entry['name']]
c['mode'] = mode
assert c.get('dns') == original.get('dns'), 'Unexpected DNS change'
assert c.get('rules') == original.get('rules'), 'Unexpected rule change'
assert c.get('proxies') == original.get('proxies'), 'Unexpected node change'
assert c.get('proxy-groups') == original.get('proxy-groups'), 'Unexpected group change'
with open(dst,'w',encoding='utf-8',newline='\n') as f:
    yaml.safe_dump(c,f,allow_unicode=True,sort_keys=False)
print(json.dumps({'configGenerated':True,'dnsUnchanged':True,'existingRoutingUnchanged':True}))
'@ | & python - $InputPath $OutputPath $Mode $Target
  if ($LASTEXITCODE -ne 0) { throw '生成候选配置失败。' }
}

function Test-CoreConfig([string] $Path) {
  $messages = @(& $core -t -d $base -f $Path 2>&1)
  if ($LASTEXITCODE -ne 0) { throw 'Mihomo 候选配置校验失败；未向界面输出可能含节点信息的完整配置。' }
}

function Reload-Core([string] $Path) {
  Assert-ClaudeClosed
  $null = Invoke-Core 'PUT' '/configs?force=true' @{path=$Path;payload=''}
}

function Restore-Selections($Snapshot) {
  $null = Invoke-Core 'PATCH' '/configs' @{mode=$Snapshot.Mode}
  foreach ($name in @('ACCOUNT-US','GLOBAL')) {
    $null = Invoke-Core 'PUT' ('/proxies/' + [uri]::EscapeDataString($name)) @{name=$Snapshot.Selections.$name}
  }
}

function Read-Snapshot {
  $cfg = Invoke-Core 'GET' '/configs'
  $selected = [ordered]@{}
  foreach ($name in @('ACCOUNT-US','GLOBAL')) { $selected[$name] = (Invoke-Core 'GET' ('/proxies/' + $name)).now }
  return [pscustomobject]@{Mode=$cfg.mode;Selections=$selected}
}

function Test-Endpoint([string] $Url) {
  $output = @(& curl.exe --silent --show-error --noproxy '' --proxy $proxyUrl --connect-timeout 8 --max-time 20 $Url 2>&1)
  return [pscustomobject]@{Url=$Url;ExitCode=$LASTEXITCODE;Body=($output -join "`n").Trim()}
}

function Test-Route {
  $client = [Net.Sockets.TcpClient]::new()
  try {
    if (-not $client.ConnectAsync('127.0.0.1', 43867).Wait(8000)) { throw '专用入口连接超时。' }
    $stream = $client.GetStream()
    $stream.ReadTimeout = 8000
    $request = $utf8.GetBytes("CONNECT example.com:443 HTTP/1.1`r`nHost: example.com:443`r`n`r`n")
    $stream.Write($request, 0, $request.Length)
    $buffer = [byte[]]::new(4096)
    $size = $stream.Read($buffer, 0, $buffer.Length)
    $response = $utf8.GetString($buffer, 0, $size)
    # Mihomo 的 CONNECT 响应可能早于真正拨号；完成 TLS 后再核对实际连接链。
    $tls = [Net.Security.SslStream]::new($stream, $true)
    if (-not $tls.AuthenticateAsClientAsync('example.com').Wait(10000)) { throw '测试站 TLS 连接超时。' }
    $connections = (Invoke-Core 'GET' '/connections').connections
    $matched = @($connections | Where-Object { $_.metadata.inboundName -eq 'claude-desktop-http' -and $_.metadata.host -eq 'example.com' })
    $chains = @($matched | ForEach-Object { $_.chains })
    return [pscustomobject]@{ConnectSucceeded=($response -match '^HTTP/1\.[01] 200');AccountGroupObserved=('ACCOUNT-US' -in $chains);ObservedChains=$chains}
  } finally { if ($tls) { $tls.Dispose() }; $client.Dispose() }
}

Assert-ClaudeClosed

if ($Action -eq 'Install') {
  if (Test-Path -LiteralPath $statePath) {
    $previous = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    if ($previous.Status -ne 'rolled_back_after_error') { throw '已有安装状态；请先 Verify，不重复安装。' }
    foreach ($entry in $previous.Files) {
      if ((Get-FileHash -LiteralPath $entry.Original).Hash -ne (Get-FileHash -LiteralPath $entry.Backup).Hash) { throw '之前的失败安装未完整恢复，停止重试。' }
    }
    Copy-Item -LiteralPath $statePath -Destination (Join-Path $PSScriptRoot ('失败安装记录-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.json'))
  }
  if (Test-Path -LiteralPath 'HKLM:\SOFTWARE\Policies\Claude') { throw '发现机器级 Claude 策略，需先评估优先级。' }
  if (Test-Path -LiteralPath $policy) { throw '发现用户级 Claude 托管策略，需先评估本地配置优先级。' }
  if (Get-NetTCPConnection -State Listen -LocalPort 43867 -ErrorAction SilentlyContinue) { throw '43867 已被占用。' }
  if ((Get-FileHash $sourceScript).Hash -ne (Get-FileHash $liveScript).Hash) { throw '工作区源文件与运行脚本不一致，停止覆盖。' }
  $oldMeta = Get-Content -LiteralPath $metaPath -Raw | ConvertFrom-Json
  if ($oldMeta.appliedId -or $oldMeta.hybridPointer -or @($oldMeta.entries).Count -gt 0) { throw '已有本地配置，需要合并评估，不能覆盖。' }
  $profileId = [guid]::NewGuid().ToString('D')
  $profilePath = Join-Path $library ($profileId + '.json')
  $snapshot = Read-Snapshot
  $backup = Join-Path $base ('account-routing-backups\' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-ClaudeDesktop专用入口')
  $null = New-Item -ItemType Directory -Path $backup
  $files = @(
    @{Original=$sourceScript;Backup=(Join-Path $backup '工作区源脚本.js');InstalledHash=$null},
    @{Original=$liveScript;Backup=(Join-Path $backup 'Script.js');InstalledHash=$null},
    @{Original=$runtime;Backup=(Join-Path $backup 'clash-verge.yaml');InstalledHash=$null},
    @{Original=$checkFile;Backup=(Join-Path $backup 'clash-verge-check.yaml');InstalledHash=$null},
    @{Original=$metaPath;Backup=(Join-Path $backup '本地配置目录元数据.json');InstalledHash=$null}
  )
  foreach ($entry in $files) { Copy-Item -LiteralPath $entry.Original -Destination $entry.Backup }
  $state = [pscustomobject]@{
    InstalledAt=(Get-Date -Format o);Status='prepared';BackupDirectory=$backup;Files=$files;
    Mode=$snapshot.Mode;Selections=$snapshot.Selections;
    ConfigurationSource='Official local configLibrary';ProfileId=$profileId;ProfilePath=$profilePath;
    ProfileInstalledHash=$null;ProxyUrl=$proxyUrl
  }
  Write-JsonFile $statePath $state
  $preview = Join-Path $backup '候选运行配置.yaml'
  $checkPreview = Join-Path $backup '候选校验配置.yaml'
  New-Preview $runtime $preview $snapshot.Mode
  New-Preview $checkFile $checkPreview $snapshot.Mode
  Test-CoreConfig $preview
  Test-CoreConfig $checkPreview
  $original = [IO.File]::ReadAllText($sourceScript)
  $needle = '  if (!Array.isArray(config.rules)) {'
  if ([regex]::Matches($original, [regex]::Escape($needle)).Count -ne 1) { throw '找不到唯一的脚本插入位置。' }
  $addition = @'
  // Claude Desktop：HTTP 专用入口直接进入账号组，不改变其他应用的 DNS/分流。
  config.listeners = config.listeners.filter(function (listener) {
    return !listener || listener.name !== "claude-desktop-http";
  });
  config.listeners.unshift({
    name: "claude-desktop-http",
    type: "http",
    listen: "127.0.0.1",
    port: 43867,
    proxy: groupName,
    users: []
  });

'@
  $newScript = $original.Replace($needle, $addition + "`n" + $needle)
  $candidateScript = Join-Path $backup '候选脚本.js'
  [IO.File]::WriteAllText($candidateScript, $newScript, $utf8)
  & node --check $candidateScript
  if ($LASTEXITCODE -ne 0) { throw '脚本语法校验失败。' }
  try {
    Assert-ClaudeClosed
    # 先写新 profile，最后设置 appliedId；不写注册表，不指定推理后端或凭据。
    Write-JsonFile $profilePath @{egressProxyUrl=$proxyUrl}
    Write-JsonFile $metaPath @{appliedId=$profileId;entries=@(@{id=$profileId;name='Claude Desktop 专用代理'})}
    Copy-Item -LiteralPath $candidateScript -Destination $sourceScript -Force
    Copy-Item -LiteralPath $candidateScript -Destination $liveScript -Force
    Copy-Item -LiteralPath $preview -Destination $runtime -Force
    Copy-Item -LiteralPath $checkPreview -Destination $checkFile -Force
    Reload-Core $runtime
    Restore-Selections $snapshot
    Assert-ClaudeClosed
    foreach ($entry in $files) { $entry.InstalledHash = (Get-FileHash -LiteralPath $entry.Original).Hash }
    $state.ProfileInstalledHash = (Get-FileHash -LiteralPath $profilePath).Hash
    $state.Status = 'installed_pending_desktop_dns'
    Write-JsonFile $statePath $state
    Write-Output '专用入口已安装；Claude Desktop 未启动。完整 DNS 防护尚未验收。'
  } catch {
    $installationError = $_
    if (@(Get-Process -Name Claude -ErrorAction SilentlyContinue).Count -gt 0) {
      $state.Status = 'stopped_because_claude_running'
      Write-JsonFile $statePath $state
      throw '检测到 Claude 被其他来源启动，已停止修改并保留当前配置，需先关闭 Claude 再核对记录。'
    }
    foreach ($entry in $files) { Copy-Item -LiteralPath $entry.Backup -Destination $entry.Original -Force }
    # 原 _meta.json 已恢复，新 profile 保留为未应用文件，不删除其他配置。
    Reload-Core $runtime
    Restore-Selections $snapshot
    $state.Status = 'rolled_back_after_error'
    Write-JsonFile $statePath $state
    throw $installationError
  }
}

if ($Action -eq 'Verify') {
  $snapshot = Read-Snapshot
  $corePid = @(Get-Process -Name verge-mihomo -ErrorAction SilentlyContinue).Id
  $listener = @(Get-NetTCPConnection -State Listen -LocalPort 43867 -ErrorAction SilentlyContinue)
  $meta = Get-Content -LiteralPath $metaPath -Raw | ConvertFrom-Json
  if ($meta.appliedId -notmatch '^[a-f0-9-]{36}$') { throw '本地配置 appliedId 无效。' }
  $profile = Get-Content -LiteralPath (Join-Path $library ($meta.appliedId + '.json')) -Raw | ConvertFrom-Json
  $configured = $profile.egressProxyUrl
  $ip = Test-Endpoint 'https://api.ipify.org'
  $trace = Test-Endpoint 'https://www.cloudflare.com/cdn-cgi/trace'
  $route = Test-Route
  $result = [pscustomobject]@{
    CheckedAt=(Get-Date -Format o);ClaudeDesktopRunning=$false;
    LocalConfigurationCorrect=($configured -eq $proxyUrl -and @($profile.PSObject.Properties).Count -eq 1 -and -not $meta.hybridPointer -and @($meta.entries | Where-Object { $_.id -eq $meta.appliedId }).Count -eq 1);
    NoManagedOverride=(-not (Test-Path -LiteralPath $policy) -and -not (Test-Path -LiteralPath 'HKLM:\SOFTWARE\Policies\Claude'));
    ListenerLoopbackOnly=($listener.Count -eq 1 -and $listener[0].LocalAddress -eq '127.0.0.1');
    CorePid=@($listener.OwningProcess);ListenerOwnedByMihomo=($listener.Count -eq 1 -and $listener[0].OwningProcess -in $corePid);
    Mode=$snapshot.Mode;Selections=$snapshot.Selections;
    IpEchoSucceeded=($ip.ExitCode -eq 0 -and $ip.Body -match '^\d{1,3}(\.\d{1,3}){3}$');
    PublicIp=$ip.Body;TraceSucceeded=($trace.ExitCode -eq 0);
    Trace=@($trace.Body -split "`n" | Where-Object {$_ -match '^(ip|loc|colo)='});
    TraceReportsUS=($trace.Body -match '(?m)^loc=US\r?$');
    Route=$route;
    DesktopDnsAcceptance='NOT_RUN';DesktopWebRtcAcceptance='NOT_RUN';OperatingSystemFailClosed='CHECK_SEPARATELY_WITH_APP_GUARD_VERIFY'
  }
  Assert-ClaudeClosed
  Write-JsonFile (Join-Path $PSScriptRoot '专用入口验收.json') $result
  $result | ConvertTo-Json -Depth 6
  if (-not ($result.LocalConfigurationCorrect -and $result.NoManagedOverride -and $result.ListenerLoopbackOnly -and $result.ListenerOwnedByMihomo -and $result.IpEchoSucceeded -and $result.TraceSucceeded -and $result.TraceReportsUS -and $route.ConnectSucceeded -and $route.AccountGroupObserved)) { throw '专用入口基础验收失败。' }
}

if ($Action -eq 'TestReject') {
  $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
  if ($state.Status -ne 'installed_pending_desktop_dns') { throw '专用入口当前并非已安装状态，不执行拒绝测试。' }
  $snapshot = Read-Snapshot
  $restoreFile = Join-Path $state.BackupDirectory ('拒绝测试前配置-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.yaml')
  Copy-Item -LiteralPath $runtime -Destination $restoreFile
  Test-CoreConfig $restoreFile
  $rejected = Join-Path $state.BackupDirectory '仅专用入口拒绝出站测试.yaml'
  New-Preview $runtime $rejected $snapshot.Mode 'REJECT'
  Test-CoreConfig $rejected
  try {
    Reload-Core $rejected
    $denied = Test-Endpoint 'https://api.ipify.org'
    $result = [pscustomobject]@{CheckedAt=(Get-Date -Format o);DedicatedEntryRejected=($denied.ExitCode -ne 0);CurlExitCode=$denied.ExitCode;ClaudeLaunched=$false;Scope='Only dedicated proxy route; not Desktop itself or OS-wide blocking'}
  } finally {
    Reload-Core $restoreFile
    Restore-Selections $snapshot
  }
  Assert-ClaudeClosed
  $restored = Test-Endpoint 'https://api.ipify.org'
  $result | Add-Member NoteProperty EntryRestored ($restored.ExitCode -eq 0)
  Write-JsonFile (Join-Path $PSScriptRoot '专用入口拒绝出站验收.json') $result
  $result | ConvertTo-Json
  if (-not ($result.DedicatedEntryRejected -and $result.EntryRestored)) { throw '拒绝出站/恢复验收未通过。' }
}

if ($Action -eq 'Rollback') {
  $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
  if ($state.Status -ne 'installed_pending_desktop_dns') { throw '当前状态不是已安装；请先检查安装记录。' }
  foreach ($entry in $state.Files) {
    if (-not $entry.InstalledHash -or (Get-FileHash -LiteralPath $entry.Original).Hash -ne $entry.InstalledHash) {
      throw '安装后配置已有其他变动，停止自动回滚，避免覆盖后续配置。'
    }
  }
  if ((Get-FileHash -LiteralPath $state.ProfilePath).Hash -ne $state.ProfileInstalledHash) {
    throw '安装后 Claude 本地代理配置已有其他变动，停止自动回滚。'
  }
  foreach ($entry in $state.Files) { Copy-Item -LiteralPath $entry.Backup -Destination $entry.Original -Force }
  Reload-Core $runtime
  Restore-Selections $state
  $state.Status = 'rolled_back'
  Write-JsonFile $statePath $state
  Assert-ClaudeClosed
  Write-Output '本次专用入口和本地配置选择已回滚；新 profile 保留为未应用文件。Claude Desktop 未启动。'
}
