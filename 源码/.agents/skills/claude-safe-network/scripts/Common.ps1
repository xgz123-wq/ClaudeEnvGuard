#requires -Version 7.0
# ============================================================================
#  claude-safe-network · 公共库 (Common.ps1)
# ----------------------------------------------------------------------------
#  被 scripts\ 下所有检查脚本 dot-source。
#  职责：定位 skill 根目录、加载拓扑真源、统一 HTTP 抓取、统一结果表输出。
#
#  用法（每个调用脚本开头）：
#      $__d = $PSScriptRoot
#      while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
#      if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
#      . (Join-Path $__d 'Common.ps1')
# ============================================================================

[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [System.Text.UTF8Encoding]::new($false)

# ---------------------------------------------------------------- 根目录解析
$script:ScriptsRoot = $PSScriptRoot
$script:SkillRoot = $null
$__probe = $PSScriptRoot
while ($__probe) {
    if (Test-Path (Join-Path $__probe 'config\Topology.psd1')) { $script:SkillRoot = $__probe; break }
    $__parent = Split-Path $__probe -Parent
    if (-not $__parent -or $__parent -eq $__probe) { break }
    $__probe = $__parent
}
if (-not $script:SkillRoot) {
    throw "Common.ps1：向上找不到 config\Topology.psd1。请保持 skill 目录结构完整。"
}
Remove-Variable __probe, __parent -ErrorAction SilentlyContinue

$script:Topology = Import-PowerShellDataFile -Path (Join-Path $script:SkillRoot 'config\Topology.psd1')
$script:Ports = $script:Topology.Ports
$script:Groups = $script:Topology.Groups
$script:Thresholds = $script:Topology.Thresholds

function Get-SkillRoot { return $script:SkillRoot }
function Get-ScriptsRoot { return $script:ScriptsRoot }
function Get-Topology { return $script:Topology }

function Expand-TopologyPath {
    param([Parameter(Mandatory)][string]$Value)
    $out = $Value
    foreach ($k in 'APPDATA', 'LOCALAPPDATA', 'USERPROFILE', 'TEMP', 'ProgramFiles') {
        $v = [Environment]::GetEnvironmentVariable($k)
        if ($v) { $out = $out -replace ([regex]::Escape('${' + $k + '}')), ($v -replace '\\', '/') }
    }
    return $out -replace '/', '\'
}

function Get-VergeDir { return (Expand-TopologyPath $script:Topology.VergeDir) }

# 取某个专用浏览器账号的 UserData 目录（默认第一个账号）
function Get-AccountUserDataDir {
    param([string] $Name = '')
    $accounts = @($script:Topology.Accounts)
    $a = if ($Name) { @($accounts | Where-Object { $_.Name -eq $Name } | Select-Object -First 1) } else { @($accounts | Select-Object -First 1) }
    if (-not $a -or -not $a[0]) { return $null }
    return (Expand-TopologyPath $a[0].UserDataDir)
}

# ---------------------------------------------------------------- 结果收集器
$script:CheckResults = [System.Collections.Generic.List[object]]::new()
# 默认：加一条就当场打印一条。这样结果天然落在所属小节标题之下，
# 不会出现「标题全打完、结果堆在最后」的错位。
$script:EchoResults = $true

function Reset-CheckResults { $script:CheckResults.Clear() }

function Add-CheckResult {
    param(
        [Parameter(Mandatory)][ValidateSet('PASS', 'WARN', 'FAIL', 'SKIP', 'MANUAL')][string]$Status,
        [Parameter(Mandatory)][string]$Item,
        [string]$Evidence = '',
        [switch]$Quiet
    )
    $script:CheckResults.Add([pscustomobject]@{ Status = $Status; Item = $Item; Evidence = $Evidence })
    if ($script:EchoResults -and -not $Quiet) { Write-CheckResult -Status $Status -Item $Item -Evidence $Evidence }
}

# ---------------------------------------------------------------- 显示宽度
function Get-DisplayWidth {
    param([string]$Text)
    if ([string]::IsNullOrEmpty($Text)) { return 0 }
    $w = 0
    foreach ($ch in $Text.ToCharArray()) {
        $c = [int]$ch
        # CJK 统一汉字 / 全角标点 / 假名 / 谚文 → 双宽
        if (($c -ge 0x1100 -and $c -le 0x115F) -or ($c -ge 0x2E80 -and $c -le 0xA4CF) -or
            ($c -ge 0xAC00 -and $c -le 0xD7A3) -or ($c -ge 0xF900 -and $c -le 0xFAFF) -or
            ($c -ge 0xFE30 -and $c -le 0xFE6F) -or ($c -ge 0xFF00 -and $c -le 0xFF60) -or
            ($c -ge 0xFFE0 -and $c -le 0xFFE6)) { $w += 2 } else { $w += 1 }
    }
    return $w
}

function Format-Cell {
    param([string]$Text, [int]$Width, [ValidateSet('Left', 'Right')][string]$Align = 'Left')
    $t = if ($null -eq $Text) { '' } else { [string]$Text }
    $pad = $Width - (Get-DisplayWidth $t)
    if ($pad -lt 0) { $pad = 0 }
    if ($Align -eq 'Right') { return (' ' * $pad) + $t }
    return $t + (' ' * $pad)
}

# ---------------------------------------------------------------- 输出helper
function Write-Banner {
    param([Parameter(Mandatory)][string]$Title, [string]$Subtitle = '')
    $line = '=' * 74
    Write-Host ''
    Write-Host $line
    Write-Host ("  " + $Title)
    if ($Subtitle) { Write-Host ("  " + $Subtitle) }
    Write-Host $line
}

function Write-Section { param([Parameter(Mandatory)][string]$Title) Write-Host ''; Write-Host ("--- " + $Title + " " + ('-' * [Math]::Max(0, 68 - (Get-DisplayWidth $Title)))) }

$script:StatusTag = @{ PASS = '[PASS]  '; WARN = '[WARN]  '; FAIL = '[FAIL]  '; SKIP = '[SKIP]  '; MANUAL = '[MANUAL]' }

function Write-CheckResult {
    param([string]$Status, [string]$Item, [string]$Evidence = '')
    $tag = $script:StatusTag[$Status]
    if (-not $tag) { $tag = '[' + $Status + ']' }
    $head = "$tag $Item"
    if ($Evidence) {
        $pad = [Math]::Max(1, 42 - (Get-DisplayWidth $head))
        Write-Host ($head + (' ' * $pad) + $Evidence)
    }
    else { Write-Host $head }
}

function Write-ResultTable {
    # 仅用于把 $script:EchoResults = $false 时攒下的结果一次性打出来。
    param([object[]]$Rows)
    if (-not $Rows -or $Rows.Count -eq 0) { return }
    foreach ($r in $Rows) { Write-CheckResult -Status $r.Status -Item $r.Item -Evidence $r.Evidence }
}

function Write-Verdict {
    param([object[]]$Rows, [string]$FailHint = '', [string]$WarnHint = '')
    $f = @($Rows | Where-Object { $_.Status -eq 'FAIL' }).Count
    $w = @($Rows | Where-Object { $_.Status -eq 'WARN' }).Count
    $p = @($Rows | Where-Object { $_.Status -eq 'PASS' }).Count
    $m = @($Rows | Where-Object { $_.Status -eq 'MANUAL' }).Count
    Write-Host ''
    if ($f -gt 0) {
        Write-Host ("结论：发现 $f 项不合格 / $w 项警告 / $p 项通过。不建议在此状态下启动 Claude。")
        if ($FailHint) { Write-Host ("      " + $FailHint) }
        return 1
    }
    if ($w -gt 0) {
        Write-Host ("结论：$p 项通过，但存在 $w 项警告 —— 不能当作已验收。")
        if ($WarnHint) { Write-Host ("      " + $WarnHint) }
        return 0
    }
    if ($m -gt 0) {
        Write-Host ("结论：程序层 $p 项全部通过；另有 $m 项需人工在浏览器里确认后，才算真正验收。")
        return 0
    }
    Write-Host ("结论：$p 项全部通过。")
    return 0
}

# ---------------------------------------------------------------- HTTP 抓取
# UA 里的 Chrome 主版本从本机 chrome.exe 现取：写死版本号会随着 Chrome 升级
# 变成"比浏览器还老"的指纹，反而更容易被风控盯上。
function Get-RealUa {
    $ver = '131'
    $exe = Get-ChromeExe
    if ($exe) {
        try {
            $pv = [string](Get-Item -LiteralPath $exe).VersionInfo.ProductVersion
            if ($pv -match '^(\d+)') { $ver = $Matches[1] }
        }
        catch { }
    }
    return 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/' + $ver + '.0.0.0 Safari/537.36'
}

function Invoke-HeadlessFetch {
    param(
        [Parameter(Mandatory)][string]$Uri,
        [int]$TimeoutSec = 30,
        [int]$Retry = 1,
        [string]$Referer = ''
    )
    $h = @{
        'User-Agent'      = (Get-RealUa)
        'Accept'          = 'text/html,application/xhtml+xml,application/xml;q=0.9,image/svg+xml,*/*;q=0.8'
        'Accept-Language' = 'en-US,en;q=0.9'
        'Cache-Control'   = 'no-cache'
    }
    if ($Referer) { $h['Referer'] = $Referer }
    $last = ''
    for ($i = 0; $i -le $Retry; $i++) {
        try {
            $r = Invoke-WebRequest -Uri $Uri -Headers $h -TimeoutSec $TimeoutSec -MaximumRedirection 5
            return [pscustomobject]@{ Ok = $true; Status = [int]$r.StatusCode; Content = [string]$r.Content; Error = '' }
        }
        catch {
            $last = $_.Exception.Message
            if ($i -lt $Retry) { Start-Sleep -Milliseconds 900 }
        }
    }
    return [pscustomobject]@{ Ok = $false; Status = 0; Content = ''; Error = $last }
}

function Test-CaptchaGate {
    param([string]$Html)
    if ([string]::IsNullOrEmpty($Html)) { return $false }
    return [bool]($Html -match 'cf-turnstile|AliyunCaptchaConfig|challenges\.cloudflare\.com/turnstile')
}

# ---------------------------------------------------------------- 真浏览器渲染抓取
# 为什么需要它：ping0.cc / ip.net.coffee 的关键数字是页面里 JS 跑完才写进 DOM 的，
# 纯 HTTP 抓到的只是一个空壳（或直接被风控挡回空页）。这里用本机 Chrome 的
# headless 模式真实渲染一遍，再把渲染后的 DOM 取回来（--dump-dom），
# 于是「必须人工开浏览器看」的项也能自动判定。
#
# 三个必须踩对的点（都实测过）：
#   1) 必须给真实 User-Agent。headless 默认 UA 里带 "HeadlessChrome"，ping0.cc 会
#      直接吐回 41 字节的空页；换成真实 Chrome UA + 关掉 AutomationControlled
#      特征后，同一台机器立刻拿到 121KB 完整页面。
#   2) 必须用独立的 --user-data-dir。复用默认 profile 的话 Chrome 会把 URL 转交给
#      用户正在使用的那个实例然后自己立刻退出，--dump-dom 什么都拿不到。
#   3) 必须给 --virtual-time-budget。SPA（ip.net.coffee）只给 --timeout 会拿到
#      5 秒时的半成品页，数据还没注入。
#
# 偶发情况：ping0.cc 连续渲染多次后偶尔会吐 ~1.4KB 的 Cloudflare Turnstile 挑战页。
# 那不是"站点说没问题"，而是被拦了 —— 所以这里会识别出来（Test-CaptchaGate）
# 并让 Invoke-HeadlessDomFetch 隔几秒重试一次。
# 无头渲染用的持久 Profile 目录：复用 cookie，降低被风控挑战的概率。
# 为什么不用全新临时目录：每次都像"第一次来的陌生访客"，短时间多跑几次就会被 CAPTCHA 挡。
function Get-HeadlessProfileDir {
    $v = ''
    if ($script:Topology.Headless) { $v = [string]$script:Topology.Headless.ProfileDir }
    if (-not $v) { $v = '${LOCALAPPDATA}\claude-safe-network\chrome-profile' }
    return (Expand-TopologyPath $v)
}

function Get-ChromeExe {
    $c = [string]$script:Topology.ChromeExe
    if (-not $c) { return $null }
    $c = Expand-TopologyPath $c
    if (Test-Path -LiteralPath $c) { return $c }
    return $null
}

function Invoke-HeadlessDomFetch {
    param(
        [Parameter(Mandatory)][string]$Uri,
        [int]$TimeoutSec = 30,
        [int]$VirtualTimeBudgetMs = 8000,
        [int]$Retry = 1,
        [int]$MinDomBytes = 400,
        [string]$ProfileDir = ''
    )
    $last = $null
    for ($i = 0; $i -le $Retry; $i++) {
        $r = Invoke-HeadlessDomFetchOnce -Uri $Uri -TimeoutSec $TimeoutSec -VirtualTimeBudgetMs $VirtualTimeBudgetMs -MinDomBytes $MinDomBytes -ProfileDir $ProfileDir
        if ($r.Ok) { return $r }
        $last = $r
        # 风控挑战页是瞬时的（隔几秒再渲染往往就正常了），值得重试；
        # 其它失败（找不到 Chrome / 渲染超时）重试无意义，早点返回省时间。
        if ($i -lt $Retry -and $r.Error -match '挑战页|空页|风控') { Start-Sleep -Seconds 5 }
        else { break }
    }
    return $last
}

function Invoke-HeadlessDomFetchOnce {
    param(
        [Parameter(Mandatory)][string]$Uri,
        [int]$TimeoutSec = 30,
        [int]$VirtualTimeBudgetMs = 8000,
        [int]$MinDomBytes = 400,
        [string]$ProfileDir = ''
    )
    $t0 = Get-Date
    $exe = Get-ChromeExe
    if (-not $exe) {
        return [pscustomobject]@{ Ok = $false; Dom = ''; Plain = ''; Error = '找不到 Chrome（config\Topology.psd1 里的 ChromeExe）'; Seconds = 0 }
    }

    # UA 按本机真实 Chrome 主版本拼，避免留下 headless 指纹
    $ua = Get-RealUa

    # 默认每次都用全新的临时 user-data-dir；但如果给了持久目录，就复用它 ——
    # 复用会累积 cookie / localStorage，看起来更像"回访用户"，能显著降低被风控挑战的概率
    # （ping0.cc 在短时间内被反复自动访问时，会开始返 Turnstile 挑战页）。
    $persistent = $false
    if ($ProfileDir) {
        $ud = $ProfileDir
        $null = New-Item -ItemType Directory -Path $ud -Force -ErrorAction SilentlyContinue
        $persistent = $true
    }
    else {
        $tag = [guid]::NewGuid().ToString('n')
        $ud = $env:TEMP + '\claude-safe-net-' + $tag
        $null = New-Item -ItemType Directory -Path $ud -Force -ErrorAction SilentlyContinue
    }

    $argl = '--headless=new --disable-gpu --no-first-run --no-default-browser-check' `
        + ' --disable-extensions --disable-sync --disable-default-apps --mute-audio' `
        + ' --window-size=1280,2400 --disable-blink-features=AutomationControlled' `
        + ' --user-agent="' + $ua + '"' `
        + ' --user-data-dir="' + $ud + '"' `
        + ' --timeout=' + ($TimeoutSec * 1000) `
        + ' --virtual-time-budget=' + $VirtualTimeBudgetMs `
        + ' --dump-dom "' + $Uri + '"'

    $dom = ''
    $errmsg = ''
    $p = $null
    try {
        # 不用 Start-Process -RedirectStandardOutput：那个输出文件在父进程里还占着句柄，
        # 紧接着读会抛 "file is being used by another process"（实测踩过）。
        # 直接读管道没有这个问题，也省掉临时文件。
        $psi = [System.Diagnostics.ProcessStartInfo]::new()
        $psi.FileName = $exe
        $psi.Arguments = $argl               # 整条命令行原样交给 Chrome，它自己拆参数
        $psi.UseShellExecute = $false
        $psi.CreateNoWindow = $true
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        $psi.StandardOutputEncoding = [System.Text.UTF8Encoding]::new($false)
        $psi.StandardErrorEncoding = [System.Text.UTF8Encoding]::new($false)
        $p = [System.Diagnostics.Process]::Start($psi)
        # 必须先起异步读再等退出，否则 12 万字节的 DOM 会把管道堵死（经典死锁）
        $stdoutTask = $p.StandardOutput.ReadToEndAsync()
        $null = $p.StandardError.ReadToEndAsync()
        if (-not $p.WaitForExit(($TimeoutSec + 20) * 1000)) {
            try { $p.Kill($true) } catch { }
            $errmsg = '渲染超时'
        }
        try { $dom = $stdoutTask.GetAwaiter().GetResult() } catch { }
    }
    catch { $errmsg = $_.Exception.Message }
    finally { if ($p) { try { $p.Dispose() } catch { } } }

    if (-not $persistent) { Remove-Item -LiteralPath $ud -Recurse -Force -ErrorAction SilentlyContinue }

    # 空壳页 / 风控挑战页都视为失败，别让上层把空页当成"站点说没问题"
    if ($dom.Length -lt $MinDomBytes) {
        if (-not $errmsg) { $errmsg = '页面渲染结果为空页（可能被风控拦截或网络不通）' }
        return [pscustomobject]@{ Ok = $false; Dom = ''; Plain = ''; Error = $errmsg; Seconds = [math]::Round(((Get-Date) - $t0).TotalSeconds, 1) }
    }
    if (Test-CaptchaGate $dom) {
        return [pscustomobject]@{ Ok = $false; Dom = ''; Plain = ''; Error = "被 Cloudflare 挑战页拦下（有效 DOM 仅 $($dom.Length) 字节）"; Seconds = [math]::Round(((Get-Date) - $t0).TotalSeconds, 1) }
    }
    if ($errmsg) {
        return [pscustomobject]@{ Ok = $false; Dom = ''; Plain = ''; Error = $errmsg; Seconds = [math]::Round(((Get-Date) - $t0).TotalSeconds, 1) }
    }
    return [pscustomobject]@{
        Ok      = $true
        Dom     = $dom
        Plain   = (ConvertTo-PlainText $dom)
        Error   = ''
        Seconds = [math]::Round(((Get-Date) - $t0).TotalSeconds, 1)
    }
}

function ConvertTo-PlainText {
    param([string]$Html)
    if ([string]::IsNullOrEmpty($Html)) { return '' }
    $s = [regex]::Replace($Html, '(?s)<script.*?</script>', ' ')
    $s = [regex]::Replace($s, '(?s)<style.*?</style>', ' ')
    $s = [regex]::Replace($s, '(?s)<[^>]+>', ' ')
    $s = $s -replace '&nbsp;', ' ' -replace '&lt;', '<' -replace '&gt;', '>' -replace '&amp;', '&' -replace '&quot;', '"'
    return ($s -replace '\s+', ' ').Trim()
}

# ---------------------------------------------------------------- 需要管理员的实盘核对
# Windows 只允许管理员读 WFP 过滤器实盘，这是系统设计，不是脚本能绕过的：
# netsh wfp show filters 与直接调 FwpmFilterGetById0 两条路在非提权下都是
# ERROR_ACCESS_DENIED (5)，实测确认。所以这里把「需要管理员」收敛成一次 UAC + 30 天缓存：
# 缓存新鲜就直接用；缓存缺失或过期就当场发起一次 UAC 重新核对，用户只需点一下「是」。
function Test-IsAdmin {
    return ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Read-ElevatedVerifyCache {
    $p = (Get-SkillRoot) + '\config\elevated-verify-cache.json'
    if (-not (Test-Path -LiteralPath $p)) { return $null }
    try { return (Get-Content -LiteralPath $p -Raw -Encoding UTF8 | ConvertFrom-Json) } catch { return $null }
}

function Get-ElevatedCacheAge {
    param($Cache)
    if (-not $Cache -or -not $Cache.At) { return $null }
    try { return [math]::Round(((Get-Date) - [datetime]$Cache.At).TotalDays, 1) } catch { return $null }
}

# 返回 @{ IsAdmin; Cache; Age; Fresh; Refreshed; Skipped; Error }
# 只要 Fresh=$true，调用方就能拿 Cache 里的 WfpFound / WfpDetail / DesktopVerifyStatus 下结论。
function Test-ElevatedVerifyCacheBinding {
    param($Cache)
    if (-not $Cache) { return $false }
    $manifestPath = (Get-SkillRoot) + '\scripts\desktop\维护文件\应用过滤目标.json'
    if (-not (Test-Path -LiteralPath $manifestPath)) { return $false }
    try {
        $manifest = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
        $manifestHash = (Get-FileHash -LiteralPath $manifestPath -Algorithm SHA256).Hash
        $expectedKeys = @($manifest.Targets | ForEach-Object { [string]$_.V4; [string]$_.V6 } | ForEach-Object { $_.ToLowerInvariant() } | Sort-Object)
        $cachedKeys = @($Cache.WfpKeys | ForEach-Object { ([string]$_).ToLowerInvariant() } | Sort-Object)
        return (
            [string]$Cache.ManifestSHA256 -eq $manifestHash -and
            [string]$Cache.PkgVersion -eq [string]$manifest.PackageVersion -and
            [int]$Cache.WfpExpected -eq $expectedKeys.Count -and
            ($cachedKeys -join '|') -ceq ($expectedKeys -join '|')
        )
    }
    catch { return $false }
}

function Update-ElevatedVerifyCache {
    param([int]$MaxAgeDays = 30, [switch]$NoElevate, [int]$TimeoutSec = 300)
    $o = [ordered]@{
        IsAdmin = (Test-IsAdmin); Cache = $null; Age = $null
        Fresh = $false; Refreshed = $false; Skipped = ''; Error = ''
    }
    $o.Cache = Read-ElevatedVerifyCache
    $o.Age = Get-ElevatedCacheAge -Cache $o.Cache
    if ($o.Cache -and $o.Age -ne $null -and $o.Age -le $MaxAgeDays -and (Test-ElevatedVerifyCacheBinding -Cache $o.Cache)) {
        $o.Fresh = $true
        return [pscustomobject]$o
    }

    # 非交互式（被计划任务 / CI 调起）或用户明确要求不要弹窗时不提权，
    # 否则会在无人看守的会话里干等一个 UAC 对话框。
    if ($NoElevate) { $o.Skipped = '已按 -NoElevate 跳过提权'; return [pscustomobject]$o }
    if ($env:CLAUDE_SAFE_NO_ELEVATE) { $o.Skipped = '已按 CLAUDE_SAFE_NO_ELEVATE 环境变量跳过提权'; return [pscustomobject]$o }
    if (-not [Environment]::UserInteractive) { $o.Skipped = '当前不是交互式会话，跳过提权'; return [pscustomobject]$o }

    # -Refresh 自己会判断：已在管理员上下文就直接干活，否则起一次 UAC
    $s = (Get-ScriptsRoot) + '\checks\Request-ElevatedVerify.ps1'
    if (-not (Test-Path -LiteralPath $s)) { $o.Error = "缺少 $s"; return [pscustomobject]$o }
    $r = Invoke-ChildPwsh -ScriptPath $s -Arguments @('-Refresh', '-MaxAgeDays', "$MaxAgeDays") -TimeoutSec $TimeoutSec
    $o.Refreshed = $true
    $o.Cache = Read-ElevatedVerifyCache
    $o.Age = Get-ElevatedCacheAge -Cache $o.Cache
    if ($o.Cache -and $o.Age -ne $null -and $o.Age -le $MaxAgeDays -and (Test-ElevatedVerifyCacheBinding -Cache $o.Cache)) { $o.Fresh = $true }
    elseif ($r.ExitCode -eq 2) { $o.Error = '提权被取消' }
    elseif ($r.ExitCode -eq 3) { $o.Error = '找不到 PowerShell 7 的 pwsh.exe' }
    elseif ($o.Cache) { $o.Error = "提权核对跑完但缓存时间戳仍是旧的（exit $($r.ExitCode)）" }
    else { $o.Error = "提权核对没有产出缓存（exit $($r.ExitCode)）" }
    return [pscustomobject]$o
}

# ---------------------------------------------------------------- 检测站读数（唯一真源）
# ping0.cc 与 ip.net.coffee 的关键数字都由前端 JS 注入，而且页面自身的提示文字里
# 就含有同样的关键词（例如 title 里那段「IDC机房IP / 家庭宽带IP」说明）——
# 于是「在 DOM 上还是纯文本上匹配」本身就是配方的一部分。
# 这些配方实测过（详见 references\04-Ip信誉检测.md），集中放在这里，
# 保证第 7 层 / 第 8 层 / 独立一键总检 用的是同一套取值逻辑，
# 不会出现「总检说通过、编排器说 WARN」这种自相矛盾。
function Get-Ping0Reading {
    param([Parameter(Mandatory)][string]$Uri)
    $r = Invoke-HeadlessDomFetch -Uri $Uri `
        -TimeoutSec $script:Topology.Headless.TimeoutSec `
        -VirtualTimeBudgetMs $script:Topology.Headless.VirtualTimeMs `
        -Retry $script:Topology.Headless.Retry `
        -ProfileDir (Get-HeadlessProfileDir)
    $o = [ordered]@{
        Ok = $false; Error = $r.Error; Seconds = $r.Seconds; DomLength = 0
        Risk = ''; RiskLabel = ''; IpType = ''; Native = ''; Share = ''; Desc = ''
    }
    if (-not $r.Ok) { return [pscustomobject]$o }
    $dom = $r.Dom
    $txt = $r.Plain
    $o.Ok = $true
    $o.DomLength = $dom.Length
    # 风控值 / 等级标签：走 HTML 里的风险条（riskitem riskcurrent）。
    # 「风控值」三个字在 <title> 提示里也出现，所以纯文本兜底必须带「(说明?)」锚点。
    if ($dom -match '(?s)riskcurrent.{0,400}?<span class="value">\s*([^<]*)</span>') { $o.Risk = $Matches[1].Trim() }
    elseif ($txt -match '风控值 \(说明\?\)\s*(\d{1,3}%)') { $o.Risk = $Matches[1] }
    if ($dom -match '(?s)riskcurrent.{0,400}?<span class="lab">\s*([^<]*)</span>') { $o.RiskLabel = $Matches[1].Trim() }
    # IP 类型 / 原生 IP 只能在纯文本上取（DOM 上必然先命中 title 里的说明文字）
    if ($txt -match 'IP 类型 \(说明\?\)\s*(\S+(?: \S+)?)') { $o.IpType = $Matches[1].Trim() }
    if ($txt -match '原生 IP \(说明\?\)\s*(非原生 IP|广播 IP|原生 IP)') { $o.Native = $Matches[1] }
    if ($dom -match 'usecount="([^"]*)"') { $o.Share = $Matches[1].Trim() }
    elseif ($txt -match '共享人数 \(说明\?\)\s*(\d+\s*-\s*\d+(?:\s*\([^)]*\))?)') { $o.Share = $Matches[1].Trim() }
    $o.Desc = [regex]::Match($dom, '<meta name="description" content="([^"]*)"').Groups[1].Value
    return [pscustomobject]$o
}

function Get-CoffeeReading {
    param([Parameter(Mandatory)][string]$Uri)
    $r = Invoke-HeadlessDomFetch -Uri $Uri `
        -TimeoutSec $script:Topology.Headless.TimeoutSec `
        -VirtualTimeBudgetMs $script:Topology.Headless.VirtualTimeMs `
        -Retry $script:Topology.Headless.Retry `
        -ProfileDir (Get-HeadlessProfileDir)
    $o = [ordered]@{
        Ok = $false; Error = $r.Error; Seconds = $r.Seconds; DomLength = 0
        Egress = ''; Geo = ''; Trust = ''; TrustLabel = ''; Attr = ''
        Vpn = ''; Proxy = ''; Tor = ''; Crawler = ''; Abuse = ''; Region = ''
        OtherEgress = @{}
    }
    if (-not $r.Ok) { return [pscustomobject]$o }
    $txt = $r.Plain
    $o.Ok = $true
    $o.DomLength = $r.Dom.Length
    # 标签与值之间夹着 HTML 标签 → 只在去标签后的纯文本上匹配才稳。
    # 「Cloudflare 出口IP 23.x」也含「出口IP」三个字，必须带 Claude AI 前缀。
    if ($txt -match 'Claude AI出口IP\s*((?:\d{1,3}\.){3}\d{1,3})') { $o.Egress = $Matches[1] }
    if ($txt -match 'Claude AI出口IP\s*(?:\d{1,3}\.){3}\d{1,3}\s*(.+?)\s*Claude AI信任评分') { $o.Geo = $Matches[1].Trim() }
    if ($txt -match '信任评分\(Trust Score\)\s*(\d{1,3})') { $o.Trust = [int]$Matches[1] }
    if ($txt -match '信任评分\(Trust Score\)\s*\d{1,3}\s*(\S+)') { $o.TrustLabel = $Matches[1] }
    if ($txt -match 'IP 属性\s*(家庭住宅IP|住宅IP|机房IP|数据中心IP|IDC)') { $o.Attr = $Matches[1] }
    if ($txt -match 'VPN\s*(未检测到|检测到)') { $o.Vpn = $Matches[1] }
    if ($txt -match '代理 \(Proxy\)\s*(未检测到|检测到)') { $o.Proxy = $Matches[1] }
    if ($txt -match 'Tor\s*(未检测到|检测到)') { $o.Tor = $Matches[1] }
    if ($txt -match '机器人 \(Crawler\)\s*(是|否)') { $o.Crawler = $Matches[1] }
    if ($txt -match '滥用记录\s*(无记录|有记录)') { $o.Abuse = $Matches[1] }
    if ($txt -match 'Claude支持地区\s*(正常|异常|不支持)') { $o.Region = $Matches[1] }
    $other = @{}
    if ($txt -match '中国出口IPv4\s*((?:\d{1,3}\.){3}\d{1,3})') { $other['directV4'] = $Matches[1] }
    if ($txt -match 'Cloudflare 出口IP\s*((?:\d{1,3}\.){3}\d{1,3})') { $other['cloudflareEgress'] = $Matches[1] }
    if ($txt -match 'UDP 出口\s*((?:\d{1,3}\.){3}\d{1,3})') { $other['udpEgress'] = $Matches[1] }
    if ($txt -match 'DNS 出口\s*((?:\d{1,3}\.){3}\d{1,3})') { $other['dnsEgress'] = $Matches[1] }
    $o.OtherEgress = $other
    return [pscustomobject]$o
}

function Test-CoffeeClean {
    param($Reading)
    $sig = @()
    if ($Reading.Vpn) { $sig += "VPN $($Reading.Vpn)" }
    if ($Reading.Proxy) { $sig += "Proxy $($Reading.Proxy)" }
    if ($Reading.Tor) { $sig += "Tor $($Reading.Tor)" }
    if ($Reading.Crawler) { $sig += "Crawler $($Reading.Crawler)" }
    # 「未检测到」本身也含「检测到」三个字，必须先排除，否则干净的也会被判脏
    $dirty = @($sig | Where-Object { (($_ -match '检测到') -and ($_ -notmatch '未检测到')) -or ($_ -match 'Crawler 是') })
    return [pscustomobject]@{ Signals = $sig; Dirty = $dirty; Text = ($sig -join ' / ') }
}

# 「检测站自动读数」的自动判定（不替代人工网页验收）。
# 四个检测站里 iprisk.top 已在第 7 层自动取，ping0.cc / ip.net.coffee 这里真实渲染；
# 只剩 ippure.com 需要人眼 —— 它的数据接口要一个混淆 JS 现算的 x-t 签名头，无头拿不到。
# 判定原则：站点取不到数只报 WARN（不 FAIL），因为那多半是站点对我们自己高频访问的临时限流，
# 与「本机配置有没有问题」无关；但绝不把取不到数当成通过。
function Get-WebAcceptanceVerdict {
    param([string]$Ping0Ip = '')
    $TH = $script:Topology.Thresholds
    $bad = @()
    $bits = @()

    $p0Uri = 'https://www.ping0.cc/'
    if ($Ping0Ip) { $p0Uri = $p0Uri + '?ip=' + $Ping0Ip }
    $p0 = Get-Ping0Reading -Uri $p0Uri
    if (-not $p0.Ok) {
        $bad += "ping0.cc 未取到读数（$($p0.Error)）"
    }
    else {
        $num = -1
        if ($p0.Risk -match '(\d{1,3})') { $num = [int]$Matches[1] }
        $bits += "ping0 风控值 $($p0.Risk)"
        if ($p0.IpType) { $bits += $p0.IpType }
        if ($p0.Native) { $bits += $p0.Native }
        if ($p0.Share) { $bits += "共享 $($p0.Share)" }
        if ($num -ge $TH.Ping0RiskPass) { $bad += "ping0.cc 风控值 $($p0.Risk) 未过合格线 < $($TH.Ping0RiskPass)%" }
        elseif ($num -lt 0) { $bad += 'ping0.cc 渲染成功但没解析出风控值' }
        if ($p0.IpType -and $p0.IpType -notmatch '家庭宽带|住宅|家宽') { $bad += "ping0.cc IP 类型 = $($p0.IpType)（期望 $($TH.Ping0WantIpType)）" }
        if ($p0.Native -and $p0.Native -ne '原生 IP') { $bad += "ping0.cc $($p0.Native)" }
    }

    $cf = Get-CoffeeReading -Uri 'https://ip.net.coffee/claude/'
    if (-not $cf.Ok) {
        $bad += "ip.net.coffee 未取到读数（$($cf.Error)）"
    }
    else {
        $bits += "Trust $($cf.Trust)/100"
        if ($cf.Attr) { $bits += $cf.Attr }
        $sig = Test-CoffeeClean -Reading $cf
        if ($sig.Signals.Count -gt 0) { $bits += $sig.Text }
        if ($cf.Trust -eq '') { $bad += 'ip.net.coffee 渲染成功但没解析出 Trust Score' }
        elseif ($cf.Trust -lt $TH.CoffeeTrustPass) { $bad += "ip.net.coffee Trust Score $($cf.Trust) < $($TH.CoffeeTrustPass)" }
        if ($cf.Attr -and $cf.Attr -ne $TH.CoffeeWantAttr) { $bad += "ip.net.coffee IP 属性 = $($cf.Attr)（期望 $($TH.CoffeeWantAttr)）" }
        if ($sig.Dirty.Count -gt 0) { $bad += "ip.net.coffee 命中风险信号：$($sig.Dirty -join '、')" }
        if ($cf.Abuse -and $cf.Abuse -ne '无记录') { $bad += "ip.net.coffee 滥用记录 $($cf.Abuse)" }
    }

    $ev = ($bits -join ' · ')
    if ($bad.Count -gt 0) {
        return [pscustomobject]@{
            Status   = 'WARN'
            Evidence = "$ev  ← $($bad -join '；')（若为站点限流，隔一会儿再跑即可恢复；急的话用 -OpenBrowser 人眼复核）"
        }
    }
    return [pscustomobject]@{
        Status   = 'PASS'
        Evidence = "$ev  （ping0 / ip.net.coffee 无头渲染自动判定，iprisk 见第 7 层；仅 ippure.com 建议人眼）"
    }
}

function ConvertTo-HtmlTable {
    param([string]$Html)
    # 返回 @( @{ 键 = 值 } ... )，用于解析 iprisk 的「字段 | 值」表
    $dict = @{}
    foreach ($tb in [regex]::Matches($Html, '(?s)<table[^>]*>(.*?)</table>')) {
        foreach ($tr in [regex]::Matches($tb.Groups[1].Value, '(?s)<tr[^>]*>(.*?)</tr>')) {
            $cells = @()
            foreach ($td in [regex]::Matches($tr.Groups[1].Value, '(?s)<t[dh][^>]*>(.*?)</t[dh]>')) {
                $cells += (ConvertTo-PlainText $td.Groups[1].Value)
            }
            if ($cells.Count -ge 2 -and $cells[0] -and -not $dict.ContainsKey($cells[0])) {
                $dict[$cells[0]] = ($cells[1..($cells.Count - 1)] -join ' ')
            }
        }
    }
    return $dict
}

function Get-DictValue {
    param([hashtable]$Dict, [string[]]$KeyPatterns)
    foreach ($p in $KeyPatterns) {
        foreach ($k in $Dict.Keys) { if ($k -like $p) { return [string]$Dict[$k] } }
    }
    return ''
}

# ---------------------------------------------------------------- 出口探测
function Get-EgressIp {
    param(
        [ValidateSet('Tun', 'Code', 'Chrome', 'Desktop')][string]$Entry = 'Tun',
        [int]$TimeoutSec = 15
    )
    $a = @('-sS', '--max-time', "$TimeoutSec")
    switch ($Entry) {
        'Code' { $a += @('--proxy', "http://127.0.0.1:$($script:Ports.Mixed)") }
        'Chrome' { $a += @('--socks5-hostname', "127.0.0.1:$($script:Ports.ChromeSocks)") }
        'Desktop' { $a += @('--proxy', "http://127.0.0.1:$($script:Ports.DesktopHttp)") }
    }
    $a += 'https://api.ipify.org'
    try {
        $o = (& curl.exe @a 2>$null) -join ''
        if ($o -match '^\s*(\d{1,3}(?:\.\d{1,3}){3})\s*$') { return $Matches[1] }
    }
    catch { }
    return $null
}

function Get-PortListener {
    param([Parameter(Mandatory)][int]$Port)
    try {
        $c = Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction Stop | Select-Object -First 1
        if ($c) {
            $p = Get-Process -Id $c.OwningProcess -ErrorAction SilentlyContinue
            return [pscustomobject]@{ Listening = $true; Pid = $c.OwningProcess; Process = if ($p) { $p.ProcessName } else { '?' } }
        }
    }
    catch { }
    return [pscustomobject]@{ Listening = $false; Pid = 0; Process = '' }
}

# ---------------------------------------------------------------- 浏览器交接
function Open-HandoffPage {
    param(
        [Parameter(Mandatory)][string]$Url,
        [string]$UserDataDir = '',
        [switch]$WhatIf
    )
    if (-not $UserDataDir) {
        $UserDataDir = Get-AccountUserDataDir
        if (-not $UserDataDir) { $UserDataDir = Join-Path $env:LOCALAPPDATA 'ClaudeAccountBrowsers\Default\UserData' }
    }
    $chrome = Expand-TopologyPath $script:Topology.ChromeExe
    if (-not (Test-Path $chrome)) {
        Write-Host "  [SKIP] 找不到 Chrome：$chrome"
        return $false
    }
    # 注意：若专用 Chrome 已在运行，直接传 URL 会复用已有实例（正是我们要的）
    $a = @(
        "--user-data-dir=$UserDataDir"
        "--proxy-server=socks5://127.0.0.1:$($script:Ports.ChromeSocks)"
        '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost'
        '--lang=en-US'
        '--accept-lang=en-US,en'
        '--force-webrtc-ip-handling-policy=disable_non_proxied_udp'
        $Url
    )
    if ($WhatIf) { Write-Host ("  [WHATIF] " + $chrome + ' ' + ($a -join ' ')); return $true }
    Start-Process -FilePath $chrome -ArgumentList $a | Out-Null
    Start-Sleep -Milliseconds 500
    return $true
}

# ---------------------------------------------------------------- 子进程调用
# 直接用 `& $engine ... | Out-String` 会用宿主代码页解码子进程的 UTF-8 字节，
# 中文乱码、JSON 字符串被截断。统一走 Process + 显式 UTF-8。
$script:PwshExe = (Get-Process -Id $PID -ErrorAction SilentlyContinue).Path
if (-not $script:PwshExe -or $script:PwshExe -notlike '*pwsh.exe') {
    $script:PwshExe = Join-Path $env:ProgramFiles 'PowerShell\7\pwsh.exe'
}

function Invoke-ChildPwsh {
    param(
        [Parameter(Mandatory)][string]$ScriptPath,
        [string[]]$Arguments = @(),
        [int]$TimeoutSec = 300
    )
    $psi = [System.Diagnostics.ProcessStartInfo]::new()
    $psi.FileName = $script:PwshExe
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $false
    $psi.StandardOutputEncoding = [System.Text.UTF8Encoding]::new($false)
    foreach ($a in @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $ScriptPath) + $Arguments) {
        $psi.ArgumentList.Add($a)
    }
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $proc = [System.Diagnostics.Process]::Start($psi)
    $out = $proc.StandardOutput.ReadToEnd()
    if (-not $proc.WaitForExit($TimeoutSec * 1000)) { try { $proc.Kill($true) } catch {} }
    $sw.Stop()
    return [pscustomobject]@{ ExitCode = $proc.ExitCode; Stdout = $out; Seconds = [math]::Round($sw.Elapsed.TotalSeconds, 1) }
}

function Get-JsonBlock {
    param([string]$Text)
    if ([string]::IsNullOrWhiteSpace($Text)) { return $null }
    $m = [regex]::Match($Text, '(?s)===JSON-BEGIN===\r?\n(.*?)\r?\n===JSON-END===')
    $candidate = if ($m.Success) { $m.Groups[1].Value } else { $Text.Trim() }
    try { return ($candidate | ConvertFrom-Json) } catch { }
    # 兜底：老脚本（如 验证Clash账号专用通道.ps1）直接打裸 JSON，从文本里捞出最外层对象
    $s = $candidate.IndexOf('{'); $e = $candidate.LastIndexOf('}')
    if ($s -ge 0 -and $e -gt $s) {
        try { return ($candidate.Substring($s, $e - $s + 1) | ConvertFrom-Json) } catch { }
    }
    return $null
}

# ---------------------------------------------------------------- JSON 输出
function Write-JsonResult {
    param([Parameter(Mandatory)]$Payload)
    Write-Output '===JSON-BEGIN==='
    $Payload | ConvertTo-Json -Depth 8
    Write-Output '===JSON-END==='
}
