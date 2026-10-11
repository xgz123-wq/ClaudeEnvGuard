#requires -Version 7.0
<#
.SYNOPSIS
    第 5 层：Windows 系统级兜底检查。

.DESCRIPTION
    这一层管的是「就算 Claude 自己不想走代理，也出不去」的兜底：
      · 系统代理必须关闭（与 TUN 双开会让流量走两条路，难排查）
      · 用户/机器级环境变量里不能有 HTTP_PROXY 之类（Claude 之外的进程会被污染）
      · TUN 的 strict-route / DNS 劫持（防旁路 + 防 DNS 泄漏）
      · 实体网卡的 IPv6 绑定状态（TUN 关闭时 IPv6 是真泄漏口）
      · hosts 文件不得有 Claude 相关劫持
      · WFP 清单要求的精确阻断是否仍在位（只有管理员读得到 → 一次 UAC + 30 天缓存，见 Update-ElevatedVerifyCache）
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
Write-Banner '第 5 层 · Windows 系统级兜底检查' '系统代理 · 环境变量 · TUN 防旁路 · IPv6 · hosts · WFP 阻断'

$detail = [ordered]@{}

# ------------------------------------------------- 系统代理
Write-Section '系统代理（必须关闭）'
$inet = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings'
try {
    $p = Get-ItemProperty -Path $inet -ErrorAction Stop
    $enabled = [int]$p.ProxyEnable
    $server = [string]$p.ProxyServer
    $detail['systemProxy'] = @{ enable = $enabled; server = $server }
    if ($enabled -eq 1) {
        Add-CheckResult -Status 'FAIL' -Item '系统代理开关' -Evidence "ProxyEnable=1（$server）—— 与 TUN 双开会双抓流量"
    }
    else {
        Add-CheckResult -Status 'PASS' -Item '系统代理开关' -Evidence 'ProxyEnable=0'
    }
    $auto = [string]$p.AutoConfigURL
    if ($auto) { Add-CheckResult -Status 'WARN' -Item '自动配置脚本 (PAC)' -Evidence $auto }
}
catch { Add-CheckResult -Status 'WARN' -Item '系统代理开关' -Evidence ("读不到注册表：" + $_.Exception.Message) }

# ------------------------------------------------- 环境变量
Write-Section '系统级环境变量（不允许出现代理指向）'
$dirty = @()
foreach ($scope in 'User', 'Machine') {
    foreach ($k in 'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'ANTHROPIC_BASE_URL') {
        $v = [Environment]::GetEnvironmentVariable($k, $scope)
        if ($v) { $dirty += "$scope/$k=$v" }
    }
}
if ($dirty.Count -eq 0) {
    Add-CheckResult -Status 'PASS' -Item '环境变量' -Evidence 'User / Machine 两处都干净'
}
else {
    Add-CheckResult -Status 'FAIL' -Item '环境变量' -Evidence ($dirty -join '; ')
}
$detail['envDirty'] = $dirty

# ------------------------------------------------- TUN
Write-Section 'TUN 防旁路'
$runtimeYaml = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev\clash-verge.yaml'
if (Test-Path $runtimeYaml) {
    $rt = Get-Content -LiteralPath $runtimeYaml -Raw
    $sr = [regex]::Match($rt, '(?m)^\s*strict-route:\s*(\S+)').Groups[1].Value
    Add-CheckResult -Status $(if ($sr -eq 'true') { 'PASS' } else { 'FAIL' }) -Item 'strict-route' -Evidence "strict-route=$sr$(if($sr -ne 'true'){' —— 关掉后应用可绕过 TUN 直连'})"
    $st = [regex]::Match($rt, '(?m)^\s*stack:\s*(\S+)').Groups[1].Value
    Add-CheckResult -Status $(if ($st) { 'PASS' } else { 'WARN' }) -Item 'TUN stack' -Evidence "stack=$st"
    $u = $rt -match '(?m)^\s*-\s*any:53\s*$'; $t = $rt -match '(?m)^\s*-\s*tcp://any:53\s*$'
    Add-CheckResult -Status $(if ($u -and $t) { 'PASS' } else { 'FAIL' }) -Item 'DNS 劫持' -Evidence "any:53=$u; tcp://any:53=$t"
    $ar = [regex]::Match($rt, '(?m)^\s*auto-route:\s*(\S+)').Groups[1].Value
    Add-CheckResult -Status $(if ($ar -eq 'true') { 'PASS' } else { 'WARN' }) -Item 'auto-route' -Evidence "auto-route=$ar"
}
else {
    Add-CheckResult -Status 'FAIL' -Item 'TUN 配置' -Evidence "缺少 $runtimeYaml"
}

# ------------------------------------------------- IPv6
Write-Section '实体网卡 IPv6 绑定'
# 只关心「真能上网的物理网卡」：虚拟网卡 / 蓝牙 / WSL / 隧道不是泄漏路径，另列不计分
$virtualLike = 'Mihomo|Clash|TUN|Meta|Bluetooth|蓝牙|vEthernet|WSL|Hyper-V|Loopback|VirtualBox|VMware|TAP|Tailscale|ZeroTier|VPN'
try {
    $ifaces = @(Get-NetAdapter -ErrorAction Stop)
    $physUp = @($ifaces | Where-Object { $_.Status -eq 'Up' -and -not $_.Virtual -and $_.Name -notmatch $virtualLike })
    $other = @($ifaces | Where-Object { $_.Name -match $virtualLike })
    $bind = @(Get-NetAdapterBinding -ComponentID 'ms_tcpip6' -ErrorAction Stop |
            Where-Object { $physUp.Name -contains $_.Name })
    $detail['ipv6'] = @($bind | ForEach-Object { "$($_.Name)=$($_.Enabled)" })
    $detail['ipv6Excluded'] = @($other | ForEach-Object { $_.Name })
    if ($bind.Count -eq 0) {
        Add-CheckResult -Status 'WARN' -Item 'IPv6 绑定' -Evidence '当前没有任何「已连接物理网卡」，这一项没被验证到；联网后复跑'
    }
    else {
        $on = @($bind | Where-Object Enabled)
        $note = if ($other.Count -gt 0) { "；已排除 $($other.Count) 个虚拟/隧道网卡" } else { '' }
        if ($on.Count -eq 0) {
            Add-CheckResult -Status 'PASS' -Item 'IPv6 绑定' -Evidence "物理网卡 $($bind.Count) 张全部关闭 IPv6$note"
        }
        else {
            Add-CheckResult -Status 'WARN' -Item 'IPv6 绑定' -Evidence ("物理网卡 {" + (($on | ForEach-Object { $_.Name }) -join ', ') + "} 仍绑定 IPv6 —— 关掉 TUN 时这是真实泄漏口，需人工确认无 IPv6 出口$note")
        }
    }
}
catch { Add-CheckResult -Status 'WARN' -Item 'IPv6 绑定' -Evidence ("查询失败：" + $_.Exception.Message) }

# ------------------------------------------------- hosts
Write-Section 'hosts 文件'
$hosts = Join-Path $env:SystemRoot 'System32\drivers\etc\hosts'
if (Test-Path $hosts) {
    $lines = @(Get-Content -LiteralPath $hosts -ErrorAction SilentlyContinue | Where-Object { $_ -match '^\s*[0-9a-fA-F:.]+\s+\S' })
    $claude = @($lines | Where-Object { $_ -match 'claude|anthropic' })
    if ($claude.Count -gt 0) {
        Add-CheckResult -Status 'FAIL' -Item 'hosts 劫持' -Evidence ($claude -join ' | ')
    }
    else {
        Add-CheckResult -Status 'PASS' -Item 'hosts' -Evidence "无 Claude/Anthropic 条目（自定义条目 $($lines.Count) 条，不影响）"
    }
}
else { Add-CheckResult -Status 'WARN' -Item 'hosts' -Evidence "找不到 $hosts" }

# ------------------------------------------------- WFP
Write-Section 'WFP 精确阻断'
# 实盘只有管理员读得到 —— 这是 Windows 的设计，不是脚本写得不好：
# netsh wfp show filters 和直接调 FwpmFilterGetById0 两条路在非提权下都是
# ERROR_ACCESS_DENIED (5)，实测确认。所以核对结论走「一次 UAC + 30 天缓存」，
# 普通权限下读缓存即可，不再每次都要求提权。
$isAdmin = Test-IsAdmin
$detail['isAdmin'] = $isAdmin
$manifestPath = $root + '\scripts\desktop\维护文件\应用过滤目标.json'
$statePath = $root + '\scripts\desktop\维护文件\应用直连防护状态.json'
$refreshHint = 'scripts\checks\Request-ElevatedVerify.ps1 -Refresh'

if (Test-Path $statePath) {
    $st = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    $expectedKeys = @()
    if (Test-Path $manifestPath) {
        $man = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
        foreach ($t in $man.Targets) { $expectedKeys += $t.V4; $expectedKeys += $t.V6 }
    }
    $pluginPathProblem = ''
    $pluginTargets = @($man.Targets | Where-Object Name -eq 'Claude Code VS Code 插件')
    $pluginRegistry = Join-Path $env:USERPROFILE '.vscode\extensions\extensions.json'
    if ($pluginTargets.Count -ne 1) {
        $pluginPathProblem = "防护清单里的 VS Code 插件目标数量为 $($pluginTargets.Count)，预期 1"
    }
    elseif (-not (Test-Path -LiteralPath $pluginRegistry)) {
        $pluginPathProblem = '缺少 VS Code extensions.json，无法确认当前插件路径'
    }
    else {
        $pluginRecords = @((Get-Content -LiteralPath $pluginRegistry -Raw | ConvertFrom-Json) | Where-Object { $_.identifier.id -ieq 'anthropic.claude-code' })
        if ($pluginRecords.Count -ne 1) {
            $pluginPathProblem = "VS Code 当前登记的 Claude Code 插件数量为 $($pluginRecords.Count)，预期 1"
        }
        elseif ($pluginRecords[0].metadata.pinned -ne $true) {
            $pluginPathProblem = 'VS Code Claude Code 插件未固定版本，自动更新可能更换未受保护的路径'
        }
        elseif ([string]$pluginRecords[0].metadata.targetPlatform -ne 'win32-x64') {
            $pluginPathProblem = "VS Code Claude Code 插件平台为 $($pluginRecords[0].metadata.targetPlatform)，预期 win32-x64"
        }
        else {
            $pluginRoot = [string]$pluginRecords[0].location.path
            if ($pluginRoot -match '^/([A-Za-z]:/.*)$') { $pluginRoot = $Matches[1] }
            $pluginRoot = $pluginRoot.Replace('/','\')
            $currentPluginPath = Join-Path $pluginRoot 'resources\native-binary\claude.exe'
            if ($pluginTargets[0].Path -ine $currentPluginPath) {
                $pluginPathProblem = "VS Code 插件路径已变化：当前=$currentPluginPath；防护=$($pluginTargets[0].Path)"
            }
        }
    }
    $vsCodeSettings = Join-Path $env:APPDATA 'Code\User\settings.json'
    if (-not $pluginPathProblem -and (Test-Path -LiteralPath $vsCodeSettings)) {
        $settingsText = Get-Content -LiteralPath $vsCodeSettings -Raw
        $wrapperMatch = [regex]::Match($settingsText, '"claudeCode\.claudeProcessWrapper"\s*:\s*"([^"]*)"')
        if ($wrapperMatch.Success -and $wrapperMatch.Groups[1].Value.Trim()) {
            $pluginPathProblem = 'VS Code 配置了 claudeProcessWrapper，实际启动程序不再是受保护的插件 claude.exe'
        }
    }
    $installedKeys = @()
    foreach ($t in $st.Targets) { foreach ($f in $t.Filters) { $installedKeys += [string]$f.Key } }
    $recordReady = ($installedKeys.Count -eq $expectedKeys.Count -and -not $pluginPathProblem)
    $recordEvidence = "安装记录含 $($installedKeys.Count) 条过滤器（清单要求 $($expectedKeys.Count) 条）"
    if ($pluginPathProblem) { $recordEvidence += "；$pluginPathProblem" }
    Add-CheckResult -Status $(if ($recordReady) { 'PASS' } else { 'FAIL' }) -Item '阻断记录数' -Evidence $recordEvidence

    # 缓存新鲜就直接用；缺失或过期就当场发起一次 UAC（见 Common.ps1 的 Update-ElevatedVerifyCache）。
    # 已经在管理员上下文时它不会弹窗，直接干活。用户要做的只有那一下「是」。
    # -Fast 会设 CLAUDE_SAFE_NO_ELEVATE，此时一律不弹窗，只报 WARN。
    $manifestHash = if (Test-Path -LiteralPath $manifestPath) { (Get-FileHash -LiteralPath $manifestPath -Algorithm SHA256).Hash } else { '' }
    $expectedKeySet = @($expectedKeys | ForEach-Object { ([string]$_).ToLowerInvariant() } | Sort-Object)
    $cacheBefore = Read-ElevatedVerifyCache
    $cacheKeySet = @($cacheBefore.WfpKeys | ForEach-Object { ([string]$_).ToLowerInvariant() } | Sort-Object)
    $cacheBindingMismatch = ($cacheBefore -and (
        [int]$cacheBefore.WfpExpected -ne $expectedKeys.Count -or
        [string]$cacheBefore.ManifestSHA256 -ne $manifestHash -or
        [string]$cacheBefore.PkgVersion -ne [string]$man.PackageVersion -or
        ($cacheKeySet -join '|') -cne ($expectedKeySet -join '|')
    ))
    $elev = if ($cacheBindingMismatch) { Update-ElevatedVerifyCache -MaxAgeDays 0 } else { Update-ElevatedVerifyCache -MaxAgeDays 30 }
    $wfpCache = $elev.Cache
    $skip = ''
    if ($elev.Skipped) { $skip = "（$($elev.Skipped)）" }
    elseif ($elev.Error) { $skip = "（自动提权没跑成：$($elev.Error)）" }

    if (-not $wfpCache) {
        Add-CheckResult -Status 'WARN' -Item 'WFP 实盘核对' -Evidence ("没有任何提权核对结论$skip；跑 $refreshHint 点一次「是」，之后 30 天免提权")
    }
    elseif ($elev.Age -ne $null -and $elev.Age -gt 30) {
        Add-CheckResult -Status 'WARN' -Item 'WFP 实盘核对' -Evidence "缓存已 $($elev.Age) 天（有效期 30 天），且刚才的提权复跑没能刷新它$skip"
    }
    elseif ([int]$wfpCache.WfpExpected -ne $expectedKeys.Count -or
            [string]$wfpCache.ManifestSHA256 -ne $manifestHash -or
            [string]$wfpCache.PkgVersion -ne [string]$man.PackageVersion -or
            ((@($wfpCache.WfpKeys | ForEach-Object { ([string]$_).ToLowerInvariant() } | Sort-Object) -join '|') -cne ($expectedKeySet -join '|'))) {
        # 没真正复核过（-Fast 跳过或 UAC 被取消）只能说“未核对”，不能判失败；复核过仍不一致才是 FAIL。
        if ($elev.Refreshed -and -not $elev.Error) {
            Add-CheckResult -Status 'FAIL' -Item 'WFP 实盘核对' -Evidence '提权复核后缓存仍与当前清单哈希、Desktop 版本或规则 GUID 不一致'
        } else {
            Add-CheckResult -Status 'WARN' -Item 'WFP 实盘核对' -Evidence ("防护清单已变更，提权缓存还是旧清单的$skip；跑 $refreshHint 点一次「是」重新核对")
        }
    }
    elseif ($wfpCache.WfpPassed) {
        $detail['wfp'] = @($wfpCache.WfpDetail)
        $detail['wfpCacheAge'] = $elev.Age
        $how = if ($elev.IsAdmin) { '提权直读' } elseif ($elev.Refreshed) { '刚刚提权复核' } else { "$($elev.Age) 天前提权复核" }
        Add-CheckResult -Status 'PASS' -Item 'WFP 实盘核对' -Evidence "实盘 $($wfpCache.WfpFound)/$($wfpCache.WfpExpected) 在位（$how，30 天内有效）"
    }
    else {
        $detail['wfp'] = @($wfpCache.WfpDetail)
        Add-CheckResult -Status 'FAIL' -Item 'WFP 实盘核对' -Evidence "实盘只找到 $($wfpCache.WfpFound)/$($wfpCache.WfpExpected) 条；跑 scripts\desktop\维护文件\应用直连防护管理.ps1 -Action Install 重签"
    }
}
else {
    Add-CheckResult -Status 'FAIL' -Item 'WFP 阻断记录' -Evidence "缺少 $statePath"
}

# ------------------------------------------------- 一键总检状态
Write-Section '桌面快捷方式与自检入口'
$desktop = [Environment]::GetFolderPath('Desktop')
foreach ($n in 'Claude-环境一键总检.lnk', 'Claude-五项自检.lnk', 'Claude-英文浏览器.lnk') {
    $p = Join-Path $desktop $n
    if (-not (Test-Path -LiteralPath $p)) {
        Add-CheckResult -Status 'WARN' -Item $n -Evidence '桌面没有；可用 scripts\apply\Repair-Shortcuts.ps1 -Apply 从原版备份还原'
        continue
    }
    $sh = New-Object -ComObject WScript.Shell
    $sc = $sh.CreateShortcut($p)
    $target = [regex]::Match($sc.Arguments, '-File\s+"?([^"]+\.ps1)"?').Groups[1].Value
    if (-not $target) {
        Add-CheckResult -Status 'WARN' -Item $n -Evidence '参数里没有 -File <脚本>'
    }
    elseif (Test-Path -LiteralPath $target) {
        # 桌面快捷方式是用户资产：只要 -File 指向的脚本真实存在就算可用。
        # 指回 docs\配置\（原始基线）或指 skill 内等价脚本都正常，不做位置要求。
        $where = if ($target -like "$root*") { 'skill 内等价脚本' } else { '原始基线位置' }
        Add-CheckResult -Status 'PASS' -Item $n -Evidence "[$where] $target"
    }
    else {
        Add-CheckResult -Status 'FAIL' -Item $n -Evidence "指向的脚本不存在：$target（跑 scripts\apply\Repair-Shortcuts.ps1 -Apply 从原版备份还原）"
    }
}

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows -FailHint '系统兜底有缺口 —— 关掉 TUN 时可能真实泄漏。' -WarnHint '有提醒项；建议用管理员终端复跑本脚本以完成 WFP 核对。'

if ($Json) {
    Write-JsonResult @{ script = 'Check-WindowsFallback.ps1'; ok = ($code -eq 0); detail = $detail; results = $rows }
}
exit $code
