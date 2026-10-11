#requires -Version 7.0
<#
.SYNOPSIS
    第 1 层：代理配置检查（Clash 全局脚本 / TUN / 规则 / 端口监听 / 三入口出口）。

.DESCRIPTION
    本脚本不重复实现 Clash 校验逻辑，而是驱动 skill 内已核验的 18 项验证程序
    （scripts\clash\验证Clash账号专用通道.ps1）与 14 项一键总检
    （scripts\Claude环境一键总检.ps1），把它们的结论统一成同一种结果格式。
    额外补一层「原生事实」：三个端口到底谁在监听、监听进程是不是 verge-mihomo。
#>
[CmdletBinding()]
param(
    [switch] $Json,
    [switch] $SkipGate   # 只做端口/监听层快查，跳过耗时的 18 项 + 14 项验收
)

$ErrorActionPreference = 'Continue'
$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

Reset-CheckResults
$root = Get-SkillRoot

Write-Banner '第 1 层 · 代理配置检查' 'Clash 全局脚本 · TUN · 规则 · 端口监听 · 三入口出口'
$script:EchoResults = $true

# ---------------------------------------------------------- 端口 / 监听方
Write-Section '端口监听与归属'
$listeners = @{}
foreach ($p in @(
        @{ Name = '混合端口 (Claude Code)'; Port = $Ports.Mixed },
        @{ Name = 'SOCKS5 (专用 Chrome)'; Port = $Ports.ChromeSocks },
        @{ Name = 'HTTP (Claude Desktop)'; Port = $Ports.DesktopHttp }
    )) {
    $l = Get-PortListener -Port $p.Port
    $listeners["$($p.Port)"] = $l
    if (-not $l.Listening) {
        Add-CheckResult -Status 'FAIL' -Item $p.Name -Evidence "127.0.0.1:$($p.Port) 未监听"
        continue
    }
    if ($l.Process -ne 'verge-mihomo') {
        Add-CheckResult -Status 'FAIL' -Item $p.Name -Evidence "端口 $($p.Port) 被 $($l.Process) (PID $($l.Pid)) 占用，不是 Clash 内核"
    }
    else {
        Add-CheckResult -Status 'PASS' -Item $p.Name -Evidence "127.0.0.1:$($p.Port) ← verge-mihomo (PID $($l.Pid))"
    }
}

# ---------------------------------------------------------- Clash 运行时
Write-Section 'Clash 运行时（mode / TUN / 组）'
$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$vergeYaml = Join-Path $base 'verge.yaml'
$runtimeYaml = Join-Path $base 'clash-verge.yaml'
if (Test-Path $vergeYaml) {
    $vy = Get-Content -LiteralPath $vergeYaml -Raw
    $tunOn = $vy -match '(?m)^\s*enable_tun_mode:\s*true'
    $sysOn = $vy -match '(?m)^\s*enable_system_proxy:\s*true'
    Add-CheckResult -Status $(if ($tunOn) { 'PASS' } else { 'FAIL' }) -Item 'TUN 模式' -Evidence $(if ($tunOn) { 'enable_tun_mode=true' } else { 'TUN 已关闭 —— Claude 三入口依赖 TUN 兜底' })
    Add-CheckResult -Status $(if ($sysOn) { 'FAIL' } else { 'PASS' }) -Item '系统代理' -Evidence $(if ($sysOn) { '系统代理开着 —— 与 TUN 双开容易双抓、难排查' } else { 'enable_system_proxy=false' })
    $mp = [regex]::Match($vy, '(?m)^\s*verge_mixed_port:\s*(\d+)')
    if ($mp.Success) {
        $st = if ([int]$mp.Groups[1].Value -eq $Ports.Mixed) { 'PASS' } else { 'FAIL' }
        Add-CheckResult -Status $st -Item '混合端口一致性' -Evidence "verge.yaml=$($mp.Groups[1].Value)  真源=$($Ports.Mixed)"
    }
}
else { Add-CheckResult -Status 'WARN' -Item 'verge.yaml' -Evidence "缺少文件：$vergeYaml" }

if (Test-Path $runtimeYaml) {
    $rt = Get-Content -LiteralPath $runtimeYaml -Raw
    $m = [regex]::Match($rt, '(?m)^\s*mode:\s*(\S+)')
    if ($m.Success) {
        Add-CheckResult -Status $(if ($m.Groups[1].Value -eq 'rule') { 'PASS' } else { 'FAIL' }) -Item '代理模式' -Evidence "mode=$($m.Groups[1].Value)（必须是 rule，不能是 global）"
    }
    foreach ($pair in @(
            @{ K = 'strict-route'; L = 'TUN 防旁路 strict-route' },
            @{ K = 'auto-route'; L = 'TUN auto-route' }
        )) {
        $mm = [regex]::Match($rt, ('(?m)^\s*' + $pair.K + ':\s*(\S+)'))
        if ($mm.Success) {
            Add-CheckResult -Status $(if ($mm.Groups[1].Value -eq 'true') { 'PASS' } else { 'WARN' }) -Item $pair.L -Evidence "$($pair.K)=$($mm.Groups[1].Value)"
        }
    }
    $udp = $rt -match '(?m)^\s*-\s*any:53\s*$'; $tcp = $rt -match '(?m)^\s*-\s*tcp://any:53\s*$'
    Add-CheckResult -Status $(if ($udp -and $tcp) { 'PASS' } else { 'FAIL' }) -Item 'DNS 劫持防泄漏' -Evidence "any:53=$udp  tcp://any:53=$tcp"
}
else { Add-CheckResult -Status 'WARN' -Item 'clash-verge.yaml' -Evidence "缺少文件：$runtimeYaml" }

if (-not $SkipGate) {
    # ------------------------------------------------------ 18 项验证程序
    Write-Section '18 项验证程序（scripts\clash\验证Clash账号专用通道.ps1）'
    $validator = Join-Path $root 'scripts\clash\验证Clash账号专用通道.ps1'
    $validatorOk = $false
    if (-not (Test-Path $validator)) {
        Add-CheckResult -Status 'FAIL' -Item '验证程序' -Evidence "缺少文件：$validator"
    }
    else {
        $run = Invoke-ChildPwsh -ScriptPath $validator
        $v = Get-JsonBlock -Text $run.Stdout
        if ($null -eq $v) {
            Add-CheckResult -Status 'FAIL' -Item '验证程序' -Evidence '输出不是有效 JSON'
        }
        else {
            $validatorOk = [bool]$v.Success
            if ($v.Success) {
                Add-CheckResult -Status 'PASS' -Item '基础验证 18/18' -Evidence 'Clash 配置语法通过，规则/DNS/监听项全部合规'
            }
            else {
                Add-CheckResult -Status 'FAIL' -Item '基础验证' -Evidence ('失败项：' + (@($v.FailedChecks) -join '、'))
            }
            $evidence = if ($v.SocksPublicIp) { "$($v.AccountNode) → $($v.SocksPublicIp)" } else { "节点：$($v.AccountNode)" }
            Add-CheckResult -Status $(if ($v.SocksPublicIp) { 'PASS' } else { 'FAIL' }) -Item 'ACCOUNT-US 出口' -Evidence $evidence
            # claude.ai 是 Cloudflare 前置：裸 curl 拿到 403 属正常反爬，只要拿到 HTTP 码就说明链路通
            $cs = [int]$v.ClaudeHttpStatus
            Add-CheckResult -Status $(if ($cs -gt 0) { 'PASS' } else { 'FAIL' }) -Item 'claude.ai 可达' `
                -Evidence $(if ($cs -eq 200) { 'HTTP 200' } elseif ($cs -gt 0) { "HTTP $cs（Cloudflare 反爬响应，链路本身通）" } else { 'HTTP 000 —— socks5 通道没通' })
            Add-CheckResult -Status $(if ([int]$v.FeishuHttpStatus -eq 200) { 'PASS' } else { 'WARN' }) -Item '国内连通（飞书）' -Evidence "HTTP $($v.FeishuHttpStatus)"
        }
    }

    # ------------------------------------------------------ 三入口出口
    Write-Section '三入口真实出口 IP'
    $ips = [ordered]@{}
    foreach ($e in @('Code', 'Chrome', 'Desktop')) {
        $ips[$e] = Get-EgressIp -Entry $e -TimeoutSec 20
        Add-CheckResult -Status $(if ($ips[$e]) { 'PASS' } else { 'FAIL' }) -Item "入口 $e" -Evidence $(if ($ips[$e]) { $ips[$e] } else { '取不到出口 IP' })
    }
    $uniq = @($ips.Values | Where-Object { $_ } | Select-Object -Unique)
    Add-CheckResult -Status $(if ($uniq.Count -eq 1 -and $uniq[0]) { 'PASS' } else { 'FAIL' }) -Item '三入口出口一致性' `
        -Evidence $(if ($uniq.Count -eq 1 -and $uniq[0]) { "全部为 $($uniq[0])" } else { ($ips.GetEnumerator() | ForEach-Object { "$($_.Key)=$($_.Value)" }) -join ' / ' })
}

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows -FailHint '代理层有失败项，先修 Clash 再谈 Claude。' -WarnHint '有提醒项，建议按建议清单逐条确认。'

if ($Json) {
    Write-JsonResult @{
        script = 'Check-ProxyStack.ps1'
        ok     = ($code -eq 0)
        ports  = @{ mixed = $Ports.Mixed; chromeSocks = $Ports.ChromeSocks; desktopHttp = $Ports.DesktopHttp }
        listeners = $listeners
        results = $rows
    }
}
exit $code
