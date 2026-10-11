#requires -Version 7.0
<#
.SYNOPSIS
    Claude 安全出口链路 · 全层编排器（一键跑完 9 层检查）。

.DESCRIPTION
    按依赖顺序串起 checks\ 下的 9 个分层检查，把各自结果汇总成一张总表：
      1. Check-ProxyStack       代理配置（Clash 全局脚本 / TUN / 规则 / 端口 / 三入口）
      2. Check-ClaudeCodeConfig Claude Code settings.json
      3. Check-ChromeEntry      专用 Chrome 快捷方式
      4. Check-DesktopConfig    Claude Desktop 策略注册表 + 直连阻断
      5. Check-WindowsFallback  Windows 系统级兜底
      6. Check-PortConsistency  端口一致性（真源 vs 字面量 vs 实际）
      7. Check-IpReputation     出口 IP 信誉（三站）
      8. Check-ClaudeIp         Cloudflare trace 逐入口 + Claude 专向人工交接
      9. Check-SuiteIntegrity   套件自包含性（相对路径不断、无外部依赖、PS 版本守卫）

    每个子检查都以 -Json 方式运行，本脚本从 '===JSON-BEGIN===' 标记之间取回结构化结果。
    任一子检查有 FAIL 则整体 exit 1。

.PARAMETER Only
    只跑指定层（可多个）。层名见上面列表的短名：proxy / code / chrome / desktop / windows / ports / ip / claudeip / suite

.PARAMETER Fast
    跳过需要联网的四层（proxy 的三入口探测、ip、claudeip），只做本机配置核对（code / desktop / windows / ports / suite）。
    不弹 UAC、不写提权缓存；管理员实盘复核单独跑 scripts\checks\Request-ElevatedVerify.ps1 -Refresh。

.PARAMETER OpenBrowser
    把浏览器人工核验项直接打开到专用 Chrome。

.EXAMPLE
    pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1
    pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1 -Only proxy,desktop
    pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1 -Fast -Json
#>
[CmdletBinding()]
param(
    [string[]] $Only,
    [switch] $Fast,
    [switch] $Json,
    [switch] $OpenBrowser
)

$ErrorActionPreference = 'Continue'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
# -File 调用时数组参数会整串进来（"code,desktop"），这里拆开，让两种写法都可用
$Only = @($Only | ForEach-Object { "$_" -split ',' } | ForEach-Object { $_.Trim() } | Where-Object { $_ })

$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

$root = Get-SkillRoot

$layers = @(
    @{ Key = 'proxy'; Title = '代理配置'; File = 'checks\Check-ProxyStack.ps1'; Net = $true; Extra = @() }
    @{ Key = 'code'; Title = 'Claude Code 配置'; File = 'checks\Check-ClaudeCodeConfig.ps1'; Net = $false; Extra = @() }
    @{ Key = 'chrome'; Title = '专用 Chrome'; File = 'checks\Check-ChromeEntry.ps1'; Net = $true; Extra = @() }
    @{ Key = 'desktop'; Title = 'Claude Desktop'; File = 'checks\Check-DesktopConfig.ps1'; Net = $false; Extra = @() }
    @{ Key = 'windows'; Title = 'Windows 兜底'; File = 'checks\Check-WindowsFallback.ps1'; Net = $false; Extra = @() }
    @{ Key = 'ports'; Title = '端口一致性'; File = 'checks\Check-PortConsistency.ps1'; Net = $false; Extra = @() }
    @{ Key = 'ip'; Title = '出口 IP 信誉'; File = 'checks\Check-IpReputation.ps1'; Net = $true; Extra = @() }
    @{ Key = 'claudeip'; Title = 'Claude 专向 IP'; File = 'checks\Check-ClaudeIp.ps1'; Net = $true; Extra = @() }
    # 放最后：本层检查"套件自己是否还完整"（相对路径/外部依赖），不带网络，-Fast 也会跑
    @{ Key = 'suite'; Title = '套件自包含性'; File = 'checks\Check-SuiteIntegrity.ps1'; Net = $false; Extra = @() }
)

$selected = if ($Only) { @($layers | Where-Object { $Only -contains $_.Key }) } else { $layers }
if ($Fast) {
    $selected = @($selected | Where-Object { -not $_.Net })
    # 快查永不弹 UAC：子进程继承这个变量，缓存缺失、过期或不匹配时只报 WARN 并给出手动刷新命令。
    $env:CLAUDE_SAFE_NO_ELEVATE = '1'
}
if ($selected.Count -eq 0) { throw "没有匹配的层。可用层名：$(($layers.Key) -join ', ')" }

Write-Banner 'Claude 安全出口链路 · 全层编排检查' "共 $($selected.Count) 层；每层独立运行，失败不影响后续层"

$summary = [System.Collections.Generic.List[object]]::new()
$allRows = [System.Collections.Generic.List[object]]::new()

foreach ($layer in $selected) {
    $path = Join-Path $script:ScriptsRoot $layer.File
    Write-Host ''
    Write-Host ('=' * 78)
    Write-Host ("  {0}  ·  {1}" -f $layer.Title, $layer.File)
    Write-Host ('=' * 78)

    if (-not (Test-Path $path)) {
        Write-Host "  [FAIL] 缺少脚本：$path"
        $summary.Add([pscustomobject]@{ Layer = $layer.Key; Title = $layer.Title; Pass = 0; Warn = 0; Fail = 1; Manual = 0; ExitCode = 127; Seconds = 0 })
        continue
    }

    $childArgs = @('-Json')
    if ($layer.Key -eq 'claudeip' -and $OpenBrowser) { $childArgs += '-OpenBrowser' }

    $run = Invoke-ChildPwsh -ScriptPath $path -Arguments $childArgs
    $raw = $run.Stdout
    $code = $run.ExitCode

    # 从标记之间取回结构化结果；标记之外的人类可读输出直接透传
    $m = [regex]::Match($raw, '(?s)===JSON-BEGIN===\r?\n(.*?)\r?\n===JSON-END===')
    if ($m.Success) {
        $human = $raw.Substring(0, $m.Index).Trim()
        if ($human) { Write-Host $human }
        try {
            $payload = $m.Groups[1].Value | ConvertFrom-Json
            $rows = @($payload.results)
            foreach ($r in $rows) { $allRows.Add([pscustomobject]@{ Layer = $layer.Key; Status = $r.Status; Item = $r.Item; Evidence = $r.Evidence }) }
            $summary.Add([pscustomobject]@{
                    Layer  = $layer.Key
                    Title  = $layer.Title
                    Pass   = @($rows | Where-Object Status -eq 'PASS').Count
                    Warn   = @($rows | Where-Object Status -eq 'WARN').Count
                    Fail   = @($rows | Where-Object Status -eq 'FAIL').Count
                    Manual = @($rows | Where-Object Status -eq 'MANUAL').Count
                    ExitCode = $code
                    Seconds  = $run.Seconds
                })
        }
        catch {
            Write-Host "  [FAIL] 子检查 JSON 解析失败：$($_.Exception.Message)"
            $summary.Add([pscustomobject]@{ Layer = $layer.Key; Title = $layer.Title; Pass = 0; Warn = 0; Fail = 1; Manual = 0; ExitCode = $code; Seconds = $run.Seconds })
        }
    }
    else {
        Write-Host $raw.Trim()
        Write-Host '  [FAIL] 子检查没有输出 JSON 结果块'
        $summary.Add([pscustomobject]@{ Layer = $layer.Key; Title = $layer.Title; Pass = 0; Warn = 0; Fail = 1; Manual = 0; ExitCode = $code; Seconds = $run.Seconds })
    }
}

# ------------------------------------------------------------------ 汇总
Write-Host ''
Write-Host ('=' * 78)
Write-Host '  汇总'
Write-Host ('=' * 78)
Write-Host ''
Write-Host ("  {0,-18} {1,5} {2,5} {3,5} {4,7} {5,8}" -f '层', 'PASS', 'WARN', 'FAIL', 'MANUAL', '耗时(s)')
Write-Host ('  ' + ('-' * 60))
foreach ($s in $summary) {
    Write-Host ("  {0,-18} {1,5} {2,5} {3,5} {4,7} {5,8}" -f $s.Title, $s.Pass, $s.Warn, $s.Fail, $s.Manual, $s.Seconds)
}
Write-Host ('  ' + ('-' * 60))
Write-Host ("  {0,-18} {1,5} {2,5} {3,5} {4,7}" -f '合计',
    (@($summary | Measure-Object Pass -Sum).Sum), (@($summary | Measure-Object Warn -Sum).Sum),
    (@($summary | Measure-Object Fail -Sum).Sum), (@($summary | Measure-Object Manual -Sum).Sum))

$totalFail = @($summary | Measure-Object Fail -Sum).Sum
$totalWarn = @($summary | Measure-Object Warn -Sum).Sum
$totalManual = @($summary | Measure-Object Manual -Sum).Sum

Write-Host ''
if ($totalFail -gt 0) {
    Write-Host "结论：有 $totalFail 项不合格 —— 不要启动 Claude，先按上面的层逐条修。" -ForegroundColor Red
    Write-Host '      排查手册： references\02-验收与排查手册.md'
}
elseif ($totalWarn -gt 0) {
    Write-Host "结论：程序层无不合格项，但有 $totalWarn 项警告 —— 按层内提示逐条确认后再验收。" -ForegroundColor Yellow
}
else {
    Write-Host '结论：程序层全部通过。' -ForegroundColor Green
}
if ($totalManual -gt 0) {
    Write-Host "      另有 $totalManual 项需人工在浏览器 / 管理员终端里确认（不算失败，但没做完就不算验收）。"
}

$exitCode = if ($totalFail -gt 0) { 1 } else { 0 }

if ($Json) {
    Write-JsonResult @{
        script   = 'Invoke-ClaudeSafetyCheck.ps1'
        time     = (Get-Date -Format 'o')
        ok       = ($exitCode -eq 0)
        fast     = [bool]$Fast
        summary  = $summary
        fail     = $totalFail
        warn     = $totalWarn
        manual   = $totalManual
        results  = $allRows
    }
}
exit $exitCode
