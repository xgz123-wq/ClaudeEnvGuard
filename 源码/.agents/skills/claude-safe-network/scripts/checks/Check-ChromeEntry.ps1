#requires -Version 7.0
<#
.SYNOPSIS
    第 3 层：专用 Chrome 入口检查（桌面快捷方式 + 独立 User Data）。

.DESCRIPTION
    逐个解析桌面上所有 Claude 相关 .lnk，核对防泄漏参数是否齐全：
      独立 --user-data-dir / --proxy-server=socks5://127.0.0.1:<ChromeSocks>
      / --host-resolver-rules 远端 DNS / --lang=en-US / 禁 WebRTC 非代理 UDP

    脚本启动类快捷方式只核对「-File 指向的脚本是否存在」，并注明它属于原始基线还是 skill 内等价脚本。
    桌面图标是**用户资产**，本层不判定它「应该在哪个目录」—— 图标指回 docs\配置\ 老路径是正常状态。
#>
[CmdletBinding()]
param([switch] $Json, [switch] $Launch)

$ErrorActionPreference = 'Continue'
$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

Reset-CheckResults
$root = Get-SkillRoot
Write-Banner '第 3 层 · 专用 Chrome 入口检查' '桌面快捷方式参数 · 独立 User Data · 远端 DNS · 禁 WebRTC'

function Read-Lnk {
    param([string] $Path)
    if (-not (Test-Path -LiteralPath $Path)) { return $null }
    $shell = New-Object -ComObject WScript.Shell
    $sc = $shell.CreateShortcut($Path)
    return [pscustomobject]@{ Path = $Path; Name = [IO.Path]::GetFileName($Path); Target = [string]$sc.TargetPath; Arguments = [string]$sc.Arguments }
}

$desktop = [Environment]::GetFolderPath('Desktop')
$allShortcuts = @(Get-ChildItem -LiteralPath $desktop -Filter '*.lnk' -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -notlike '.*' } |
        ForEach-Object { Read-Lnk $_.FullName } | Where-Object { $_ })
$all = @($allShortcuts | Where-Object {
        $_.Name -match 'Claude|KK-' -or $_.Arguments -match "--proxy-server=.*127\.0\.0\.1:$($Ports.ChromeSocks)"
    })

$browserLnk = @($all | Where-Object { $_.Arguments -match '--proxy-server' })
$launcherLnk = @($all | Where-Object { $_.Arguments -notmatch '--proxy-server' })

Write-Section "专用浏览器快捷方式（$($browserLnk.Count) 个）"
$userDataDirs = @()
foreach ($lnk in $browserLnk) {
    $a = $lnk.Arguments
    $checks = [ordered]@{
        '独立 User Data' = ($a -match '--user-data-dir=')
        'SOCKS5 专用入口' = ($a -match "127\.0\.0\.1:$($Ports.ChromeSocks)")
        '远端 DNS 解析' = ($a -match '--host-resolver-rules=')
        '英文语言' = ($a -match '--lang=en-US')
        '禁 WebRTC 非代理 UDP' = ($a -match 'disable_non_proxied_udp')
    }
    $bad = @($checks.Keys | Where-Object { -not $checks[$_] })
    Add-CheckResult -Status $(if ($bad.Count -eq 0) { 'PASS' } else { 'FAIL' }) -Item $lnk.Name `
        -Evidence $(if ($bad.Count -eq 0) { '防旁路参数齐全' } else { '缺少：' + ($bad -join '、') })

    # 独立 UserData 目录必须真实存在，且各账号互不相同
    $m = [regex]::Match($a, '--user-data-dir="?([^"]+?)"?(?:\s|$)')
    if ($m.Success) {
        $ud = $m.Groups[1].Value.TrimEnd('\')
        $userDataDirs += $ud
        Add-CheckResult -Status $(if (Test-Path $ud) { 'PASS' } else { 'WARN' }) -Item "$($lnk.Name) 的 User Data" -Evidence $ud
    }
    # 必须打开 Claude 专向检测页（首屏即验 IP）
    Add-CheckResult -Status $(if ($a -match 'ip\.net\.coffee/claude') { 'PASS' } else { 'WARN' }) -Item "$($lnk.Name) 首页" -Evidence $(if ($a -match 'ip\.net\.coffee/claude') { 'ip.net.coffee/claude/（首屏验 IP）' } else { '未指向 Claude 专向检测页' })
}
if ($userDataDirs.Count -gt 1 -and @($userDataDirs | Select-Object -Unique).Count -ne $userDataDirs.Count) {
    Add-CheckResult -Status 'FAIL' -Item '账号隔离' -Evidence '多个快捷方式共用同一个 User Data —— 账号会串'
}
elseif ($userDataDirs.Count -gt 0) {
    Add-CheckResult -Status 'PASS' -Item '账号隔离' -Evidence "$($userDataDirs.Count) 个账号各自独立 User Data"
}

# 拓扑里登记的账号是否都有对应的桌面快捷方式
Write-Section '拓扑登记的账号'
foreach ($acc in @($Topology.Accounts)) {
    $expectedDir = Expand-TopologyPath $acc.UserDataDir
    $lnk = Join-Path $desktop $acc.Shortcut
    if (Test-Path -LiteralPath $lnk) {
        Add-CheckResult -Status 'PASS' -Item $acc.Name -Evidence "快捷方式在位；User Data 目录$(if (Test-Path $expectedDir) { '已存在' } else { '尚未创建（首次启动时自动生成）' })"
    }
    else {
        Add-CheckResult -Status 'WARN' -Item $acc.Name -Evidence "桌面缺少 $($acc.Shortcut)；可用 scripts\apply\Repair-Shortcuts.ps1 -Apply 从原版备份还原"
    }
}

Write-Section "脚本启动类快捷方式（$($launcherLnk.Count) 个）"
foreach ($lnk in $launcherLnk) {
    $scriptPath = [regex]::Match($lnk.Arguments, '-File\s+"?([^"]+\.ps1)"?').Groups[1].Value
    if (-not $scriptPath) {
        Add-CheckResult -Status 'WARN' -Item $lnk.Name -Evidence '参数里没有 -File <脚本> 形式的目标'
        continue
    }
    # 桌面图标是用户资产：只验「目标脚本存在」，不判定它该在哪个目录
    $decl = @($Topology.DesktopShortcuts | Where-Object { $_.Name -eq $lnk.Name })
    $note = ''
    if ($decl.Count -gt 0) {
        if ($scriptPath -like "$root*") { $note = '（skill 内等价脚本）' }
        elseif ($scriptPath.EndsWith([string]$decl[0].BaselineScript, [StringComparison]::OrdinalIgnoreCase)) { $note = '（原始基线）' }
        else { $note = '（非基线位置，人工确认即可）' }
    }
    if (Test-Path -LiteralPath $scriptPath) {
        Add-CheckResult -Status 'PASS' -Item $lnk.Name -Evidence "-File 目标存在$note：$scriptPath"
    }
    else {
        Add-CheckResult -Status 'FAIL' -Item $lnk.Name -Evidence "-File 目标不存在，图标已失效：$scriptPath"
    }
}
if ($launcherLnk.Count -eq 0) {
    Add-CheckResult -Status 'WARN' -Item '脚本启动类快捷方式' -Evidence '桌面没有找到；可用 scripts\apply\Repair-Shortcuts.ps1 -Apply 从原版备份还原'
}
# 基线登记了但桌面上没有的（只在缺失时提示，避免虚增通过项）
foreach ($decl in @($Topology.DesktopShortcuts)) {
    if (-not (Test-Path -LiteralPath (Join-Path $desktop $decl.Name))) {
        Add-CheckResult -Status 'WARN' -Item $decl.Name -Evidence '桌面缺少这个图标；可用 scripts\apply\Repair-Shortcuts.ps1 -Apply 从原版备份还原'
    }
}

Write-Section 'Clash 侧：43866 → ACCOUNT-US'
$claude = Get-EgressIp -Entry 'Chrome' -TimeoutSec 20
Add-CheckResult -Status $(if ($claude) { 'PASS' } else { 'FAIL' }) -Item "socks5 127.0.0.1:$($Ports.ChromeSocks) 出口" -Evidence $(if ($claude) { $claude } else { '取不到出口 IP' })

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows -FailHint '专用浏览器参数不全 = 可能真实 IP / DNS 泄漏，先修快捷方式。' -WarnHint '有提醒项：先看上面明细；图标被误改/误删可用 scripts\apply\Repair-Shortcuts.ps1 -Apply 从原版备份还原。'

if ($Launch -and $browserLnk.Count -gt 0) { Start-Process -FilePath $browserLnk[0].Path }

if ($Json) {
    Write-JsonResult @{ script = 'Check-ChromeEntry.ps1'; ok = ($code -eq 0); shortcuts = $all; results = $rows }
}
exit $code
