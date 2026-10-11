#requires -Version 7.0
<#
.SYNOPSIS
    校验 / 还原桌面 Claude 相关快捷方式到「原始基线」。

.DESCRIPTION
    桌面快捷方式是**用户资产**：本套件不接管它、也不把图标改指到 skill 内部。
    只做一件事 —— 拿 assets\shortcuts\ 里的原版逐字节备份去比对桌面，不一致或缺失时可选还原。

    因此本脚本的方向是「还原」，不是「重建」：
      · 桌面图标本来就该指向用户自己的 docs\配置\ 路径，那是它的原始状态；
      · 只有当图标被误改、误删，或者你想强制回到已知良好状态时，才需要跑 -Apply。

    默认只报告（不改任何文件），必须显式加 -Apply 才写回桌面。
    写回方式是逐字节复制（Copy-Item -Force），保证 SHA256 与备份完全一致。

    · 只读校验： pwsh -File scripts\apply\Repair-Shortcuts.ps1
    · 还原基线： pwsh -File scripts\apply\Repair-Shortcuts.ps1 -Apply

    想跑 skill 内「修正版」的检查脚本时，不要去改桌面图标，直接命令行调用即可，例如：
      pwsh -File scripts\Claude环境一键总检.ps1 -OpenBrowser
      pwsh -File scripts\clash\Claude日常五项自检.ps1
      pwsh -File scripts\desktop\启动Claude英文浏览器.ps1
    （等价关系登记在 config\Topology.psd1 的 DesktopShortcuts 节。）
#>
[CmdletBinding()]
param(
    [switch] $Apply,
    [switch] $Json
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

$root = Get-SkillRoot
$desktop = [Environment]::GetFolderPath('Desktop')
$baselineDir = Join-Path $root 'assets\shortcuts'

if (-not (Test-Path -LiteralPath $baselineDir)) { throw "缺少原版备份目录：$baselineDir" }

$baselineFiles = @(Get-ChildItem -LiteralPath $baselineDir -Filter '*.lnk' -File | Sort-Object Name)
if ($baselineFiles.Count -eq 0) { throw "原版备份目录里没有 .lnk：$baselineDir" }

$items = [System.Collections.Generic.List[object]]::new()
foreach ($f in $baselineFiles) {
    $dst = Join-Path $desktop $f.Name
    $baseHash = (Get-FileHash -LiteralPath $f.FullName -Algorithm SHA256).Hash
    $curHash = if (Test-Path -LiteralPath $dst) { (Get-FileHash -LiteralPath $dst -Algorithm SHA256).Hash } else { $null }
    $state = if (-not $curHash) { 'missing' } elseif ($curHash -eq $baseHash) { 'same' } else { 'drift' }
    $items.Add([pscustomobject]@{
            Name         = $f.Name
            DesktopPath  = $dst
            BaselineHash = $baseHash
            CurrentHash  = $curHash
            State        = $state
            Action       = switch ($state) {
                'same' { '无' }
                'drift' { '从备份还原（当前内容与基线不一致）' }
                default { '从备份新建（桌面缺失）' }
            }
        })
}

Write-Banner $(if ($Apply) { '还原桌面快捷方式基线（写入模式）' } else { '还原桌面快捷方式基线（预览模式）' }) $(if ($Apply) { '正在从 assets\shortcuts 逐字节写回桌面' } else { '只报告，不改任何文件；加 -Apply 才写入' })

$need = @($items | Where-Object { $_.State -ne 'same' })

Write-Section "原始基线（assets\shortcuts，$($items.Count) 个）"
foreach ($i in $items) {
    Write-Host ''
    Write-Host ("  {0} {1}" -f $(if ($i.State -eq 'same') { '[PASS]' } else { '[WARN]' }), $i.Name)
    Write-Host ("        基线 SHA256：{0}" -f $i.BaselineHash.Substring(0, 16))
    Write-Host ("        桌面 SHA256：{0}" -f $(if ($i.CurrentHash) { $i.CurrentHash.Substring(0, 16) } else { '（桌面无此文件）' }))
    if ($i.State -ne 'same') { Write-Host ("        待办：{0}" -f $i.Action) }
}

# 桌面上的 Claude 相关 .lnk 里，没有基线备份的那些（例如后续新建的账号浏览器）—— 只提示，不算问题
$extra = @(Get-ChildItem -LiteralPath $desktop -Filter '*.lnk' -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -notlike '.*' -and $_.Name -match 'Claude|KK-' } |
        Where-Object { $items.Name -notcontains $_.Name })
if ($extra.Count -gt 0) {
    Write-Section "未纳入基线的 Claude 相关快捷方式（$($extra.Count) 个，提示项）"
    foreach ($e in $extra) { Write-Host ("  [INFO] {0}（用户自建 / 后续新增，本套件不管）" -f $e.Name) }
}

Write-Host ''
if ($need.Count -eq 0) {
    Write-Host "结论：$($items.Count) 个快捷方式全部与原始基线一致，无需改动。"
    Write-Host '      桌面图标保持指向用户自己的配置路径 —— 这是预期状态。'
}
elseif (-not $Apply) {
    Write-Host "结论：$($need.Count)/$($items.Count) 个快捷方式偏离原始基线（预览模式，未写入）。"
    Write-Host '      "偏离" 可能只是你手工调整过，不一定有问题；'
    Write-Host '      确认要回到已知良好状态后再运行： pwsh -File scripts\apply\Repair-Shortcuts.ps1 -Apply'
}
else {
    $ok = 0; $fail = 0
    foreach ($i in $need) {
        try {
            Copy-Item -LiteralPath (Join-Path $baselineDir $i.Name) -Destination $i.DesktopPath -Force
            $newHash = (Get-FileHash -LiteralPath $i.DesktopPath -Algorithm SHA256).Hash
            if ($newHash -ne $i.BaselineHash) { throw '还原后哈希仍不匹配' }
            $ok++
            Write-Host ("  [OK]   {0}" -f $i.Name)
        }
        catch {
            $fail++
            Write-Host ("  [FAIL] {0} — {1}" -f $i.Name, $_.Exception.Message)
        }
    }
    Write-Host ''
    Write-Host "结论：还原完成 $ok 个，失败 $fail 个。"
    Write-Host '      建议随后运行： pwsh -File scripts\checks\Check-ChromeEntry.ps1 复核入口参数。'
}

if ($Json) {
    Write-JsonResult @{
        script   = 'Repair-Shortcuts.ps1'
        applied  = [bool]$Apply
        baseline = $baselineDir
        items    = $items
        extra    = @($extra | ForEach-Object { $_.Name })
    }
}
exit 0
