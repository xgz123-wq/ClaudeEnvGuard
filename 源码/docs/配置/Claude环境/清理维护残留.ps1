#requires -Version 7.0
# 清理 ClaudeNetworkGuard 下的过期维护残留：改前备份、更新日志目录、遗留安装包。
# 默认只列清单不删除；加 -Yes 才删除。每次维护收尾跑一次 dry-run，经用户确认后再加 -Yes。
# 规则：名称带 yyyyMMdd 日期且距今 >= KeepDays 天的目录/文件删除；
#       受控更新-*、统一维护-*、基线刷新备份-* 每个位置各保留最新一个，作为最近一次更新的证据；
#       claude-network-probe.exe 和不带日期的项目永不处理。
[CmdletBinding()]
param(
    [int]$KeepDays = 7,
    [switch]$Yes
)
$ErrorActionPreference = 'Stop'
$today = (Get-Date).Date
# Codex 等 MSIX 打包应用写 %LOCALAPPDATA% 会被重定向到 Packages\<包>\LocalCache\Local，两处都要清。
$roots = @(Join-Path $env:LOCALAPPDATA 'ClaudeNetworkGuard') + @(Get-ChildItem -Path (Join-Path $env:LOCALAPPDATA 'Packages\*\LocalCache\Local\ClaudeNetworkGuard') -Directory -ErrorAction SilentlyContinue | ForEach-Object FullName)
$keepLatest = @('受控更新','统一维护','基线刷新备份')
$candidates = @()
foreach ($root in @($roots | Where-Object { Test-Path -LiteralPath $_ })) {
    $items = @(Get-ChildItem -LiteralPath $root -Force | Where-Object { $_.Name -ne 'claude-network-probe.exe' } | ForEach-Object {
        $m = [regex]::Match($_.Name, '^(?<group>.+?)-(?<date>20\d{6})')
        if ($m.Success) { [pscustomobject]@{Item=$_;Group=$m.Groups['group'].Value;Date=[datetime]::ParseExact($m.Groups['date'].Value,'yyyyMMdd',$null)} }
    })
    $protected = @(foreach ($group in $keepLatest) { $items | Where-Object Group -eq $group | Sort-Object { $_.Item.Name } -Descending | Select-Object -First 1 | ForEach-Object { $_.Item.FullName } })
    foreach ($entry in $items) {
        if ($entry.Item.FullName -in $protected -or ($today - $entry.Date).Days -lt $KeepDays) { continue }
        $files = if ($entry.Item.PSIsContainer) { @(Get-ChildItem -LiteralPath $entry.Item.FullName -Recurse -File -Force) } else { @($entry.Item) }
        $candidates += [pscustomobject]@{Path=$entry.Item.FullName;Files=$files.Count;KB=[math]::Round(($files | Measure-Object Length -Sum).Sum/1KB)}
    }
}
# 更新失败后遗留的安装包：超过保留天数仍在，说明不会再续接。
foreach ($directory in @((Join-Path $env:ProgramData 'ClaudeNetworkGuard')) + @($roots | ForEach-Object { Join-Path $_ 'Desktop安装包下载' })) {
    if (-not (Test-Path -LiteralPath $directory)) { continue }
    Get-ChildItem -LiteralPath $directory -File -Filter 'Claude-*.msix' | Where-Object { ($today - $_.LastWriteTime.Date).Days -ge $KeepDays } | ForEach-Object {
        $candidates += [pscustomobject]@{Path=$_.FullName;Files=1;KB=[math]::Round($_.Length/1KB)}
    }
}
# 在 Codex 等 MSIX 应用里，普通根是合并视图，会把 Packages 下同一目录再列一次；按“去掉根目录后的相对路径”去重。
$candidates = @($candidates | Group-Object {
    $key = $_.Path
    foreach ($root in $roots) { if ($key.StartsWith($root + '\', [StringComparison]::OrdinalIgnoreCase)) { $key = '<root>' + $key.Substring($root.Length); break } }
    $key
} | ForEach-Object { $_.Group[0] })
$report = [ordered]@{At=(Get-Date -Format o);Mode=$(if($Yes){'delete'}else{'dry-run'});KeepDays=$KeepDays;Count=$candidates.Count;TotalMB=[math]::Round((($candidates | Measure-Object KB -Sum).Sum)/1KB,1);Items=$candidates;Errors=@()}
if ($Yes) {
    foreach ($candidate in $candidates) {
        if (-not (Test-Path -LiteralPath $candidate.Path)) { continue }
        try { Remove-Item -LiteralPath $candidate.Path -Recurse -Force }
        catch { $report.Errors += ($candidate.Path + '：' + $_.Exception.Message) }
    }
    $logDirectory = Join-Path $env:LOCALAPPDATA 'ClaudeNetworkGuard'
    $null = New-Item -ItemType Directory -Path $logDirectory -Force
    $report | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $logDirectory ('清理维护残留-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.json')) -Encoding utf8
}
$report | ConvertTo-Json -Depth 6
if ($report.Errors.Count) { exit 1 }
