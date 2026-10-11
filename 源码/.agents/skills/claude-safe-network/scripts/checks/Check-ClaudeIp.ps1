#requires -Version 7.0
<#
.SYNOPSIS
    第 8 层：Claude 专向 IP / 地理位置一致性检查。

.DESCRIPTION
    A. 用 Cloudflare 官方 cdn-cgi/trace 端点，逐个入口取「真实落地 IP + 国家 + 边缘机房」——
       这比 ipify 更有意义，因为 Anthropic 全站挂在 Cloudflare 上，
       trace 返回的 loc/colo 就是 Claude 实际看到的出口身份。
    B. 三入口一致性 + 国家必须一致；不一致 = 有入口绕过了 ACCOUNT-US。
    C. 用无头 Chrome 真实渲染 ip.net.coffee/claude/，自动读 Trust Score / IP 属性 /
       VPN·Proxy·Tor·Crawler / 滥用记录 / Claude 支持地区，并核对「Claude 专向出口 IP」
       是否与 trace 取到的一致。
        注意：该页数值全是前端 JS 注入的，必须 --virtual-time-budget 等渲染完成；
              且「Claude AI出口IP」与「Cloudflare 出口IP」两个词组都含「出口IP」，
              匹配时必须带前缀，否则会误抓到非 Claude 域名的出口。
#>  
[CmdletBinding()]
param([switch] $Json, [switch] $OpenBrowser, [int] $TimeoutSec = 25)

$ErrorActionPreference = 'Continue'
$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

Reset-CheckResults
$TH = $script:Thresholds
Write-Banner '第 8 层 · Claude 专向 IP 与地理一致性' 'Cloudflare trace 逐入口 · 国家一致性 · ip.net.coffee 真实渲染'

$traceUrls = @(
    @{ Name = 'cloudflare.com'; Url = 'https://www.cloudflare.com/cdn-cgi/trace' },
    @{ Name = 'claude.ai'; Url = 'https://claude.ai/cdn-cgi/trace' }
)

function Get-Trace {
    param([string] $Entry, [string] $Url, [int] $TimeoutSec)
    $a = @('-sS', '--max-time', "$TimeoutSec")
    switch ($Entry) {
        'Code' { $a += @('--proxy', "http://127.0.0.1:$($script:Ports.Mixed)") }
        'Chrome' { $a += @('--socks5-hostname', "127.0.0.1:$($script:Ports.ChromeSocks)") }
        'Desktop' { $a += @('--proxy', "http://127.0.0.1:$($script:Ports.DesktopHttp)") }
    }
    $a += $Url
    $o = (& curl.exe @a 2>$null | Out-String)
    if (-not $o) { return $null }
    $dict = @{}
    foreach ($line in ($o -split "`n")) {
        $t = $line.Trim()
        if (-not $t) { continue }
        $i = $t.IndexOf('=')
        if ($i -gt 0) { $dict[$t.Substring(0, $i)] = $t.Substring($i + 1) }
    }
    if ($dict.Count -eq 0) { return $null }
    return [pscustomobject]@{ Ip = $dict['ip']; Loc = $dict['loc']; Colo = $dict['colo']; Warp = $dict['warp']; Raw = $dict }
}

$entries = @('Tun', 'Code', 'Chrome', 'Desktop')
$label = @{ Tun = 'TUN 全局'; Code = "Code ($($Ports.Mixed))"; Chrome = "Chrome ($($Ports.ChromeSocks))"; Desktop = "Desktop ($($Ports.DesktopHttp))" }
$collected = [ordered]@{}

foreach ($t in $traceUrls) {
    Write-Section "Cloudflare trace · $($t.Name)"
    $locs = @()
    foreach ($e in $entries) {
        $r = Get-Trace -Entry $e -Url $t.Url -TimeoutSec $TimeoutSec
        $collected["$e/$($t.Name)"] = $r
        if (-not $r) {
            Add-CheckResult -Status 'FAIL' -Item $label[$e] -Evidence '取不到 trace（入口不通）'
            continue
        }
        $locs += $r.Loc
        $ev = "ip=$($r.Ip)  loc=$($r.Loc)  colo=$($r.Colo)"
        if ($r.Warp -and $r.Warp -ne 'off') { $ev += "  warp=$($r.Warp)" }
        Add-CheckResult -Status $(if ($r.Loc -eq 'US') { 'PASS' } else { 'FAIL' }) -Item $label[$e] -Evidence $ev
    }
    $uniqLoc = @($locs | Where-Object { $_ } | Select-Object -Unique)
    $uniqIp = @($collected.GetEnumerator() | Where-Object { $_.Key -like "*/$($t.Name)" -and $_.Value } | ForEach-Object { $_.Value.Ip } | Select-Object -Unique)
    if ($uniqLoc.Count -le 1 -and $uniqIp.Count -le 1) {
        Add-CheckResult -Status $(if ($uniqLoc.Count -eq 1 -and $uniqLoc[0] -eq 'US') { 'PASS' } else { 'WARN' }) -Item "$($t.Name) 一致性" `
            -Evidence "$(if($uniqIp[0]){"ip=$($uniqIp[0]) "})loc=$($uniqLoc -join ',')"
    }
    else {
        Add-CheckResult -Status 'FAIL' -Item "$($t.Name) 一致性" -Evidence "各入口不一致：ip=$($uniqIp -join ',')  loc=$($uniqLoc -join ',')"
    }
}

# ------------------------------------------------- ip.net.coffee（无头 Chrome 真实渲染）
Write-Section 'ip.net.coffee/claude/  Claude 专向检测'
$coffee = 'https://ip.net.coffee/claude/'
$cf = [ordered]@{}
$coffeeRowsBefore = $script:CheckResults.Count

# 取值配方集中在 Common.ps1 的 Get-CoffeeReading（与独立的一键总检共用同一套）。
$domF = Get-CoffeeReading -Uri $coffee

if (-not $domF.Ok) {
    Add-CheckResult -Status 'WARN' -Item 'ip.net.coffee/claude/（自动取数未成功）' `
        -Evidence "$($domF.Error) → 该站对本机出口的风控是临时的，等一会儿再跑；急的话用 -OpenBrowser 人眼看"
}
else {
    # 该页所有数值都由前端 JS 注入，且标签与值之间夹着 HTML 标签 →
    # Get-CoffeeReading 内部只在「去标签 + 折叠空白」后的纯文本上匹配才稳（DOM 上做会串到 CSS / 脚本里去）。
    $cf['rendered'] = $true
    $cf['seconds'] = $domF.Seconds

    $egress = $domF.Egress
    $geoTxt = $domF.Geo
    $trust = $domF.Trust
    $trustLab = $domF.TrustLabel
    $attr = $domF.Attr
    $vpn = $domF.Vpn
    $proxy = $domF.Proxy
    $tor = $domF.Tor
    $crawl = $domF.Crawler
    $abuse = $domF.Abuse
    $region = $domF.Region

    $cf['egress'] = $egress
    $cf['geo'] = $geoTxt
    $cf['trust'] = $trust
    $cf['trustLabel'] = $trustLab
    $cf['attr'] = $attr
    $cf['vpn'] = $vpn
    $cf['proxy'] = $proxy
    $cf['tor'] = $tor
    $cf['crawler'] = $crawl
    $cf['abuse'] = $abuse
    $cf['region'] = $region

    $expect = @($collected.Values | Where-Object { $_ -and $_.Ip } | Select-Object -First 1).Ip
    if ($egress) {
        $ev = "$egress"
        if ($geoTxt) { $ev += "  $geoTxt" }
        if ($expect) { $ev += "（与 Claude 入口一致：$expect）" }
        Add-CheckResult -Status $(if (-not $expect -or $egress -eq $expect) { 'PASS' } else { 'FAIL' }) -Item 'Claude 专向出口 IP' -Evidence $ev
    }
    else {
        Add-CheckResult -Status 'WARN' -Item 'Claude 专向出口 IP' -Evidence "页面渲染成功但没解析出出口 IP（有效 DOM $($domF.DomLength) 字节）→ 浏览器复核"
    }

    if ($trust -ne '') {
        $ev = "$trust/100"
        if ($trustLab) { $ev += "  $trustLab" }
        $ev += "（合格线 ≥ $($TH.CoffeeTrustPass)）"
        Add-CheckResult -Status $(if ($trust -ge $TH.CoffeeTrustPass) { 'PASS' } else { 'WARN' }) -Item 'Trust Score' -Evidence $ev
    }
    if ($attr) {
        Add-CheckResult -Status $(if ($attr -eq $TH.CoffeeWantAttr) { 'PASS' } else { 'WARN' }) -Item 'IP 属性' -Evidence "$attr（期望 $($TH.CoffeeWantAttr)）"
    }

    $sig = Test-CoffeeClean -Reading $domF
    if ($sig.Signals.Count -gt 0) {
        Add-CheckResult -Status $(if ($sig.Dirty.Count -eq 0) { 'PASS' } else { 'FAIL' }) -Item '风险信号' -Evidence ($sig.Text + $(if ($sig.Dirty.Count -eq 0) { '  （全部未检测到）' } else { "  ← 命中：$($sig.Dirty -join '、')" }))
    }

    if ($abuse) { Add-CheckResult -Status $(if ($abuse -eq '无记录') { 'PASS' } else { 'FAIL' }) -Item '滥用记录' -Evidence $abuse }
    if ($region) { Add-CheckResult -Status $(if ($region -eq '正常') { 'PASS' } else { 'WARN' }) -Item 'Claude 支持地区' -Evidence $region }

    if ($script:CheckResults.Count -eq $coffeeRowsBefore) {
        Add-CheckResult -Status 'WARN' -Item 'ip.net.coffee/claude/' -Evidence "页面渲染成功但没有一个字段能解析（有效 DOM $($domF.DomLength) 字节）→ 浏览器复核"
    }

    # 顺带记录：其他出口（非 Claude 域名）走的是哪个组，出问题时可用来区分「谁绕过了 ACCOUNT-US」
    $cf['otherEgress'] = $domF.OtherEgress
}

if ($OpenBrowser) {
    Open-HandoffPage -Url $coffee | Out-Null
    Open-HandoffPage -Url "https://www.ping0.cc/?ip=$($collected['Tun/cloudflare.com'].Ip)" | Out-Null
    Open-HandoffPage -Url 'https://ippure.com/' | Out-Null
    Write-Host '  已交接：ip.net.coffee/claude/、ping0.cc、ippure.com（共用同一个专用 Chrome 实例）'
}

Write-Section '仍需人工看一眼的（浏览器专属）'
Write-Host '        ippure.com   人机流量占比 human ≥ 85%，IP 来源属性 = 住宅 / 原生，系数 0%'
Write-Host '                     （该站 API 要混淆 JS 生成的 x-t 签名头，无头拿不到 → 只能用 -OpenBrowser 看一眼）'
$manualIp = @($collected.Values | Where-Object { $_ -and $_.Ip } | Select-Object -First 1).Ip
if ($manualIp) { Write-Host "        当前出口 IP：$manualIp（三页显示的 IP 必须完全相同）" }

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows -FailHint '不合格多半是：① 某入口的落地国家不是 US（没走 ACCOUNT-US）；② ip.net.coffee 报出 VPN/Proxy/Tor 或滥用记录。' -WarnHint '程序层有提醒项，多为 IP 太新或站点抓取不稳定，建议浏览器复核后再决定。'

if ($Json) {
    Write-JsonResult @{ script = 'Check-ClaudeIp.ps1'; ok = ($code -eq 0); traces = $collected; coffee = $cf; results = $rows }
}
exit $code
