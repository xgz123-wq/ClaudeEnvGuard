#requires -Version 7.0
<#
.SYNOPSIS
    出口 IP 信誉体检 —— 自动抓取可无头解析的站点，并明确交接必须人工看的站点。

.DESCRIPTION
    四个站的可自动化程度不同，本脚本不假装都能自动跑：
      · iprisk.top   → 纯 HTTP 可解析（纯净度评分 / 机房·代理信号 / 黑名单 / 原生 IP）
      · ping0.cc     → 无头 Chrome 真实渲染（风控值 / IP 类型 / 原生 IP / 共享人数）★
      · ip.net.coffee/claude/ → 无头 Chrome 真实渲染（见第 8 层 Check-ClaudeIp.ps1）
      · ippure.com   → 只能浏览器（API 要混淆 JS 生成的 x-t 签名头，无头拿不到）

    所以本脚本产出两段：
      A. 自动判定的结论（可直接进 CI / 总检）
      B. 人工核验交接清单（-OpenBrowser 用 43866 专用 Chrome 打开）

.PARAMETER Ip
    要检测的 IP。不给则按 -Entry 自动探测当前真实出口。

.PARAMETER Entry
    Tun / Code(43865) / Chrome(43866) / Desktop(43867) —— 决定用哪个入口探出口 IP。

.PARAMETER AllEntries
    四个入口全测，并校验是否同一个出口（用户的核心诉求：Claude 三入口一致性）。

.PARAMETER OpenBrowser
    用专用 Chrome（独立 User Data + socks5 43866 + 远端 DNS + 禁 WebRTC 泄漏）打开人工核验页。

.EXAMPLE
    pwsh -File Check-IpReputation.ps1
    pwsh -File Check-IpReputation.ps1 -AllEntries
    pwsh -File Check-IpReputation.ps1 -Ip <出口IP> -Json
    pwsh -File Check-IpReputation.ps1 -OpenBrowser
#>
[CmdletBinding()]
param(
    [string] $Ip = '',
    [ValidateSet('Auto', 'Tun', 'Code', 'Chrome', 'Desktop')][string] $Entry = 'Auto',
    [switch] $AllEntries,
    [switch] $OpenBrowser,
    [switch] $Json,
    [int] $TimeoutSec = 30
)

$ErrorActionPreference = 'Continue'
$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

Reset-CheckResults
$TH = $script:Thresholds
$entries = if ($AllEntries) { @('Tun', 'Code', 'Chrome', 'Desktop') } else { @() }

# ============================================================ 1. 出口 IP
Write-Banner 'Claude 安全出口链路 · 出口 IP 信誉体检' ("skill: " + (Get-SkillRoot))

$observed = [ordered]@{}
if ($AllEntries) {
    Write-Section '一、四个入口的真实出口 IP'
    foreach ($e in $entries) {
        $got = Get-EgressIp -Entry $e -TimeoutSec $TimeoutSec
        $observed[$e] = $got
        $port = switch ($e) { 'Tun' { '(系统 TUN)' } 'Code' { $script:Ports.Mixed } 'Chrome' { $script:Ports.ChromeSocks } 'Desktop' { $script:Ports.DesktopHttp } }
        Add-CheckResult -Status $(if ($got) { 'PASS' } else { 'FAIL' }) -Item "入口 $e" -Evidence $(if ($got) { "$got  端口 $port" } else { "取不到出口 IP（端口 $port 可能没在监听）" })
    }
    $uniq = @($observed.Values | Where-Object { $_ } | Select-Object -Unique)
    if ($uniq.Count -eq 1 -and $uniq[0]) {
        Add-CheckResult -Status 'PASS' -Item '三入口出口一致性' -Evidence "全部为 $($uniq[0])"
    }
    elseif ($uniq.Count -gt 1) {
        $detail = (($observed.GetEnumerator() | ForEach-Object { "$($_.Key)=$($_.Value)" }) -join ' / ')
        Add-CheckResult -Status 'FAIL' -Item '三入口出口一致性' -Evidence "出口不一致：$detail"
    }
    Reset-CheckResults

    if (-not $Ip) { $Ip = @($observed.Values | Where-Object { $_ } | Select-Object -First 1) }
}

if (-not $Ip) {
    $probeEntry = if ($Entry -eq 'Auto') { 'Tun' } else { $Entry }
    $Ip = Get-EgressIp -Entry $probeEntry -TimeoutSec $TimeoutSec
    if ($Ip) {
        Write-Section '一、当前出口 IP'
        Write-Host "  入口 $probeEntry  →  $Ip"
    }
}

if (-not $Ip) {
    Write-Host ''
    Write-Host '[FAIL] 取不到任何出口 IP —— 后续检测无法进行。'
    Write-Host '       可能原因：Clash 没在跑；TUN 关了且没有指定 -Entry；端口号改了（去 config\Topology.psd1 核对）。'
    if ($Json) { Write-JsonResult @{ ok = $false; error = 'no-egress-ip' } }
    exit 1
}

$ipaddr = $Ip.ToString().Trim()
Write-Host ''
Write-Host ("  本次检测目标 IP：$ipaddr")

# ============================================================ 2. iprisk.top（主力）
Write-Section '二、iprisk.top —— 纯净度 / 机房·代理信号 / 黑名单'
$iprisk = [ordered]@{}
$ipriskTbl = @{}
$ipriskOk = $false

$badge = Invoke-HeadlessFetch -Uri "https://iprisk.top/badge/$ipaddr" -TimeoutSec $TimeoutSec -Retry 2 -Referer 'https://iprisk.top/'
if ($badge.Ok -and -not (Test-CaptchaGate $badge.Content)) {
    $bScore = ''
    $m = [regex]::Match($badge.Content, 'aria-label="[^"]*?(\d{1,3})\s*/\s*100')
    if ($m.Success) { $bScore = [int]$m.Groups[1].Value }
    else {
        $m2 = [regex]::Match($badge.Content, '>(\d{1,3})\s*/\s*100')
        if ($m2.Success) { $bScore = [int]$m2.Groups[1].Value }
    }
    $grade = ''
    $gm = [regex]::Match($badge.Content, '>\s*\d{1,3}\s*/\s*100\s*([^<>]{1,20}?)\s*<')
    if ($gm.Success) { $grade = $gm.Groups[1].Value.Trim() }
    $iprisk['badgeScore'] = $bScore
    $iprisk['badgeGrade'] = $grade
    if ($bScore -ne '') { $ipriskOk = $true }
}

$page = Invoke-HeadlessFetch -Uri "https://iprisk.top/ip/$ipaddr" -TimeoutSec $TimeoutSec -Retry 2 -Referer 'https://iprisk.top/'
if ($page.Ok -and -not (Test-CaptchaGate $page.Content)) {
    $ipriskTbl = ConvertTo-HtmlTable -Html $page.Content
    $ipriskOk = $true
    $iprisk['title'] = [regex]::Match($page.Content, '<title>([^<]*)</title>').Groups[1].Value
    $iprisk['desc'] = [regex]::Match($page.Content, '<meta name="description" content="([^"]*)"').Groups[1].Value

    $h = @()
    foreach ($t in [regex]::Matches($page.Content, '(?s)<h2[^>]*>(.*?)</h2>')) { $h += (ConvertTo-PlainText $t.Groups[1].Value) }
    $iprisk['headings'] = ($h -join ' / ')

    $cl = @()
    foreach ($t in [regex]::Matches($page.Content, '(?s)<div class="checklist-item (pass|fail)">(.*?)</div>')) {
        $st = $t.Groups[1].Value
        $txt = ConvertTo-PlainText ([regex]::Match($t.Groups[2].Value, 'class="checklist-text">(.*?)</span>').Groups[1].Value)
        if (-not $txt) { $txt = ConvertTo-PlainText $t.Groups[2].Value }
        $cl += [pscustomobject]@{ Status = $st; Text = $txt }
    }
    $iprisk['checklist'] = $cl
    $chips = @()
    foreach ($c in [regex]::Matches($page.Content, '(?s)<div class="risk-chip-list">(.*?)</div>')) {
        foreach ($s in [regex]::Matches($c.Groups[1].Value, '(?s)<span[^>]*>(.*?)</span>')) { $chips += (ConvertTo-PlainText $s.Groups[1].Value) }
    }
    $iprisk['tags'] = ($chips -join ', ')
}

if (-not $ipriskOk) {
    if (Test-CaptchaGate $page.Content) {
        Add-CheckResult -Status 'WARN' -Item 'iprisk.top' -Evidence '被 Turnstile 拦下，需浏览器人工看'
    }
    else {
        Add-CheckResult -Status 'WARN' -Item 'iprisk.top' -Evidence ("抓取失败：" + $page.Error)
    }
}
else {
    $score = if ($iprisk['badgeScore'] -ne '') { [int]$iprisk['badgeScore'] } else { 0 }
    $scoreTxt = if ($ipriskTbl.ContainsKey('纯净度评分')) { $ipriskTbl['纯净度评分'] } else { "$score/100" }
    $typeTxt = Get-DictValue $ipriskTbl @('IP 类型', '*类型')
    $proxyTxt = Get-DictValue $ipriskTbl @('是否代理*', '*代理/VPN*')
    $idcTxt = Get-DictValue $ipriskTbl @('是否机房*', '*机房 IP*')
    $ispTxt = Get-DictValue $ipriskTbl @('ISP', '*运营商*')
    $asnTxt = Get-DictValue $ipriskTbl @('ASN')
    $tagTxt = Get-DictValue $ipriskTbl @('标签', '*标签*')
    $lvlTxt = Get-DictValue $ipriskTbl @('风险信号等级', '*等级*')

    $iprisk['score'] = $score
    $iprisk['scoreText'] = $scoreTxt
    $iprisk['type'] = $typeTxt
    $iprisk['isProxy'] = $proxyTxt
    $iprisk['isIdc'] = $idcTxt
    $iprisk['isp'] = $ispTxt
    $iprisk['asn'] = $asnTxt
    $iprisk['riskLevel'] = $lvlTxt

    $st = if ($score -ge $TH.IpRiskScorePass) { 'PASS' } elseif ($score -ge $TH.IpRiskScoreWarn) { 'WARN' } else { 'FAIL' }
    Add-CheckResult -Status $st -Item '纯净度评分' -Evidence "$scoreTxt（合格线 ≥ $($TH.IpRiskScorePass)/100）  等级：$lvlTxt"

    $st = if ($typeTxt -match '住宅|家宽|居民') { 'PASS' } elseif ($typeTxt -match '机房|数据中心|IDC|托管') { 'FAIL' } else { 'WARN' }
    Add-CheckResult -Status $st -Item 'IP 类型' -Evidence "$typeTxt  (ISP $ispTxt / $asnTxt)"

    $st = if ($proxyTxt -match '^否|^不是|no$') { 'PASS' } elseif ($proxyTxt) { 'FAIL' } else { 'WARN' }
    Add-CheckResult -Status $st -Item '代理 / VPN 信号' -Evidence $(if ($proxyTxt) { $proxyTxt } else { '未取到' })

    $st = if ($idcTxt -match '^否|^不是|no$') { 'PASS' } elseif ($idcTxt) { 'FAIL' } else { 'WARN' }
    Add-CheckResult -Status $st -Item '机房 IP 信号' -Evidence $(if ($idcTxt) { $idcTxt } else { '未取到' })

    if ($iprisk['checklist']) {
        foreach ($c in $iprisk['checklist']) {
            if ($c.Status -eq 'fail') {
                $s = if ($c.Text -match '使用时间|新 ?IP|时间较短') { 'WARN' } else { 'FAIL' }
                Add-CheckResult -Status $s -Item '风险项' -Evidence $c.Text
            }
        }
    }
    if ($tagTxt) { Add-CheckResult -Status 'PASS' -Item '标签' -Evidence $tagTxt }
}

# ============================================================ 3. ipinfo.io 交叉印证
Write-Section '三、ipinfo.io —— ASN / 地理位置交叉印证'
$ipinfoText = ''
$ii = Invoke-HeadlessFetch -Uri "https://ipinfo.io/$ipaddr/json" -TimeoutSec $TimeoutSec -Retry 1
if ($ii.Ok -and $ii.Content -match '\{') {
    try {
        $o = $ii.Content | ConvertFrom-Json
        $ipinfoText = "$($o.city) / $($o.region) / $($o.country) / $($o.org) / $($o.timezone)"
        Add-CheckResult -Status 'PASS' -Item 'ASN / 地理位置' -Evidence $ipinfoText
        $observed['asn'] = $o.org
        $observed['timezone'] = $o.timezone
        $observed['country'] = $o.country
    }
    catch { Add-CheckResult -Status 'WARN' -Item 'ASN / 地理位置' -Evidence 'JSON 解析失败' }
}
else { Add-CheckResult -Status 'WARN' -Item 'ASN / 地理位置' -Evidence ("抓取失败：" + $ii.Error) }

# ============================================================ 4. ping0.cc（无头 Chrome 真实渲染）
Write-Section '四、ping0.cc —— 风控值 / IP 类型 / 原生 IP / 共享人数'
$ping0 = [ordered]@{}

# 该站数值由前端 JS 注入，纯 HTTP 只能拿到骨架 —— 必须真实渲染。
# 取值配方集中在 Common.ps1 的 Get-Ping0Reading（与独立的一键总检共用同一套），
# 这样两个入口不会出现「一个说通过、另一个说 WARN」的分歧。
$p0 = Get-Ping0Reading -Uri "https://www.ping0.cc/?ip=$ipaddr"

if (-not $p0.Ok) {
    # 渲染失败才降级，绝不用「抓不到」冒充「没问题」
    Add-CheckResult -Status 'WARN' -Item 'ping0.cc（自动取数未成功）' `
        -Evidence "$($p0.Error) → 该站对本机出口的风控是临时的，等一会儿再跑；急的话用 -OpenBrowser 人眼看"
    $ping0['gated'] = $true
}
else {
    $risk = $p0.Risk
    $lab = $p0.RiskLabel
    $iptype = $p0.IpType
    $native = $p0.Native
    $share = $p0.Share
    $pdesc = $p0.Desc
    $ping0['rendered'] = $true
    $ping0['seconds'] = $p0.Seconds
    $ping0['risk'] = $risk
    $ping0['riskLabel'] = $lab
    $ping0['ipType'] = $iptype
    $ping0['native'] = $native
    $ping0['share'] = $share
    $ping0['desc'] = $pdesc

    if ($risk -match '(\d{1,3})') {
        $num = [int]$Matches[1]
        $st = if ($num -lt $TH.Ping0RiskPass) { 'PASS' } else { 'WARN' }
        $ev = "$risk"
        if ($lab) { $ev += "  $lab" }
        $ev += "（合格线 < $($TH.Ping0RiskPass)%）"
        Add-CheckResult -Status $st -Item '风控值' -Evidence $ev
    }
    else {
        Add-CheckResult -Status 'WARN' -Item '风控值' -Evidence "页面渲染成功但没解析出风控值（有效 DOM $($p0.DomLength) 字节）→ 浏览器复核"
    }

    if ($iptype) {
        $st = if ($iptype -match '家庭宽带|住宅|家宽') { 'PASS' } elseif ($iptype -match 'IDC|机房|数据中心|托管') { 'WARN' } else { 'WARN' }
        Add-CheckResult -Status $st -Item 'IP 类型 (ping0)' -Evidence "$iptype（期望 $($TH.Ping0WantIpType)）"
    }

    if ($native) {
        Add-CheckResult -Status $(if ($native -eq '原生 IP') { 'PASS' } else { 'WARN' }) -Item '原生 IP (ping0)' -Evidence $native
    }

    if ($share) { Add-CheckResult -Status 'PASS' -Item '共享人数' -Evidence "$share（住宅 IP 共享越少越干净）" }
    if ($pdesc) { Add-CheckResult -Status 'PASS' -Item 'ASN 摘要' -Evidence $pdesc }
}

$autoRows = @($script:CheckResults)

# ============================================================ 5. 浏览器交叉复核
Write-Section '五、浏览器交叉复核（选做 —— 自动判定已在上文，这里只是多一双眼睛）'
$handoff = @(
    [pscustomobject]@{ Need = '必看'; Site = 'ippure.com';          Url = 'https://ippure.com/';              Watch = "它的数据接口要一个混淆 JS 现算的 x-t 签名头，无头拿不到 → 只能人眼。看：人机流量占比 human ≥ $($TH.IppureHumanWarn)%；系数应为 $($TH.IppureFactorPass)%；IP 来源属性应为「住宅 / 原生」" }
    [pscustomobject]@{ Need = '选做'; Site = 'iprisk.top';          Url = "https://iprisk.top/ip/$ipaddr";    Watch = '无头已读到纯净度与信号，人眼是防止站点改版导致正则失配。看：黑名单全清' }
    [pscustomobject]@{ Need = '选做'; Site = 'ip.net.coffee/claude'; Url = 'https://ip.net.coffee/claude/';    Watch = '无头已读到 Trust Score / 属性 / 风险信号（见第 8 层）。看：时区与语言是否与「美国家宽出口」自洽' }
)
$w1 = ($handoff | ForEach-Object { Get-DisplayWidth $_.Site } | Measure-Object -Maximum).Maximum
foreach ($h in $handoff) {
    Write-Host ("  · [" + $h.Need + "] " + (Format-Cell $h.Site $w1) + "  " + $h.Url)
    Write-Host ("    " + (' ' * $w1) + "  看什么：" + $h.Watch)
}
if ($OpenBrowser) {
    Write-Host ''
    Write-Host '  正在用 43866 专用 Chrome 打开（独立 User Data + socks5 + 远端 DNS + 禁 WebRTC 泄漏）：'
    foreach ($h in $handoff) { Write-Host ("    → " + $h.Url); Open-HandoffPage -Url $h.Url | Out-Null }
}

# ============================================================ 6. 结论
$code = Write-Verdict -Rows $autoRows -FailHint '先换节点（固定一个住宅出口）再重测；不要带着不合格的出口去登 Claude。' -WarnHint '警告项多数是 IP 太新或站点抓取不稳定，用浏览器人工确认后再决定。'

# ============================================================ 7. JSON
if ($Json) {
    Write-JsonResult @{
        ok               = ($code -eq 0)
        ip               = $ipaddr
        entry            = $Entry
        allEntries       = $observed
        iprisk           = $iprisk
        ipriskTable      = $ipriskTbl
        ping0            = $ping0
        ipinfo           = $ipinfoText
        headlessChecked  = @($ipriskOk, [bool]$ping0['risk'])
        manualChecklist  = $handoff
        results          = $autoRows
    }
}

exit $code
