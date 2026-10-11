#requires -Version 7.0
<#
.SYNOPSIS
    给 .ps1 加一个「用 PowerShell 7 运行」的右键菜单入口（只写 HKCU，可随时撤销）。

.DESCRIPTION
    先把话说清楚 —— 这里没有「冲突」，也没有「系统开关」可调：

      · powershell.exe = 系统自带的 Windows PowerShell 5.1，装在 C:\WINDOWS\System32\WindowsPowerShell\v1.0\
      · pwsh.exe       = 你后来装的 PowerShell 7，装在 C:\Program Files\PowerShell\7\
      两个名字不同、目录不同、PATH 里各只命中一个，**不存在互相抢调用**。
      5.1 也不能卸载：它是 Windows 系统组件，且系统自带的两个计划任务
      （UninstallSMB1ClientTask / UninstallSMB1ServerTask）本身就依赖它。

    真正会咬人的只有一件事：5.1 按 ANSI/GBK 解析「无 BOM 的 UTF-8 中文脚本」，
    跑本套件里带中文的脚本会乱码、甚至直接语法报错。
    所以本套件的 27 个 .ps1 现在全部写了 #requires -Version 7.0 ——
    万一被 5.1 跑到，它会明确报「此脚本需要 PowerShell 7.0 或更高版本」，而不是丢一堆乱码给你。

    最后剩下的缺口是**手工**跑脚本：Windows 右键菜单里的「使用 PowerShell 运行」走的是 5.1。
    本脚本补一个独立的、名字一眼能认出的右键项：「用 PowerShell 7 运行」。

    刻意不做的事（每条都有原因）：
      · 不卸载、不替换 PowerShell 5.1 —— 系统组件，卸载会连带弄坏系统任务；
      · 不把 powershell.exe 别名/替换成 pwsh 7 —— 会弄坏系统工具和第三方安装器；
      · 不改 .ps1 的双击默认关联 —— HKCR 的 UserChoice 带 Hash 保护，硬改会把关联弄坏，
        而且 Windows 11 故意把双击默认成「用记事本打开」是安全设计，不该动；
      · 只写 HKCU，不写 HKLM —— 不需要管理员、不影响其他用户、随时可撤干净。

    默认只报告；-Apply 才写入；-Rollback 完整撤销。

    · 只看状态： pwsh -File scripts\apply\启用-PowerShell7入口.ps1
    · 启用：     pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Apply
    · 撤销：     pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Rollback

.NOTES
    全部改动只落在 HKCU\Software\Classes\SystemFileAssociations\.ps1\shell\RunWithPwsh7。
    回滚 = 删掉这一个键，无残留。
#>
[CmdletBinding()]
param(
    [switch] $Apply,
    [switch] $Rollback,
    [switch] $Json
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

# ---- 目标位置（唯一写入点）------------------------------------------------
$leafRoot = 'HKCU:\Software\Classes\SystemFileAssociations\.ps1'
$verbRoot = $leafRoot + '\shell\RunWithPwsh7'
$cmdRoot = $verbRoot + '\Command'

# ---- 期望值 ---------------------------------------------------------------
$pwshExe = (Get-Process -Id $PID).Path
if (-not $pwshExe -or $pwshExe -notmatch 'pwsh\.exe$') {
    $pwshExe = "$env:ProgramFiles\PowerShell\7\pwsh.exe"
}
if (-not (Test-Path -LiteralPath $pwshExe)) { throw "找不到 pwsh 7：$pwshExe" }

$menuText = '用 PowerShell 7 运行(&R)'
$expectedCmd = '"{0}" -NoLogo -NoExit -ExecutionPolicy Bypass -File "%1"' -f $pwshExe

# ---- 读当前状态 -----------------------------------------------------------
function Get-VerbState {
    if (-not (Test-Path -LiteralPath $cmdRoot)) {
        return [pscustomobject]@{ State = 'missing'; CurrentCmd = $null; CurrentText = $null }
    }
    $k = Get-Item -LiteralPath $verbRoot
    $text = $k.GetValue('')
    $cmd = (Get-Item -LiteralPath $cmdRoot).GetValue('')
    $state = if ($cmd -eq $expectedCmd) { 'ok' } else { 'drift' }
    return [pscustomobject]@{ State = $state; CurrentCmd = $cmd; CurrentText = $text }
}

function Remove-EmptyKey($path) {
    if (-not (Test-Path -LiteralPath $path)) { return }
    $k = Get-Item -LiteralPath $path
    if ($k.SubKeyCount -eq 0 -and $k.ValueCount -eq 0) { Remove-Item -LiteralPath $path -Force }
}

$before = Get-VerbState

# ---- 动作 -----------------------------------------------------------------
$mode = if ($Rollback) { 'rollback' } elseif ($Apply) { 'apply' } else { 'report' }

Write-Banner $(switch ($mode) {
        'apply' { '启用「用 PowerShell 7 运行」右键入口（写入模式）' }
        'rollback' { '撤销「用 PowerShell 7 运行」右键入口（回滚模式）' }
        default { '启用「用 PowerShell 7 运行」右键入口（预览模式）' }
    }) $(switch ($mode) {
        'apply' { '只写 HKCU，不需要管理员；随时可 -Rollback' }
        'rollback' { '删除本脚本写入的键，无其它改动' }
        default { '只报告，不写任何东西；加 -Apply 才写入' }
    })

Write-Section '当前状态'
Write-Host ''
Write-Host ("  写入位置：{0}" -f $verbRoot)
Write-Host ("  目标引擎：{0}" -f $pwshExe)
Write-Host ("  （当前进程引擎：PowerShell {0}，{1}）" -f $PSVersionTable.PSVersion, $PSVersionTable.PSEdition)
Write-Host ''
switch ($before.State) {
    'ok' { Write-Host '  [PASS]  右键入口已就位，命令与期望一致。' }
    'drift' {
        Write-Host '  [WARN]  右键入口存在，但命令与期望不一致：'
        Write-Host ("          现在：{0}" -f $before.CurrentCmd)
        Write-Host ("          期望：{0}" -f $expectedCmd)
    }
    default { Write-Host '  [WARN]  右键入口尚未注册（属可选加固，不影响主链路）。' }
}

$applied = $false
$rolledBack = $false
$ok = $true

if ($mode -eq 'apply') {
    Write-Section '写入注册表（仅 HKCU）'
    try {
        New-Item -Path $cmdRoot -Force | Out-Null
        Set-ItemProperty -LiteralPath $verbRoot -Name '(default)' -Value $menuText
        Set-ItemProperty -LiteralPath $verbRoot -Name 'Icon' -Value $pwshExe
        Set-ItemProperty -LiteralPath $cmdRoot -Name '(default)' -Value $expectedCmd
        $after = Get-VerbState
        if ($after.State -ne 'ok') { throw "写入后复核不一致：$($after.CurrentCmd)" }
        $applied = $true
        Write-Host ''
        Write-Host '  [OK]  已写入并复核通过。'
    }
    catch {
        $ok = $false
        Write-Host ''
        Write-Host ("  [FAIL] 写入失败：{0}" -f $_.Exception.Message)
    }
}
elseif ($mode -eq 'rollback') {
    Write-Section '撤销注册表（仅删本脚本写入的键）'
    try {
        if (Test-Path -LiteralPath $verbRoot) { Remove-Item -LiteralPath $verbRoot -Recurse -Force }
        Remove-EmptyKey ($leafRoot + '\shell')
        Remove-EmptyKey $leafRoot
        $after = Get-VerbState
        if ($after.State -ne 'missing') { throw '删除后仍能读到该键' }
        $rolledBack = $true
        Write-Host ''
        Write-Host '  [OK]  已撤销，注册表无残留。'
    }
    catch {
        $ok = $false
        Write-Host ''
        Write-Host ("  [FAIL] 撤销失败：{0}" -f $_.Exception.Message)
    }
}

# ---- 结论 -----------------------------------------------------------------
Write-Host ''
if ($mode -eq 'report') {
    if ($before.State -eq 'ok') {
        Write-Host '结论：右键入口已启用，手工跑脚本时选「用 PowerShell 7 运行」即可，无需改动。'
    }
    else {
        Write-Host '结论：右键入口未就位（预览模式，未写入）。'
        Write-Host '      这套改动是可选的加固，主链路（桌面 3 个图标 + 套件自身）本来就走 pwsh 7，不启用也不影响使用。'
        Write-Host '      想启用： pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Apply'
    }
}
elseif ($mode -eq 'apply' -and $applied) {
    Write-Host '结论：已完成。'
    Write-Host '      请你手工确认一次（这个我没法在无界面下替你验证）：'
    Write-Host '        在任意 .ps1 文件上点右键 → 菜单里应能看到「用 PowerShell 7 运行(&R)」。'
    Write-Host '        Win11 若默认折叠菜单，点「显示更多选项」即可看到。'
    Write-Host '      撤销： pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Rollback'
}
elseif ($mode -eq 'rollback' -and $rolledBack) {
    Write-Host '结论：已回到未启用状态，注册表干净。'
    Write-Host '      系统自带的「使用 PowerShell 运行」（5.1）不受影响，仍是原样。'
}
elseif (-not $ok) {
    Write-Host '结论：操作未完成，请按上面的 [FAIL] 行排查。'
}

if ($Json) {
    Write-JsonResult @{
        script       = '启用-PowerShell7入口.ps1'
        mode         = $mode
        verbRoot     = $verbRoot
        pwshExe      = $pwshExe
        expectedCmd  = $expectedCmd
        before       = $before.State
        applied      = $applied
        rolledBack   = $rolledBack
        ok           = $ok
        engine       = "PowerShell $($PSVersionTable.PSVersion)"
    }
}

if (-not $ok) { exit 1 }
exit 0
