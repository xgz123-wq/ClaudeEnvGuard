#requires -Version 7.0
# 三端更新编排器：一次调用走完「查版本 → Desktop 落后就下载并审核官方包 → 汇总 4 个受保护目标的指纹 → 给出一条一键更新命令」。
# -Plan 只查只下（不提权、不安装、不启动 Claude）；-Run 直接执行一键更新，全程只弹一次 UAC。
# 前置条件：没有名为 claude 的进程在跑，有则报 blocked_running 由用户退出后重跑。
# 安全边界：哈希与清单不同时必须带有效 Anthropic 签名才允许进入更新参数；不结束进程、不改策略、不启动 Claude。
[CmdletBinding()]
param([ValidateSet('Plan','Run')][string]$Action = 'Plan')

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSStyle.OutputRendering = 'PlainText'

$result = [ordered]@{
    At = (Get-Date -Format o); Action = $Action; Status = 'running'
    RunningProcesses = @(); Products = @(); ChangedTargets = @()
    Plan = $null; Command = $null; Maintenance = $null; Error = $null
}

function Quote-Literal { param([string]$Value) return ("'" + $Value.Replace("'", "''") + "'") }

# 子脚本用 pwsh 7 独立进程调用：脚本内部有 exit，直接调用会提前终止本编排器。
function Invoke-ChildJson {
    param([string]$FileName, [string[]]$Arguments = @())
    $raw = & (Join-Path $PSHOME 'pwsh.exe') -NoProfile -NonInteractive -File (Join-Path $PSScriptRoot $FileName) @Arguments 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw ($FileName + ' 失败：' + $raw.Trim()) }
    return ($raw | ConvertFrom-Json)
}

try {
    $running = @(Get-CimInstance Win32_Process -Filter "Name='claude.exe'" | Select-Object ProcessId, ExecutablePath)
    if ($running.Count) {
        $result.RunningProcesses = $running
        $result.Status = 'blocked_running'
        $result.Error = '有 claude 进程在运行；请退出 Claude Code（含 VS Code 插件会话）与 Claude Desktop 后重跑。'
        $result | ConvertTo-Json -Depth 8
        exit 1
    }

    $versions = Invoke-ChildJson '查询Claude最新版本.ps1'
    $result.Products = @($versions.Results | ForEach-Object {
        [ordered]@{ Product = $_.Product; Installed = $_.Installed; Latest = $_.Latest; Status = $_.Status }
    })

    $manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot '应用过滤目标.json') -Raw | ConvertFrom-Json

    # Desktop：地址里的版本先判落后，再下载并经官方签名审核；审核不过就不产出参数。
    $desktopBehind = @($versions.Results | Where-Object { $_.Product -eq 'Claude Desktop' -and $_.Status -eq 'update_available' }).Count -gt 0
    $package = $null
    if ($desktopBehind) {
        $package = Invoke-ChildJson '下载ClaudeDesktop官方包.ps1'
        if ($package.Status -ne 'downloaded') { throw ('Desktop 安装包未就绪：' + $package.Status) }
    }

    $hashes = @()
    $changed = @()
    $pluginIndex = -1
    for ($i = 0; $i -lt $manifest.Targets.Count; $i++) {
        $target = $manifest.Targets[$i]
        if ($target.Path -match '\\\.vscode\\extensions\\anthropic\.claude-code-') { $pluginIndex = $i }
        $actual = (Get-FileHash -LiteralPath $target.Path -Algorithm SHA256).Hash
        $hashes += $actual
        if ($actual -eq $target.SHA256) { continue }
        $signature = Get-AuthenticodeSignature -LiteralPath $target.Path
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O="?Anthropic, PBC"?(,|$)') {
            throw ('新文件缺少有效的 Anthropic 签名，拒绝进入更新参数：' + $target.Name)
        }
        $changed += [ordered]@{ Index = $i; Name = $target.Name; From = $target.SHA256; To = $actual }
    }

    $pluginNow = $versions.Results | Where-Object { $_.Product -like 'VS Code*' }
    $pluginMoved = [bool]($pluginNow -and ($pluginNow.ProtectedPathMatches -eq $false) -and $pluginIndex -ge 0)

    # 插件换目录时旧路径可能已不存在，且第 4 项必须用新目录程序的指纹。
    $pluginApproved = $null
    if ($pluginMoved) {
        $newPluginPath = [string]$pluginNow.CurrentExe
        if (-not (Test-Path -LiteralPath $newPluginPath)) { throw ('插件新路径不存在：' + $newPluginPath) }
        $pluginApproved = (Get-FileHash -LiteralPath $newPluginPath -Algorithm SHA256).Hash
        if ($pluginApproved -ne [string]$pluginNow.CurrentSHA256) { throw '插件新程序与查询脚本给出的指纹不一致。' }
        $signature = Get-AuthenticodeSignature -LiteralPath $newPluginPath
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O="?Anthropic, PBC"?(,|$)') {
            throw '插件新程序缺少有效的 Anthropic 签名，拒绝进入更新参数。'
        }
        $hashes[$pluginIndex] = $pluginApproved
    }

    $result.ChangedTargets = $changed
    $result.Plan = [ordered]@{
        DesktopBehind = $desktopBehind
        PackagePath = [string]$package.PackagePath
        PackageSHA256 = [string]$package.SHA256
        PluginMoved = $pluginMoved
        OldPluginPath = [string]($manifest.Targets[$pluginIndex].Path)
        NewPluginPath = if ($pluginMoved) { [string]$pluginNow.CurrentExe } else { $null }
        PluginApprovedSHA256 = $pluginApproved
        CliApprovedSHA256 = $hashes
    }

    if (-not $desktopBehind -and -not $changed.Count -and -not $pluginMoved) {
        $result.Status = 'up_to_date'
        $result | ConvertTo-Json -Depth 8
        exit 0
    }

    $command = '& ' + (Quote-Literal (Join-Path $PSScriptRoot '一键更新ClaudeDesktop.ps1')) + ' -Action Update'
    if ($desktopBehind) { $command += ' -PackagePath ' + (Quote-Literal $package.PackagePath) + ' -ApprovedSHA256 ' + (Quote-Literal $package.SHA256) }
    $command += ' -CliApprovedSHA256 ' + ((@($hashes) | ForEach-Object { Quote-Literal $_ }) -join ',')
    if ($pluginMoved) { $command += ' -PluginApprovedSHA256 ' + (Quote-Literal $pluginApproved) }
    $result.Command = $command

    if ($Action -eq 'Plan') {
        $result.Status = 'plan_ready'
        $result | ConvertTo-Json -Depth 8
        exit 0
    }

    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $output = & (Join-Path $PSHOME 'pwsh.exe') -NoProfile -EncodedCommand $encoded 2>&1 | Out-String
    $result.Maintenance = $output.Trim()
    $result.Status = if ($LASTEXITCODE -eq 0) { 'passed' } else { 'failed' }
    $result | ConvertTo-Json -Depth 8
    exit $LASTEXITCODE
}
catch {
    $result.Status = 'failed'
    $result.Error = $_.Exception.Message
    $result | ConvertTo-Json -Depth 8
    exit 1
}
