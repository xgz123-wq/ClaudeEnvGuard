#requires -Version 7.0
[CmdletBinding()]
param(
    [switch]$CheckOnly
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

$expectedProxy = 'http://127.0.0.1:43867'
$expectedPort = 43867
$guardDirectory = Join-Path $PSScriptRoot '维护文件'
$guardManifestPath = Join-Path $guardDirectory '应用过滤目标.json'
$guardStatePath = Join-Path $guardDirectory '应用直连防护状态.json'
$guardVerifyPath = Join-Path $guardDirectory '应用直连防护-Verify.json'
$guardInstallPath = Join-Path $guardDirectory '应用直连防护-最近操作.json'
$configRoot = Join-Path $env:LOCALAPPDATA 'Claude-3p\configLibrary'
$clashRuntime = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev\clash-verge.yaml'

function Write-Pass {
    param([string]$Message)
    Write-Host ('[PASS] ' + $Message) -ForegroundColor Green
}

function Get-EffectiveClaudeProxy {
    $machinePolicy = Get-ItemProperty -Path 'HKLM:\SOFTWARE\Policies\Claude' -ErrorAction SilentlyContinue
    $userPolicy = Get-ItemProperty -Path 'HKCU:\SOFTWARE\Policies\Claude' -ErrorAction SilentlyContinue
    $machineManaged = ($null -ne $machinePolicy.disableAutoUpdates) -or (-not [string]::IsNullOrWhiteSpace([string]$machinePolicy.egressProxyUrl))
    $userManaged = ($null -ne $userPolicy.disableAutoUpdates) -or (-not [string]::IsNullOrWhiteSpace([string]$userPolicy.egressProxyUrl))

    if ($machineManaged) {
        return [pscustomobject]@{ Source = 'HKLM 策略'; Value = [string]$machinePolicy.egressProxyUrl }
    }
    if ($userManaged) {
        return [pscustomobject]@{ Source = 'HKCU 策略'; Value = [string]$userPolicy.egressProxyUrl }
    }

    $metaPath = Join-Path $configRoot '_meta.json'
    if (-not (Test-Path -LiteralPath $metaPath)) {
        throw '找不到 Claude Desktop 当前配置，无法确认固定代理。'
    }
    $meta = Get-Content -LiteralPath $metaPath -Raw | ConvertFrom-Json
    if ([string]::IsNullOrWhiteSpace([string]$meta.appliedId)) {
        throw 'Claude Desktop 当前配置没有有效的 appliedId。'
    }
    $profilePath = Join-Path $configRoot ($meta.appliedId + '.json')
    if (-not (Test-Path -LiteralPath $profilePath)) {
        throw '找不到 Claude Desktop 当前配置文件。'
    }
    $profile = Get-Content -LiteralPath $profilePath -Raw | ConvertFrom-Json
    return [pscustomobject]@{ Source = '本地配置'; Value = [string]$profile.egressProxyUrl }
}

function Test-RecordedFilters {
    param($RecordedTargets, $ManifestTargets)

    if (@($RecordedTargets).Count -ne @($ManifestTargets).Count) { return $false }
    foreach ($manifestTarget in @($ManifestTargets)) {
        $recordedTarget = @($RecordedTargets | Where-Object Path -eq $manifestTarget.Path)
        if ($recordedTarget.Count -ne 1) { return $false }
        $filters = @($recordedTarget[0].Filters)
        if ($filters.Count -ne 2) { return $false }
        $expectedKeys = @([string]$manifestTarget.V4, [string]$manifestTarget.V6)
        if (@($filters | Where-Object { [string]$_.Key -notin $expectedKeys }).Count) { return $false }
        if (@($filters | Where-Object {
            $_.Enabled -ne $true -or
            $_.Persistent -ne $true -or
            $_.ExactAppIdCondition -ne $true -or
            $_.NonLoopbackCondition -ne $true -or
            $_.IsOwnedExactBlock -ne $true
        }).Count) { return $false }
    }
    return $true
}

function Assert-GuardEvidence {
    param([string]$PackageVersion)

    foreach ($path in @($guardManifestPath, $guardStatePath, $guardInstallPath)) {
        if (-not (Test-Path -LiteralPath $path)) {
            throw 'Windows 直连防护记录不完整。'
        }
    }

    $manifest = Get-Content -LiteralPath $guardManifestPath -Raw | ConvertFrom-Json
    $state = Get-Content -LiteralPath $guardStatePath -Raw | ConvertFrom-Json
    $install = Get-Content -LiteralPath $guardInstallPath -Raw | ConvertFrom-Json
    $manifestHash = (Get-FileHash -LiteralPath $guardManifestPath -Algorithm SHA256).Hash

    $pluginTargets = @($manifest.Targets | Where-Object Name -eq 'Claude Code VS Code 插件')
    $pluginRegistry = Join-Path $env:USERPROFILE '.vscode\extensions\extensions.json'
    if ($pluginTargets.Count -ne 1 -or -not (Test-Path -LiteralPath $pluginRegistry)) {
        throw 'VS Code Claude Code 插件的直连防护清单或当前登记不完整。'
    }
    $pluginRecords = @((Get-Content -LiteralPath $pluginRegistry -Raw | ConvertFrom-Json) | Where-Object { $_.identifier.id -ieq 'anthropic.claude-code' })
    if ($pluginRecords.Count -ne 1 -or $pluginRecords[0].metadata.pinned -ne $true) {
        throw 'VS Code Claude Code 插件未唯一登记或未固定版本。'
    }
    $pluginRoot = [string]$pluginRecords[0].location.path
    if ($pluginRoot -match '^/([A-Za-z]:/.*)$') { $pluginRoot = $Matches[1] }
    $currentPluginPath = Join-Path ($pluginRoot.Replace('/','\')) 'resources\native-binary\claude.exe'
    if ($pluginTargets[0].Path -ine $currentPluginPath) {
        throw 'VS Code Claude Code 插件已更新换路径，直连防护必须迁移后才能继续。'
    }
    $vsCodeSettings = Join-Path $env:APPDATA 'Code\User\settings.json'
    if (Test-Path -LiteralPath $vsCodeSettings) {
        $wrapperMatch = [regex]::Match((Get-Content -LiteralPath $vsCodeSettings -Raw), '"claudeCode\.claudeProcessWrapper"\s*:\s*"([^"]*)"')
        if ($wrapperMatch.Success -and $wrapperMatch.Groups[1].Value.Trim()) { throw 'VS Code 配置了未纳入防护的 claudeProcessWrapper。' }
    }

    if ([string]$manifest.PackageVersion -ne $PackageVersion) {
        throw 'Claude Desktop 已更新，Windows 直连防护尚未迁移到当前版本。'
    }
    foreach ($target in @($manifest.Targets)) {
        if (-not (Test-Path -LiteralPath $target.Path)) {
            throw ('直连防护目标不存在：' + $target.Name)
        }
        if ((Get-FileHash -LiteralPath $target.Path -Algorithm SHA256).Hash -ne $target.SHA256) {
            throw ('程序文件已变化，需要重新核验直连防护：' + $target.Name)
        }
    }
    if ([string]$state.Status -notlike 'installed_*' -or $state.ManifestSHA256 -ne $manifestHash) {
        throw 'Windows 直连防护安装状态与当前目标清单不一致。'
    }
    if (-not (Test-RecordedFilters $state.Targets $manifest.Targets)) {
        throw 'Windows 直连防护安装记录不完整。'
    }

    $verifyReady = $false
    if (Test-Path -LiteralPath $guardVerifyPath) {
        $verify = Get-Content -LiteralPath $guardVerifyPath -Raw | ConvertFrom-Json
        $verifyReady =
            ($verify.Status -eq 'passed') -and
            ($verify.ManifestSHA256 -eq $manifestHash) -and
            ($verify.PackageVersionMatches -eq $true) -and
            (Test-RecordedFilters $verify.Targets $manifest.Targets)
    }
    $installReady =
        ($install.Action -eq 'Install') -and
        ($install.Status -eq 'passed') -and
        (Test-RecordedFilters $install.Installed $manifest.Targets)
    if (-not $verifyReady -and -not $installReady) {
        throw '没有与当前版本匹配的管理员安装或 Verify 凭证。'
    }

    if ($verifyReady) { return '管理员 Verify 凭证' }
    return '管理员安装凭证'
}

function Assert-ClashRoute {
    if (-not (Test-Path -LiteralPath $clashRuntime)) {
        throw '找不到 Clash 当前运行配置。'
    }
    $text = Get-Content -LiteralPath $clashRuntime -Raw
    $listenerMatches = [regex]::Matches($text, '(?ms)^- name:\s*claude-desktop-http\s*$.*?(?=^- name:|\z)')
    if ($listenerMatches.Count -ne 1) {
        throw 'Clash 当前配置中的 Claude Desktop 专用入口缺失或重复。'
    }
    $listenerBlock = $listenerMatches[0].Value
    $listenerReady =
        ($listenerBlock -match '(?m)^\s*type:\s*http\s*$') -and
        ($listenerBlock -match '(?m)^\s*listen:\s*127\.0\.0\.1\s*$') -and
        ($listenerBlock -match "(?m)^\s*port:\s*$expectedPort\s*`$") -and
        ($listenerBlock -match '(?m)^\s*proxy:\s*ACCOUNT-US\s*$')
    if (-not $listenerReady) {
        throw 'Clash 的 43867 没有完整固定到 ACCOUNT-US。'
    }

    $groupMatches = [regex]::Matches($text, '(?ms)^- name:\s*ACCOUNT-US\s*$.*?(?=^- name:|\z)')
    if ($groupMatches.Count -ne 1) {
        throw 'Clash 当前配置中的 ACCOUNT-US 组缺失或重复。'
    }
    $groupBlock = $groupMatches[0].Value
    if ($groupBlock -notmatch '(?m)^\s*exclude-type:\s*Direct\|Reject\s*$') {
        throw 'ACCOUNT-US 没有排除 Direct/Reject，已拒绝启动 Claude。'
    }
}

try {
    $package = Get-AppxPackage -Name Claude | Select-Object -First 1
    if (-not $package) { throw '找不到 Claude Desktop 安装包。' }
    $exe = Join-Path $package.InstallLocation 'app\Claude.exe'
    if (-not (Test-Path -LiteralPath $exe)) { throw '找不到 Claude Desktop 主程序。' }

    $running = @(Get-CimInstance Win32_Process -Filter "Name='claude.exe'" | Where-Object { $_.ExecutablePath -eq $exe })
    if ($running.Count) { throw '请先完全退出 Claude Desktop，再使用这个入口。已有进程不会重新加载语言参数。' }

    $proxy = Get-EffectiveClaudeProxy
    if ($proxy.Value -ne $expectedProxy) {
        throw "Claude Desktop 固定代理不正确：$($proxy.Source) 当前不是 $expectedProxy。"
    }
    Write-Pass "固定代理：$($proxy.Source) -> $expectedProxy"

    Assert-ClashRoute
    Write-Pass 'Clash 路由：43867 -> ACCOUNT-US，且排除 Direct/Reject'

    $listeners = @(Get-NetTCPConnection -State Listen -LocalAddress 127.0.0.1 -LocalPort $expectedPort -ErrorAction SilentlyContinue)
    if (-not $listeners.Count) {
        throw 'Claude 专用代理 43867 未开启，已拒绝启动 Claude。'
    }
    $ownerNames = @($listeners.OwningProcess | Select-Object -Unique | ForEach-Object {
        (Get-Process -Id $_ -ErrorAction Stop).ProcessName
    } | Select-Object -Unique)
    if ($ownerNames.Count -ne 1 -or $ownerNames[0] -ne 'verge-mihomo') {
        throw ('43867 不是由 Clash 内核 verge-mihomo 提供，已拒绝启动 Claude。当前：' + ($ownerNames -join ', '))
    }
    Write-Pass '43867 正在监听，且归属 verge-mihomo'

    $guardEvidence = Assert-GuardEvidence -PackageVersion $package.Version.ToString()
    Write-Pass "Windows 直连防护记录有效：$guardEvidence；Claude Desktop $($package.Version) 与规则匹配"

    if (-not (Get-Command curl.exe -ErrorAction SilentlyContinue)) {
        throw '找不到 curl.exe，无法验证专用代理是否真正可用。'
    }
    $proxyOutput = (& curl.exe -sS --max-time 10 --proxy $expectedProxy https://api.ipify.org 2>$null | Out-String).Trim()
    $parsedIp = $null
    if ($LASTEXITCODE -ne 0 -or -not [Net.IPAddress]::TryParse($proxyOutput, [ref]$parsedIp)) {
        throw '43867 虽已监听，但无法通过它取得有效公网出口，已拒绝启动 Claude。'
    }
    Write-Pass "43867 代理连通，出口：$proxyOutput"

    if ($CheckOnly) {
        Write-Host '结论：启动前检查全部通过；本次只检查，没有启动 Claude。' -ForegroundColor Cyan
        exit 0
    }

    Start-Process -FilePath $exe -ArgumentList '--lang=en-US','--accept-lang=en-US,en'
    Write-Host '启动前检查全部通过，已启动 Claude 英文浏览器。' -ForegroundColor Green
} catch {
    Write-Host ('[BLOCK] ' + $_.Exception.Message) -ForegroundColor Red
    Write-Host '结论：已拒绝启动 Claude；没有放行真实 IP 直连。' -ForegroundColor Yellow
    if (-not $CheckOnly) {
        Read-Host '按回车关闭'
    }
    exit 1
}
