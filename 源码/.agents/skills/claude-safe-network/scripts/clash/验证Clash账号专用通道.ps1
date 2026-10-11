#requires -Version 7.0
$ErrorActionPreference = 'Stop'

$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$runtime = Join-Path $base 'clash-verge.yaml'
$core = 'C:\Program Files\Clash Verge\verge-mihomo.exe'
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
$requiredClaudeRules = @(
  'DOMAIN-SUFFIX,anthropic.com,ACCOUNT-US',
  'DOMAIN-SUFFIX,claude.ai,ACCOUNT-US',
  'DOMAIN-SUFFIX,claude.com,ACCOUNT-US',
  'DOMAIN-SUFFIX,claude.app,ACCOUNT-US',
  'DOMAIN-SUFFIX,clau.de,ACCOUNT-US',
  'DOMAIN-SUFFIX,claudemcpclient.com,ACCOUNT-US',
  'DOMAIN-SUFFIX,claudemcpcontent.com,ACCOUNT-US',
  'DOMAIN-SUFFIX,claudeusercontent.com,ACCOUNT-US',
  'DOMAIN,servd-anthropic-website.b-cdn.net,ACCOUNT-US',
  'DOMAIN,anthropic.com.cdn.cloudflare.net,ACCOUNT-US',
  'DOMAIN,anthropic.auth0.com,ACCOUNT-US',
  'DOMAIN,anthropic-com.ghost.io,ACCOUNT-US',
  'IP-CIDR,160.79.104.0/21,ACCOUNT-US,no-resolve',
  'IP-CIDR6,2607:6bc0::/32,ACCOUNT-US,no-resolve',
  'IP-ASN,399358,ACCOUNT-US,no-resolve'
)
$requiredClaudeDnsDomains = @(
  '+.anthropic.com',
  '+.claude.ai',
  '+.claude.com',
  '+.claude.app',
  '+.clau.de',
  '+.claudemcpclient.com',
  '+.claudemcpcontent.com',
  '+.claudeusercontent.com'
)
$forbiddenBroadTelemetryRules = @(
  'DOMAIN-SUFFIX,sentry.io,ACCOUNT-US',
  'DOMAIN-SUFFIX,statsigapi.net,ACCOUNT-US',
  'DOMAIN,browser-intake-us5-datadoghq.com,ACCOUNT-US',
  'DOMAIN-KEYWORD,datadog,ACCOUNT-US',
  'DOMAIN-KEYWORD,sift,ACCOUNT-US',
  'DOMAIN-SUFFIX,intercom.io,ACCOUNT-US',
  'DOMAIN-SUFFIX,intercomcdn.com,ACCOUNT-US',
  'DOMAIN,cdn.usefathom.com,ACCOUNT-US'
)
$forbiddenBroadTelemetryDnsDomains = @(
  '+.sentry.io',
  '+.statsigapi.net',
  '+.datadoghq.com',
  '+.intercom.io',
  '+.intercomcdn.com'
)

function ConvertFrom-ChunkedBody {
  param([Parameter(Mandatory)] [string] $Body)
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

function Invoke-MihomoPipe {
  param([string] $Method, [string] $Path)
  $pipe = [System.IO.Pipes.NamedPipeClientStream]::new('.', 'verge-mihomo', [System.IO.Pipes.PipeDirection]::InOut)
  try {
    $pipe.Connect(3000)
    $request = "$Method $Path HTTP/1.1`r`nHost: localhost`r`nConnection: close`r`nContent-Length: 0`r`n`r`n"
    $bytes = $utf8NoBom.GetBytes($request)
    $pipe.Write($bytes, 0, $bytes.Length)
    $pipe.Flush()
    $reader = [System.IO.StreamReader]::new($pipe, [System.Text.Encoding]::UTF8, $false, 4096, $true)
    $response = $reader.ReadToEnd()
    $parts = $response -split "`r?`n`r?`n", 2
    $body = if ($parts.Count -gt 1) { $parts[1] } else { '' }
    if ($parts[0] -match '(?im)^Transfer-Encoding:\s*chunked\s*$') { $body = ConvertFrom-ChunkedBody $body }
    [pscustomobject]@{ Status = ($parts[0] -split "`r?`n")[0]; Body = $body }
  } finally { $pipe.Dispose() }
}

foreach ($path in @($runtime, $core)) {
  if (-not (Test-Path -LiteralPath $path)) { throw "缺少必需文件: $path" }
}

$text = Get-Content -LiteralPath $runtime -Raw
$listener65 = @(Get-NetTCPConnection -State Listen -LocalPort 43865 -ErrorAction SilentlyContinue)
$listener66 = @(Get-NetTCPConnection -State Listen -LocalPort 43866 -ErrorAction SilentlyContinue)
$accountResponse = Invoke-MihomoPipe GET '/proxies/ACCOUNT-US'
$configResponse = Invoke-MihomoPipe GET '/configs'
$account = if ($accountResponse.Status -match ' 200 ') { $accountResponse.Body | ConvertFrom-Json } else { $null }
$live = if ($configResponse.Status -match ' 200 ') { $configResponse.Body | ConvertFrom-Json } else { $null }
$validation = @(& $core -t -d $base -f $runtime 2>&1)
$validationExit = $LASTEXITCODE
$socksIp = curl.exe --silent --show-error --max-time 20 --socks5-hostname 127.0.0.1:43866 https://api.ipify.org
$socksExit = $LASTEXITCODE
$claudeStatus = curl.exe --silent --show-error --output NUL --write-out '%{http_code}' --max-time 20 --socks5-hostname 127.0.0.1:43866 https://claude.ai/
$claudeExit = $LASTEXITCODE
$feishuStatus = curl.exe --silent --show-error --output NUL --write-out '%{http_code}' --max-time 20 --noproxy '*' https://www.feishu.cn/
$feishuExit = $LASTEXITCODE
$proxyEnabled = (Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -Name ProxyEnable -ErrorAction SilentlyContinue).ProxyEnable
$missingClaudeRules = @($requiredClaudeRules | Where-Object { $text -notmatch ('(?m)^- ' + [regex]::Escape($_) + '$') })
$missingClaudeDnsPolicies = @($requiredClaudeDnsDomains | Where-Object {
  $text -notmatch ('(?m)^\s*' + [regex]::Escape($_) + ':\s*$')
})
$claudeDnsUsesAccountUS =
  ($missingClaudeDnsPolicies.Count -eq 0) -and
  ($text -match '(?m)^\s*- https://1\.1\.1\.1/dns-query#ACCOUNT-US\s*$') -and
  ($text -match '(?m)^\s*- https://8\.8\.8\.8/dns-query#ACCOUNT-US\s*$')
$broadTelemetryRulesAbsent = -not @($forbiddenBroadTelemetryRules | Where-Object {
  $text -match ('(?m)^- ' + [regex]::Escape($_) + '$')
}).Count
$broadTelemetryDnsPoliciesAbsent = -not @($forbiddenBroadTelemetryDnsDomains | Where-Object {
  $text -match ('(?m)^\s*' + [regex]::Escape($_) + ':\s*$')
}).Count
# 只看真正的 Claude / Anthropic 标识；遥测类域名（sentry / statsig / datadog / intercom 等）
# 按设计允许留在订阅自己的分流里，不由本项判定。
$legacyClaudeBindings = @($text -split "`r?`n" | Where-Object {
  $_ -match '^-(?: DOMAIN| IP-| DOMAIN-KEYWORD)' -and
  $_ -match '(?i)(anthropic|claude|160\.79\.104|2607:6bc0|399358)' -and
  $_ -notmatch ',ACCOUNT-US(?:,|$)'
})
# DNS 绑定后缀只允许 ACCOUNT-US；出现其它组名即视为残留的订阅组耦合。
$foreignDnsBindingGroups = @([regex]::Matches($text, '#([\w\-\.]+)&disable-ipv6') |
  ForEach-Object { $_.Groups[1].Value } | Where-Object { $_ -ne 'ACCOUNT-US' } | Select-Object -Unique)

$checks = [ordered]@{
  ModeIsRule = ($live.mode -eq 'rule')
  TunEnabled = [bool]$live.tun.enable
  SystemProxyDisabled = ($proxyEnabled -eq 0)
  MixedPort43865Listening = ($listener65.Count -gt 0)
  AccountPort43866Listening = ($listener66.Count -gt 0)
  SameMihomoPid = ($listener65.Count -gt 0 -and $listener66.Count -gt 0 -and $listener65[0].OwningProcess -eq $listener66[0].OwningProcess)
  AccountGroupExactlyOnce = (([regex]::Matches($text, '(?m)^- name: ACCOUNT-US$')).Count -eq 1)
  AccountListenerExactlyOnce = (([regex]::Matches($text, '(?m)^- name: account-us-socks$')).Count -eq 1)
  AccountRuleExactlyOnce = (([regex]::Matches($text, '(?m)^- IN-NAME,account-us-socks,ACCOUNT-US$')).Count -eq 1)
  ClaudeRulesUseAccountUS = ($missingClaudeRules.Count -eq 0)
  NoClaudeRulesBoundToOtherGroups = ($legacyClaudeBindings.Count -eq 0)
  NoDnsBoundToOtherGroups = ($foreignDnsBindingGroups.Count -eq 0)
  ClaudeDnsUsesAccountUS = $claudeDnsUsesAccountUS
  NoBroadTelemetryRulesInAccountUS = $broadTelemetryRulesAbsent
  NoBroadTelemetryDnsInAccountUS = $broadTelemetryDnsPoliciesAbsent
  AccountNodeSelected = ($null -ne $account -and -not [string]::IsNullOrWhiteSpace($account.now))
  MihomoConfigValid = ($validationExit -eq 0)
  SocksHasPublicIpv4 = ($socksExit -eq 0 -and $socksIp -match '^\d{1,3}(\.\d{1,3}){3}$')
  ClaudeReachableThroughSocks = ($claudeExit -eq 0 -and $claudeStatus -ne '000')
  FeishuReachable = ($feishuExit -eq 0 -and $feishuStatus -match '^[23]')
}
$failed = @($checks.GetEnumerator() | Where-Object { -not $_.Value } | ForEach-Object Key)
$result = [pscustomobject]@{
  Success = ($failed.Count -eq 0)
  Checks = $checks
  FailedChecks = $failed
  AccountNode = $account.now
  MissingClaudeRules = $missingClaudeRules
  MissingClaudeDnsPolicies = $missingClaudeDnsPolicies
  LegacyClaudeBindings = $legacyClaudeBindings
  SocksPublicIp = $socksIp
  ClaudeHttpStatus = $claudeStatus
  FeishuHttpStatus = $feishuStatus
  CorePid = @($listener66.OwningProcess | Select-Object -Unique)
  Validation = $validation[-1]
}
$result | ConvertTo-Json -Depth 6
if ($failed.Count -gt 0) { exit 1 }
