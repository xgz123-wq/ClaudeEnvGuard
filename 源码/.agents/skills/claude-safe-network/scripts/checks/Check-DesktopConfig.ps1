#requires -Version 7.0
<#
.SYNOPSIS
    第 4 层：Claude Desktop 配置检查。

.DESCRIPTION
    Claude Desktop 是本链路里最脆的一环 —— 它不读系统代理、只认官方策略字段。
    本脚本核对四件事：
      1. HKLM\SOFTWARE\Policies\Claude 的 egressProxyUrl 是否指向 Desktop 专用入口
      2. disableAutoUpdates 是否与 egressProxyUrl 处在「同一层」（不同层会被整体忽略）
      3. HKCU 策略不应存在（存在会与 HKLM 打架）
      4. configLibrary\_meta.json 里 appliedId 对应的配置内容是否真含该代理
    另做「升级漂移预警」：MSIX 当前版本 / 目标哈希 与 应用过滤目标.json 记录比对，
    一旦不一致就说明 Desktop 升级过、WFP 精确阻断已失效，必须重签。
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
$expectedProxy = "http://127.0.0.1:$($Ports.DesktopHttp)"
Write-Banner '第 4 层 · Claude Desktop 配置检查' "策略注册表 · 同层校验 · configLibrary · 升级漂移"

$detail = [ordered]@{ expectedProxy = $expectedProxy }

# ------------------------------------------------- 策略注册表
Write-Section '策略注册表（注意：真实路径没有 Anthropic 中间层）'
$policyLayers = @()
foreach ($key in $Topology.DesktopPolicyKeys) {
    if (-not (Test-Path $key)) { continue }
    $p = Get-ItemProperty -Path $key -ErrorAction SilentlyContinue
    $proxy = [string]$p.egressProxyUrl
    $disable = $p.disableAutoUpdates
    $policyLayers += $key
    $detail[$key] = @{ egressProxyUrl = $proxy; disableAutoUpdates = $disable }

    $label = if ($key -match 'WOW6432Node') { 'HKLM WOW6432Node 副本' } else { 'HKLM' }
    if ($proxy -eq $expectedProxy) {
        Add-CheckResult -Status 'PASS' -Item "$label egressProxyUrl" -Evidence $proxy
    }
    else {
        Add-CheckResult -Status 'FAIL' -Item "$label egressProxyUrl" -Evidence "$proxy（应为 $expectedProxy）"
    }
    # 关键：两者必须同层，否则 Desktop 会忽略代理直接尝试直连（09-12 踩过）
    if ($null -ne $disable -and $proxy) {
        Add-CheckResult -Status 'PASS' -Item "$label 同层完整" -Evidence "disableAutoUpdates 与 egressProxyUrl 在同一层"
    }
    elseif ($null -ne $disable) {
        Add-CheckResult -Status 'FAIL' -Item "$label 同层完整" -Evidence '只有 disableAutoUpdates，同层缺少 egressProxyUrl —— Desktop 会忽略代理'
    }
    else {
        Add-CheckResult -Status 'WARN' -Item "$label 同层完整" -Evidence '缺少 disableAutoUpdates（升级会被自动拉新版本）'
    }
}
if ($policyLayers.Count -eq 0) {
    Add-CheckResult -Status 'FAIL' -Item '策略注册表' -Evidence "HKLM\SOFTWARE\Policies\Claude 不存在 —— Desktop 会直连，被 WFP 精确规则拒绝"
}

$hkcu = 'HKCU:\SOFTWARE\Policies\Claude'
if (Test-Path $hkcu) {
    Add-CheckResult -Status 'WARN' -Item 'HKCU 策略' -Evidence '存在用户级策略，会与 HKLM 打架；建议统一走 HKLM'
}
else {
    Add-CheckResult -Status 'PASS' -Item 'HKCU 策略' -Evidence '不存在（策略统一走 HKLM，干净）'
}

# ------------------------------------------------- configLibrary
Write-Section 'configLibrary（Desktop 3p 本地配置库）'
$lib = Join-Path $env:LOCALAPPDATA 'Claude-3p\configLibrary'
$metaPath = Join-Path $lib '_meta.json'
if (-not (Test-Path $metaPath)) {
    Add-CheckResult -Status 'WARN' -Item '_meta.json' -Evidence "不存在：$metaPath"
}
else {
    try {
        $meta = Get-Content -LiteralPath $metaPath -Raw | ConvertFrom-Json
        $applied = [string]$meta.appliedId
        $named = @($meta.entries | Where-Object { $_.id -eq $applied } | Select-Object -First 1).name
        $detail['appliedId'] = $applied
        $detail['appliedName'] = $named

        if (-not $applied) {
            Add-CheckResult -Status 'FAIL' -Item 'appliedId' -Evidence '_meta.json 没有 appliedId —— 没有任何配置被应用'
        }
        else {
            Add-CheckResult -Status 'PASS' -Item 'appliedId' -Evidence "$applied（$named）"
            $profilePath = Join-Path $lib "$applied.json"
            if (Test-Path $profilePath) {
                $body = Get-Content -LiteralPath $profilePath -Raw
                $detail['profile'] = ($body | ConvertFrom-Json)
                Add-CheckResult -Status $(if ($body -match [regex]::Escape($expectedProxy)) { 'PASS' } else { 'FAIL' }) -Item '配置内容' `
                    -Evidence $(if ($body -match [regex]::Escape($expectedProxy)) { "含 $expectedProxy" } else { "不含 $expectedProxy：$body" })
            }
            else {
                Add-CheckResult -Status 'FAIL' -Item '配置内容' -Evidence "缺少配置文件：$profilePath"
            }
        }
    }
    catch { Add-CheckResult -Status 'FAIL' -Item '_meta.json' -Evidence ("解析失败：" + $_.Exception.Message) }
}

# ------------------------------------------------- 升级漂移
Write-Section '升级漂移预警（每次 Desktop 升级都必须重做）'
$manifestPath = Join-Path $root 'scripts\desktop\维护文件\应用过滤目标.json'
$installed = @(Get-AppxPackage -Name 'Claude' -ErrorAction SilentlyContinue | Select-Object -First 1)
$currentVersion = if ($installed) { $installed.Version.ToString() } else { '' }
Add-CheckResult -Status $(if ($currentVersion) { 'PASS' } else { 'WARN' }) -Item 'MSIX 已安装版本' -Evidence $(if ($currentVersion) { "Claude_pzs8sxrjxfjjc = $currentVersion" } else { '未检测到 MSIX 包' })
$detail['msixVersion'] = $currentVersion

if (Test-Path $manifestPath) {
    $man = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $detail['manifestVersion'] = $man.PackageVersion
    $detail['manifestPreparedAt'] = $man.PreparedAt

    if ($currentVersion -and $currentVersion -ne [string]$man.PackageVersion) {
        Add-CheckResult -Status 'FAIL' -Item '版本漂移' -Evidence "当前 $currentVersion ≠ 清单 $($man.PackageVersion)；WFP 精确阻断已失效，Desktop 会被判直连"
    }
    else {
        Add-CheckResult -Status 'PASS' -Item '版本漂移' -Evidence "$($man.PackageVersion)（清单 $($man.PreparedAt)）"
    }

    Write-Section 'WFP 精确阻断目标哈希'
    $hashOk = 0; $hashBad = 0; $hashMissing = 0
    foreach ($t in $man.Targets) {
        if (-not (Test-Path -LiteralPath $t.Path)) { $hashMissing++; continue }
        $h = (Get-FileHash -LiteralPath $t.Path -Algorithm SHA256 -ErrorAction SilentlyContinue).Hash
        if ($h -eq $t.SHA256) { $hashOk++ }
        else {
            $hashBad++
            Add-CheckResult -Status 'FAIL' -Item "哈希漂移：$($t.Name)" -Evidence "$($h.Substring(0,16))… ≠ 清单 $($t.SHA256.Substring(0,16))…"
        }
    }
    if ($hashBad -eq 0) {
        Add-CheckResult -Status 'PASS' -Item '目标哈希' -Evidence "一致 $hashOk/$(@($man.Targets).Count)$(if($hashMissing -gt 0){"（$hashMissing 个路径不存在）"})"
    }
}
else {
    Add-CheckResult -Status 'WARN' -Item '应用过滤目标.json' -Evidence "缺少：$manifestPath"
}

# 安装状态记录（含历史 pending 项，如实报告不美化）
$statePath = Join-Path $root 'scripts\desktop\维护文件\应用直连防护状态.json'
if (Test-Path $statePath) {
    $st = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    $detail['guardStatus'] = $st.Status
    Add-CheckResult -Status $(if ($st.Status -match 'installed') { 'PASS' } else { 'WARN' }) -Item '直连阻断安装状态' -Evidence "$($st.Status)（装于 $($st.InstalledAt)）"
    $verifyPath = Join-Path $root 'scripts\desktop\维护文件\应用直连防护-Verify.json'
    $limit = 30
    # 这条记录是「某次提权跑出来的快照」，不代表现在还是这样 ——
    # Desktop 每次升级都会重建 MSIX 目录、WFP 里的旧过滤器会失效，所以必须带时效判断。
    # 记录缺失 / 过期 / 版本对不上 / 上次是 failed 时，当场发起一次 UAC 重新核对
    # （用户只需点一下「是」），而不是把结论挂成「待人工」。
    $readGuardVerify = {
        param([string]$Path)
        if (-not (Test-Path -LiteralPath $Path)) { return $null }
        try { return (Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json) } catch { return $null }
    }
    $guardVerifyAge = {
        param($Record)
        if (-not $Record -or -not $Record.At) { return $null }
        try { return [math]::Floor(((Get-Date) - [datetime]$Record.At).TotalDays) } catch { return $null }
    }

    $vf = & $readGuardVerify $verifyPath
    $ageDays = & $guardVerifyAge $vf
    # 版本字段缺失（老记录）按「未知」处理，不当成不匹配，避免每次总检都白提权一次
    $verOk = if ($vf -and $null -ne $vf.PackageVersionMatches) { [bool]$vf.PackageVersionMatches } else { $true }
    $elev = $null
    if ((-not $vf) -or ($null -eq $ageDays) -or ($ageDays -gt $limit) -or (-not $verOk) -or ([string]$vf.Status -eq 'failed')) {
        $elev = Update-ElevatedVerifyCache -MaxAgeDays $limit
        $vf = & $readGuardVerify $verifyPath
        $ageDays = & $guardVerifyAge $vf
        $verOk = if ($vf -and $null -ne $vf.PackageVersionMatches) { [bool]$vf.PackageVersionMatches } else { $true }
    }
    if ($vf) {
        $detail['guardVerifyAt'] = [string]$vf.At
        $detail['guardVerifyAgeDays'] = $ageDays
        $detail['guardVerifyVersion'] = [string]$vf.PackageVersion
    }
    $noEvidence = ''
    if ($elev) {
        if ($elev.Skipped) { $noEvidence = "（$($elev.Skipped)）" }
        elseif ($elev.Error) { $noEvidence = "（自动提权没跑成：$($elev.Error)）" }
    }

    if (-not $vf) {
        Add-CheckResult -Status 'WARN' -Item '直连阻断端到端验证' -Evidence ("没有可用的管理员验证记录$noEvidence；跑 pwsh -File scripts\checks\Request-ElevatedVerify.ps1 -Refresh 点一次「是」即可（之后 30 天免提权）")
    }
    elseif ([string]$vf.Status -eq 'failed') {
        Add-CheckResult -Status 'FAIL' -Item '直连阻断端到端验证' -Evidence "提权复跑后 Verify 仍是 failed → 直连没有被真正挡住$noEvidence，跑 scripts\desktop\维护文件\应用直连防护管理.ps1 -Action Install 重签"
    }
    elseif (-not $verOk) {
        Add-CheckResult -Status 'WARN' -Item '直连阻断端到端验证' -Evidence "记录里的 Claude 版本是 $($vf.PackageVersion)，与当前不一致 → Desktop 升级后旧阻断过滤器可能已失效，必须重签：scripts\desktop\维护文件\应用直连防护管理.ps1 -Action Install"
    }
    elseif ($null -eq $ageDays) {
        Add-CheckResult -Status 'PASS' -Item '直连阻断端到端验证' -Evidence "$($vf.Status)（记录未写时间，无法判时效）"
    }
    elseif ($ageDays -gt $limit) {
        Add-CheckResult -Status 'WARN' -Item '直连阻断端到端验证' -Evidence "$($vf.Status)，但记录已 $ageDays 天（>$limit 天），且刚才的提权复跑没能刷新它$noEvidence"
    }
    else {
        Add-CheckResult -Status 'PASS' -Item '直连阻断端到端验证' -Evidence "$($vf.Status)（$ageDays 天前提权复核，$limit 天内有效）"
    }
}

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows -FailHint "Desktop 断网/直连被拒的根因基本都在这里；改注册表要同层改 $($Ports.DesktopHttp)。"

if ($Json) {
    Write-JsonResult @{ script = 'Check-DesktopConfig.ps1'; ok = ($code -eq 0); detail = $detail; results = $rows }
}
exit $code
