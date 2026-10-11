#requires -Version 7.0
<#
.SYNOPSIS
    第 6 层：端口一致性 / 漂移检测。

.DESCRIPTION
    本套件把端口、组名、阈值收敛到 config\Topology.psd1 作为单一真源，
    但历史遗留脚本里仍有硬编码字面量（保持不动的取舍见 SKILL.md）。
    本脚本就是那份取舍的「安全网」：
      1. 真源值 vs 全 skill 字面量出现位置（给出文件:行号清单）
      2. 真源值 vs 实际监听端口
      3. 真源值 vs 桌面快捷方式参数里的端口
      4. 真源值 vs Clash 运行时配置
    只要有人改了拓扑，这里会立刻点名「还有哪些文件没跟着改」。
    另外把这份清单直接输出成 SOP-3 可粘贴的表格。
#>
[CmdletBinding()]
param([switch] $Json)

$ErrorActionPreference = 'Continue'
$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

Reset-CheckResults
$root = Get-SkillRoot
Write-Banner '第 6 层 · 端口一致性检查' 'Topology 真源 vs 脚本字面量 vs 实际监听 vs 快捷方式'

$portList = @($Ports.Mixed, $Ports.ChromeSocks, $Ports.DesktopHttp)
$portAlt = ($portList -join '|')
$detail = [ordered]@{ truth = @{ mixed = $Ports.Mixed; chromeSocks = $Ports.ChromeSocks; desktopHttp = $Ports.DesktopHttp } }

Write-Section '真源（config\Topology.psd1）'
Add-CheckResult -Status 'PASS' -Item 'Topology' -Evidence "Mixed=$($Ports.Mixed)  ChromeSocks=$($Ports.ChromeSocks)  DesktopHttp=$($Ports.DesktopHttp)  组=$($Groups.Primary)"

# ------------------------------------------------- 1. 字面量扫描
Write-Section '端口字面量清单与漂移检测'
$scanExt = '*.ps1', '*.psm1', '*.js', '*.yaml', '*.yml', '*.json', '*.psd1', '*.cjs', '*.cs', '*.txt'
$skipName = '应用直连防护', '安装状态', '共用DNS修复状态', 'Topology.psd1', 'Check-PortConsistency.ps1'
$knownPorts = @($portList) + @($Ports.Controller) | Where-Object { $_ }
$hits = [System.Collections.Generic.List[object]]::new()
$suspects = [System.Collections.Generic.List[object]]::new()
$files = @(Get-ChildItem -LiteralPath $root -Recurse -File -Include $scanExt -ErrorAction SilentlyContinue |
        Where-Object { $n = $_.Name; -not ($skipName | Where-Object { $n -like "*$_*" }) })

# 疑似同族端口：438xx（本套件用）/ 789x、909x（Clash 老默认）/ 1080x（代理老默认）
$suspectRe = '(?<![0-9])(438[0-9]{2}|789[0-9]|909[0-9]|1080[0-9]?)(?![0-9])'
foreach ($f in $files) {
    $rel = $f.FullName.Substring($root.Length + 1)
    $n = 0
    foreach ($line in (Get-Content -LiteralPath $f.FullName -ErrorAction SilentlyContinue)) {
        $n++
        if ($line -match "(?<![0-9])($portAlt)(?![0-9])") {
            $hitPorts = @($portList | Where-Object { $line -match "(?<![0-9])$_(?![0-9])" })
            $hits.Add([pscustomobject]@{ File = $rel; Line = $n; Ports = ($hitPorts -join ','); Text = $line.Trim() })
        }
        elseif ($line -match $suspectRe) {
            $found = $Matches[1]
            if ($knownPorts -notcontains [int]$found) {
                $suspects.Add([pscustomobject]@{ File = $rel; Line = $n; Found = $found; Text = $line.Trim() })
            }
        }
    }
}

$byFile = @($hits | Group-Object File | Sort-Object Name)
if ($byFile.Count -eq 0) {
    Add-CheckResult -Status 'PASS' -Item '字面量' -Evidence '全 skill 已无端口字面量（完全参数化）'
}
else {
    Add-CheckResult -Status 'PASS' -Item '字面量清单' -Evidence "$($byFile.Count) 个文件、$($hits.Count) 行含真源端口 —— 这不是缺陷，是「改端口时按行同步」的清单（原文见 -Json 的 literalHits）"
    foreach ($g in $byFile) {
        $lines = @($g.Group | ForEach-Object { $_.Line })
        Write-Host ("        {0}  行 {1}" -f $g.Name, ($lines -join ','))
    }
}

if ($suspects.Count -gt 0) {
    Add-CheckResult -Status 'WARN' -Item '疑似遗留端口' -Evidence "$($suspects.Count) 行出现同族但不在真源里的端口号（可能改了拓扑没跟上，或只是老文档例句）"
    foreach ($s in $suspects) { Write-Host ("        {0}:{1}  {2}  |  {3}" -f $s.File, $s.Line, $s.Found, $s.Text) }
}
else {
    Add-CheckResult -Status 'PASS' -Item '疑似遗留端口' -Evidence '没有发现真源之外的同族端口号'
}
$detail['literalHits'] = $hits
$detail['suspectHits'] = $suspects

# ------------------------------------------------- 2. 实际监听
Write-Section '实际监听'
foreach ($p in $portList) {
    $l = Get-PortListener -Port $p
    Add-CheckResult -Status $(if ($l.Listening) { 'PASS' } else { 'FAIL' }) -Item "127.0.0.1:$p" `
        -Evidence $(if ($l.Listening) { "$($l.Process) (PID $($l.Pid))" } else { '未监听 —— 真源与实际不一致' })
}

# ------------------------------------------------- 3. 快捷方式
Write-Section '桌面快捷方式参数里的端口'
$desktop = [Environment]::GetFolderPath('Desktop')
$lnks = @(Get-ChildItem -LiteralPath $desktop -Filter '*.lnk' -ErrorAction SilentlyContinue | Where-Object { $_.Name -match 'Claude|KK-' })
$lnkHits = [System.Collections.Generic.List[object]]::new()
foreach ($l in $lnks) {
    $sh = New-Object -ComObject WScript.Shell
    $sc = $sh.CreateShortcut($l.FullName)
    $m = [regex]::Matches($sc.Arguments, "(?<![0-9])($portAlt)(?![0-9])")
    if ($m.Count -gt 0) {
        $lnkHits.Add([pscustomobject]@{ Shortcut = $l.Name; Ports = (@($m | ForEach-Object { $_.Value }) | Select-Object -Unique) -join ',' })
    }
}
if ($lnkHits.Count -gt 0) {
    $stray = @($lnkHits | Where-Object { @($_.Ports -split ',') | Where-Object { $knownPorts -notcontains [int]$_ } })
    if ($stray.Count -gt 0) {
        Add-CheckResult -Status 'FAIL' -Item '快捷方式端口' -Evidence "$($stray.Count) 个快捷方式里的端口不在真源里（改了端口就得手工同步这两处；图标被改坏可用 scripts\apply\Repair-Shortcuts.ps1 -Apply 还原）"
        foreach ($h in $stray) { Write-Host ("        {0}  →  {1}" -f $h.Shortcut, $h.Ports) }
    }
    else {
        Add-CheckResult -Status 'PASS' -Item '快捷方式端口' -Evidence "$($lnkHits.Count) 个快捷方式内嵌真源端口，取值正确"
        foreach ($h in $lnkHits) { Write-Host ("        {0}  →  {1}" -f $h.Shortcut, $h.Ports) }
    }
}
else {
    Add-CheckResult -Status 'PASS' -Item '快捷方式端口' -Evidence '桌面 Claude 相关快捷方式不含端口字面量'
}
$detail['shortcutHits'] = $lnkHits

# ------------------------------------------------- 4. Clash 运行时
Write-Section 'Clash 运行时端口'
$base = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$vergeYaml = Join-Path $base 'verge.yaml'
$runtimeYaml = Join-Path $base 'clash-verge.yaml'
$liveScript = Join-Path $base 'profiles\Script.js'

if (Test-Path $vergeYaml) {
    $mp = [regex]::Match((Get-Content -LiteralPath $vergeYaml -Raw), '(?m)^\s*verge_mixed_port:\s*(\d+)').Groups[1].Value
    Add-CheckResult -Status $(if ([int]$mp -eq $Ports.Mixed) { 'PASS' } else { 'FAIL' }) -Item 'verge.yaml mixed-port' -Evidence "$mp（真源 $($Ports.Mixed)）"
}
if (Test-Path $runtimeYaml) {
    $rt = Get-Content -LiteralPath $runtimeYaml -Raw
    $mp2 = [regex]::Match($rt, '(?m)^\s*mixed-port:\s*(\d+)').Groups[1].Value
    Add-CheckResult -Status $(if ([int]$mp2 -eq $Ports.Mixed) { 'PASS' } else { 'FAIL' }) -Item '运行时 mixed-port' -Evidence "$mp2（真源 $($Ports.Mixed)）"
    foreach ($pair in @(@{ L = 'ChromeSocks listener'; P = $Ports.ChromeSocks }, @{ L = 'DesktopHttp listener'; P = $Ports.DesktopHttp })) {
        $found = $rt -match "(?m)^\s*port:\s*$($pair.P)\s*$"
        Add-CheckResult -Status $(if ($found) { 'PASS' } else { 'FAIL' }) -Item $pair.L -Evidence "$($pair.P) $(if($found){'在位'}else{'未出现在运行时配置'})"
    }
}
if (Test-Path $liveScript) {
    # 全局扩展脚本只负责注入两个 listener（43866 / 43867）；43865 是 Clash 自己的混合端口，由 verge.yaml 管
    $needJs = @($Ports.ChromeSocks, $Ports.DesktopHttp)
    $js = Get-Content -LiteralPath $liveScript -Raw
    $bad = @($needJs | Where-Object { $js -notmatch "(?<![0-9])$_(?![0-9])" })
    Add-CheckResult -Status $(if ($bad.Count -eq 0) { 'PASS' } else { 'FAIL' }) -Item '已安装 Script.js' -Evidence $(if ($bad.Count -eq 0) { "两个 listener 端口都在位：$($needJs -join ' / ')" } else { "缺少端口：" + ($bad -join ',') })

    # 已安装脚本 vs skill 内权威副本
    $authoritative = Join-Path $root 'scripts\clash\Clash全局账号通道.js'
    if (Test-Path $authoritative) {
        $h1 = (Get-FileHash $liveScript -Algorithm SHA256).Hash
        $h2 = (Get-FileHash $authoritative -Algorithm SHA256).Hash
        Add-CheckResult -Status $(if ($h1 -eq $h2) { 'PASS' } else { 'WARN' }) -Item 'Script.js 与 skill 权威副本' `
            -Evidence $(if ($h1 -eq $h2) { "哈希一致 $($h1.Substring(0,12))…" } else { "不一致：已安装 $($h1.Substring(0,12))… vs 副本 $($h2.Substring(0,12))…；请由用户手动同步（更新Clash通用规则.ps1 会重启 Clash，默认拒绝运行）" })
    }
    else {
        Add-CheckResult -Status 'FAIL' -Item 'Script.js 权威副本' -Evidence "skill 内缺少：$authoritative"
    }
}

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows -FailHint '真源与实际不一致 —— 三入口中已有走不到 ACCOUNT-US 的。' -WarnHint '出现真源之外的同族端口号：确认是遗留残留还是无害文档例句；改端口后按「字面量清单」逐处同步。'

if ($Json) {
    Write-JsonResult @{ script = 'Check-PortConsistency.ps1'; ok = ($code -eq 0); detail = $detail; results = $rows }
}
exit $code
