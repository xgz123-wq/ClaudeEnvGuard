#requires -Version 7.0
[CmdletBinding()]
param(
    [ValidateSet('Inspect','Apply')][string]$Action = 'Inspect',
    [Parameter(Mandatory)][string]$PackagePath,
    # 人工审核整个包及声明后，传入 Inspect 输出的 SHA256；不会自行提权。
    [string]$ApprovedSHA256,
    # 注册前失败时复用日志内已生效的新规则；禁止重复添加孤立规则。
    [string]$ResumeJournal
)
$ErrorActionPreference = 'Stop'
$helper = Join-Path $PSScriptRoot '应用直连过滤.cs'
$manager = Join-Path $PSScriptRoot '应用直连防护管理.ps1'
$manifestPath = Join-Path $PSScriptRoot '应用过滤目标.json'
$statePath = Join-Path $PSScriptRoot '应用直连防护状态.json'
$journalPath = $null
$journal = [ordered]@{ At=(Get-Date -Format o); Action=$Action; Status='running'; Steps=@(); ClaudeLaunched=$false; GlobalNetworkChanged=$false; RegistrationAttempted=$false }
function Save-Journal {
    if ($journalPath) { $journal | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $journalPath -Encoding utf8 }
}
function Step([string]$Name, $Evidence) {
    $journal.Steps += [pscustomobject]@{At=(Get-Date -Format o);Name=$Name;Evidence=$Evidence}
    Save-Journal
    Write-Host ('完成：' + $Name)
}
function Assert-Environment {
    if (@(Get-Process -Name claude -ErrorAction SilentlyContinue).Count) { throw 'Claude 正在运行；请退出后重试。本脚本不会结束或启动应用。' }
    $policy = Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Policies\Claude'
    if ($policy.disableAutoUpdates -ne 1 -or $policy.egressProxyUrl -ne 'http://127.0.0.1:43867') {
        throw '机器级禁更与 43867 专用代理策略必须同时存在且正确。'
    }
}
function Assert-Signature([string]$Path) {
    $signature = Get-AuthenticodeSignature -LiteralPath $Path
    if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O="?Anthropic, PBC"?(,|$)') {
        throw ('文件没有有效 Anthropic 签名：' + $Path)
    }
    return [pscustomobject]@{Status=[string]$signature.Status;Subject=$signature.SignerCertificate.Subject;Thumbprint=$signature.SignerCertificate.Thumbprint}
}
function Select-Filters($Filters) {
    @($Filters | Select-Object Key,Layer,Id,Flags,NativeAppPath,Enabled,Persistent,ProviderIsNull,ExactAppIdCondition,NonLoopbackCondition,IsOwnedExactBlock)
}
function Verify-Targets($Targets) {
    @(foreach ($target in $Targets) {
        if ((Get-FileHash -LiteralPath $target.Path -Algorithm SHA256).Hash -ne $target.SHA256) { throw ('目标文件发生变化：' + $target.Name) }
        [pscustomobject]@{Name=$target.Name;Path=$target.Path;Filters=(Select-Filters ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Verify($target.Path,[guid]$target.V4,[guid]$target.V6,$true)))}
    })
}
function Run-Manager([string]$Mode) {
    & (Join-Path $PSHOME 'pwsh.exe') -NoProfile -File $manager -Action $Mode
    if ($LASTEXITCODE -ne 0) { throw ('防护管理 ' + $Mode + ' 失败，退出码：' + $LASTEXITCODE) }
}
function Assert-NoDuplicatePreGuard([string]$NewPath, [string]$RecoveryJournal) {
    $approved = if ($RecoveryJournal) { Get-Content -LiteralPath $RecoveryJournal -Raw | ConvertFrom-Json } else { $null }
    # Codex 等 MSIX 打包应用写 %LOCALAPPDATA% 会被重定向到 Packages\<包>\LocalCache\Local；两处历史都要查。
    $roots=@(Join-Path $env:LOCALAPPDATA 'ClaudeNetworkGuard') + @(Get-ChildItem -Path (Join-Path $env:LOCALAPPDATA 'Packages\*\LocalCache\Local\ClaudeNetworkGuard') -Directory -ErrorAction SilentlyContinue | ForEach-Object FullName)
    foreach ($directory in @($roots | Where-Object { Test-Path -LiteralPath $_ } | ForEach-Object { Get-ChildItem -LiteralPath $_ -Directory -Filter '受控更新-*' })) {
        $path=Join-Path $directory.FullName '更新过程.json'
        if (-not (Test-Path -LiteralPath $path)) { continue }
        $prior=Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
        if (-not $prior.NewDesktop -or $prior.NewDesktop.Path -ine $NewPath) { continue }
        $v4=[guid]$prior.NewDesktop.V4; $v6=[guid]$prior.NewDesktop.V6
        if ($null -eq [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get($v4) -and $null -eq [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get($v6)) { continue }
        if (-not $approved -or $approved.NewDesktop.V4 -ne [string]$v4 -or $approved.NewDesktop.V6 -ne [string]$v6) {
            throw ('检测到该新版已有预保护规则；必须核对并用 -ResumeJournal 复用，不能普通重试。记录：'+$path)
        }
    }
}
try {
    Assert-Environment
    $PackagePath = (Resolve-Path -LiteralPath $PackagePath).Path
    if ([IO.Path]::GetExtension($PackagePath) -ne '.msix') { throw '只接受审核过的官方 .msix 包。' }
    $packageHash = (Get-FileHash -LiteralPath $PackagePath -Algorithm SHA256).Hash
    $packageSignature = Assert-Signature $PackagePath
    # XML 禁用 DTD 与外部解析；ZIP 只读，不解压或执行任何包内容。
    $zip = [IO.Compression.ZipFile]::OpenRead($PackagePath)
    try {
        $entries = @($zip.Entries | Where-Object {$_.FullName -ceq 'AppxManifest.xml'})
        if ($entries.Count -ne 1 -or $entries[0].Length -gt 4MB) { throw 'MSIX manifest 缺失、重复或过大。' }
        $settings = [Xml.XmlReaderSettings]::new()
        $settings.DtdProcessing = [Xml.DtdProcessing]::Prohibit
        $settings.XmlResolver = $null
        $stream = $entries[0].Open()
        $reader = [Xml.XmlReader]::Create($stream,$settings)
        try { $xml = [Xml.XmlDocument]::new(); $xml.XmlResolver=$null; $xml.Load($reader) }
        finally { $reader.Dispose(); $stream.Dispose() }
        if (@($zip.Entries | Where-Object {$_.FullName -ieq 'app/claude.exe'}).Count -ne 1) { throw '包中未找到唯一 app/claude.exe。' }
    } finally { $zip.Dispose() }
    $identity = $xml.SelectSingleNode('/*[local-name()="Package"]/*[local-name()="Identity"]')
    $applications = @($xml.SelectNodes('/*[local-name()="Package"]/*[local-name()="Applications"]/*[local-name()="Application"]'))
    $mainApplications = @($applications | Where-Object {$_.GetAttribute('Id') -ceq 'Claude'})
    if ($mainApplications.Count -ne 1 -or $mainApplications[0].GetAttribute('Executable').Replace('/','\') -ine 'app\claude.exe') { throw '主程序声明变化，需要单独修订更新流程。' }
    Import-Module Appx -UseWindowsPowerShell -WarningAction SilentlyContinue
    $packages = @(Get-AppxPackage -Name Claude)
    if ($packages.Count -ne 1) { throw '当前用户 Claude 包不唯一或不存在。' }
    $current = $packages[0]
    if ($identity.GetAttribute('Name') -cne $current.Name -or $identity.GetAttribute('Publisher') -cne $current.Publisher -or $identity.GetAttribute('ProcessorArchitecture') -ne [string]$current.Architecture) { throw '包名称、发布者或架构与已装版本不符。' }
    $newVersion = [version]$identity.GetAttribute('Version')
    if ($newVersion -lt [version]$current.Version) { throw '拒绝降级。' }
    $inspection = [ordered]@{Path=$PackagePath;SHA256=$packageHash;Signature=$packageSignature;OldVersion=[string]$current.Version;NewVersion=[string]$newVersion;PackageFamily=$current.PackageFamilyName;Identity=$identity.OuterXml;Applications=($applications | ForEach-Object {$_.OuterXml});Dependencies=$xml.SelectSingleNode('/*[local-name()="Package"]/*[local-name()="Dependencies"]').OuterXml;Extensions=@($xml.SelectNodes('//*[local-name()="Extension"]') | ForEach-Object {$_.OuterXml});Capabilities=$xml.SelectSingleNode('/*[local-name()="Package"]/*[local-name()="Capabilities"]').OuterXml}
    if ($Action -eq 'Inspect') { $inspection | ConvertTo-Json -Depth 12; exit 0 }
    if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Apply 必须在管理员 PowerShell 中执行。' }
    if ($ApprovedSHA256 -notmatch '^[a-fA-F0-9]{64}$' -or $ApprovedSHA256 -ne $packageHash) { throw 'ApprovedSHA256 必须匹配人工审核后的完整包。' }
    if ($newVersion -eq [version]$current.Version) { throw '已是此版本，无需执行更新；请运行现有 Verify。' }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    if ($manifest.PackageFamily -ne $current.PackageFamilyName -or $manifest.PackageVersion -ne [string]$current.Version -or $state.Status -notlike 'installed_*' -or $state.ManifestSHA256 -ne (Get-FileHash -LiteralPath $manifestPath).Hash -or $state.HelperSHA256 -ne (Get-FileHash -LiteralPath $helper).Hash) { throw '现有版本、清单或防护状态不一致。' }
    $oldExe = Join-Path $current.InstallLocation 'app\claude.exe'
    $desktopTargets = @($manifest.Targets | Where-Object {$_.Path -ieq $oldExe})
    if ($desktopTargets.Count -ne 1 -or $manifest.Targets.Count -ne 4) { throw '预期一个 Desktop 目标、两个 CLI 目标与一个 VS Code 插件目标；清单变化需要人工审核。' }
    $oldDesktop = $desktopTargets[0] | ConvertTo-Json -Depth 10 | ConvertFrom-Json
    $oldCode = @($manifest.Targets | Where-Object {$_.Path -ine $oldExe}) | ConvertTo-Json -Depth 10 -Compress
    $allKeys = @($manifest.Targets | ForEach-Object {[guid]$_.V4;[guid]$_.V6})
    if (@($allKeys | Select-Object -Unique).Count -ne 8 -or $allKeys -contains [guid]::Empty) { throw '原有过滤 GUID 不唯一或无效。' }
    Add-Type -LiteralPath $helper
    $oldVerified = Verify-Targets $manifest.Targets
    $backup = Join-Path $env:LOCALAPPDATA ('ClaudeNetworkGuard\受控更新-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
    $null = New-Item -ItemType Directory -Path $backup
    # 备份在第一次调用 Verify/Test 前完成，避免覆盖原验收凭证。
    foreach ($name in @('应用过滤目标.json','应用直连防护状态.json','应用直连防护-Verify.json','应用直连防护-最近操作.json','应用直连防护-Refresh.json','应用直连过滤.cs','应用直连防护管理.ps1','更新ClaudeDesktop.ps1')) {
        $path = Join-Path $PSScriptRoot $name
        if (Test-Path -LiteralPath $path) { Copy-Item -LiteralPath $path -Destination $backup }
    }
    $journalPath = Join-Path $backup '更新过程.json'
    $journal.Package=$inspection
    $journal.OldDesktop=$oldDesktop
    $journal.OldVerified=$oldVerified
    $journal.Backup=$backup
    $journal.UserSid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    Step '保存恢复材料与旧规则实查' $backup
    Run-Manager 'Verify'
    # 更新仅核验现有规则，不运行探针或真实应用。
    Step '旧版本防护与独立探针通过' $true
    Assert-Environment
    if ((Get-FileHash -LiteralPath $PackagePath).Hash -ne $packageHash) { throw '审核后的安装包发生变化。' }
    $stageVolume = @(Get-AppxVolume | Where-Object { $_.PackageStorePath -ieq (Split-Path $current.InstallLocation -Parent) -and -not $_.IsOffline })
    if ($stageVolume.Count -ne 1) { throw '找不到当前 Claude 所在的唯一在线安装卷。' }
    Add-AppxPackage -Path $PackagePath -Stage -Volume $stageVolume[0] -ErrorAction Stop
    $registered = @(Get-AppxPackage -Name Claude)
    if ($registered.Count -ne 1 -or $registered[0].PackageFullName -ne $current.PackageFullName) { throw 'Stage 后当前用户注册状态意外改变；保留现场，停止。' }
    $staged = @(Get-AppxPackage -AllUsers -Name Claude | Where-Object {[string]$_.Version -eq [string]$newVersion -and $_.PackageFamilyName -eq $current.PackageFamilyName -and [string]$_.Architecture -eq [string]$current.Architecture})
    if ($staged.Count -ne 1 -or -not $staged[0].InstallLocation) { throw '找不到唯一实际 Stage 路径。' }
    $newRoot = [IO.Path]::GetFullPath($staged[0].InstallLocation)
    $newExe = Join-Path $newRoot 'app\claude.exe'
    $newManifest = Join-Path $newRoot 'AppxManifest.xml'
    if (-not (Test-Path -LiteralPath $newExe) -or -not (Test-Path -LiteralPath $newManifest)) { throw 'Stage 文件缺失。' }
    $newSignature = Assert-Signature $newExe
    $newHash = (Get-FileHash -LiteralPath $newExe).Hash
    Assert-NoDuplicatePreGuard $newExe $ResumeJournal
    if ($ResumeJournal) {
        $resume=Get-Content -LiteralPath $ResumeJournal -Raw | ConvertFrom-Json
        if ($resume.Status -ne 'failed' -or $resume.Package.SHA256 -ne $packageHash -or $resume.OldDesktop.Path -ine $oldExe -or $resume.NewDesktop.Path -ine $newExe -or $resume.NewDesktop.SHA256 -ne $newHash) { throw '恢复日志与本次旧版、审核包或新版文件不匹配。' }
        $newV4=[guid]$resume.NewDesktop.V4; $newV6=[guid]$resume.NewDesktop.V6
        $journal.ResumedFrom=(Resolve-Path -LiteralPath $ResumeJournal).Path
    } else {
        $newV4=[guid]::NewGuid(); $newV6=[guid]::NewGuid()
    }
    $journal.NewDesktop=[ordered]@{Path=$newExe;SHA256=$newHash;V4=[string]$newV4;V6=[string]$newV6;Signature=$newSignature;PackageFullName=$staged[0].PackageFullName}
    Step '新版已落盘且尚未注册' $newRoot
    Assert-Environment
    $null = [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Add($newExe,$newV4,$newV6,$true)
    $newFilters = Select-Filters ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Verify($newExe,$newV4,$newV6,$true))
    Step '新版首次注册前的直连防护已生效' $newFilters
    Assert-Environment
    if ((Get-FileHash -LiteralPath $newExe).Hash -ne $newHash) { throw 'Stage 程序文件变化。' }
    $null = Verify-Targets $manifest.Targets
    $journal.RegistrationAttempted=$true
    Save-Journal
    # 前面已拒绝任何运行中的 Claude；让安装器释放旧包残留资源，不延迟到下次启动。
    Add-AppxPackage -Path $newManifest -Register -DisableDevelopmentMode -ForceTargetApplicationShutdown -ErrorAction Stop
    $registered = @(Get-AppxPackage -Name Claude)
    if ($registered.Count -ne 1 -or $registered[0].PackageFullName -ne $staged[0].PackageFullName -or $registered[0].InstallLocation -ine $newRoot) { throw '注册返回后实际版本或路径不符。' }
    Assert-Environment
    Step '当前用户新版注册完成' $registered[0].PackageFullName
    $desktopTargets[0].Path=$newExe; $desktopTargets[0].SHA256=$newHash; $desktopTargets[0].V4=[string]$newV4; $desktopTargets[0].V6=[string]$newV6
    $manifest.PackageVersion=[string]$newVersion
    $manifest.PreparedAt=Get-Date -Format o
    $manifest.Status='migrated_current_paths_pending_desktop_e2e'
    if ((@($manifest.Targets | Where-Object {$_.Path -ine $newExe}) | ConvertTo-Json -Depth 10 -Compress) -cne $oldCode) { throw 'Code 清单出现非预期变化。' }
    $verified = Verify-Targets $manifest.Targets
    $manifest | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $manifestPath -Encoding utf8
    $state.ManifestSHA256=(Get-FileHash -LiteralPath $manifestPath).Hash
    $state.Targets=$verified
    $state.Status='installed_current_paths_pending_desktop_e2e'
    $state | Add-Member -NotePropertyName RevalidatedAt -NotePropertyValue (Get-Date -Format o) -Force
    $state | Add-Member -NotePropertyName UpdateJournal -NotePropertyValue $journalPath -Force
    $state | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $statePath -Encoding utf8
    Run-Manager 'Verify'
    Step '新版清单、状态和管理员 Verify 已一致' $true
    Assert-Environment
    $oldEvidence = @($oldVerified | Where-Object {$_.Path -ieq $oldExe})[0].Filters
    # Windows 可能已清理旧 EXE；用注册前的真实 WFP 读回记录校验旧规则归属。
    foreach ($expected in $oldEvidence) {
        $actual = [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get([guid]$expected.Key)
        if ($null -eq $actual -or -not $actual.IsOwnedExactBlock -or -not $actual.Enabled -or -not $actual.Persistent -or $actual.NativeAppPath -cne $expected.NativeAppPath -or $actual.Layer -ne $expected.Layer -or $actual.Id -ne $expected.Id) { throw '旧规则缺失或被改动；保留新旧防护，停止清理。' }
    }
    $null = Verify-Targets $manifest.Targets
    $removed = [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Remove([guid]$oldDesktop.V4,[guid]$oldDesktop.V6)
    if ($removed -ne 2 -or $null -ne [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get([guid]$oldDesktop.V4) -or $null -ne [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get([guid]$oldDesktop.V6)) { throw '旧 Desktop 规则清理结果不符。' }
    Run-Manager 'Verify'
    Assert-Environment
    Step '旧 Desktop 两条已移除，当前八条防护复核通过' $removed
    $journal.Status='passed_pending_desktop_e2e'
    Save-Journal
    Write-Output ('更新完成：' + $current.Version + ' → ' + $newVersion + '；记录：' + $journalPath + '。未启动 Claude；真实应用验收另行记录。')
} catch {
    $journal.Status='failed'
    $journal.Error=$_.Exception.Message
    $journal.Recovery='保留现有防护及现场；禁止自动撤销新规则、恢复旧清单或降级。按日志核对实际注册版本后继续修复。'
    Save-Journal
    Write-Error -Message ($_.Exception.Message + $(if($journalPath){'；现场记录：'+$journalPath}else{''})) -ErrorAction Continue
    exit 1
}

