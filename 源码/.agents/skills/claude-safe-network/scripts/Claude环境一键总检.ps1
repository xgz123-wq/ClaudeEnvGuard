#requires -Version 7.0
[CmdletBinding()]
param(
  [switch] $OpenBrowser,
  [switch] $Json
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)

# 公共库：端口 / 组名 / 阈值唯一真源
$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

$scriptsRoot    = $PSScriptRoot
$validator      = Join-Path $scriptsRoot 'clash\验证Clash账号专用通道.ps1'
$guardDirectory = Join-Path $scriptsRoot 'desktop\维护文件'
$portMixed      = $Ports.Mixed
$portChrome     = $Ports.ChromeSocks
$portDesktop    = $Ports.DesktopHttp
$runtime = Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev\clash-verge.yaml'
$desktop = [Environment]::GetFolderPath('Desktop')
$guardManifest = Join-Path $guardDirectory '应用过滤目标.json'
$guardState = Join-Path $guardDirectory '应用直连防护状态.json'
$guardVerify = Join-Path $guardDirectory '应用直连防护-Verify.json'
$guardInstall = Join-Path $guardDirectory '应用直连防护-最近操作.json'
$rangeCheck = Join-Path $scriptsRoot 'apply\Anthropic网段直连阻断.ps1'
$desktopConfigRoot = Join-Path $env:LOCALAPPDATA 'Claude-3p\configLibrary'
$desktopMeta = Join-Path $desktopConfigRoot '_meta.json'
$expectedDesktopProxy = "http://127.0.0.1:$portDesktop"
$machineClaudePolicy = Get-ItemProperty -Path 'HKLM:\SOFTWARE\Policies\Claude' -ErrorAction SilentlyContinue
$userClaudePolicy = Get-ItemProperty -Path 'HKCU:\SOFTWARE\Policies\Claude' -ErrorAction SilentlyContinue

$results = [System.Collections.Generic.List[object]]::new()

function Add-Result {
  param(
    [ValidateSet('PASS','WARN','FAIL','MANUAL')][string] $Status,
    [string] $Item,
    [string] $Evidence
  )
  $results.Add([pscustomobject]@{Status=$Status;Item=$Item;Evidence=$Evidence})
}

function Test-ProxyExit {
  param(
    [ValidateSet('http','socks5h')][string] $Type,
    [int] $Port
  )
  $arguments = @('-sS','--max-time','12')
  if ($Type -eq 'socks5h') {
    $arguments += @('--socks5-hostname', "127.0.0.1:$Port")
  } else {
    $arguments += @('--proxy', "http://127.0.0.1:$Port")
  }
  $arguments += 'https://api.ipify.org'
  $body = (& curl.exe @arguments 2>$null | Out-String).Trim()
  [pscustomobject]@{Succeeded=($LASTEXITCODE -eq 0 -and $body -match '^\d{1,3}(\.\d{1,3}){3}$');IP=$body}
}

function Read-Shortcut {
  param([string] $Path)
  if (-not (Test-Path -LiteralPath $Path)) { return $null }
  $shell = New-Object -ComObject WScript.Shell
  $shortcut = $shell.CreateShortcut($Path)
  [pscustomobject]@{Path=$Path;Target=$shortcut.TargetPath;Arguments=[string]$shortcut.Arguments}
}

function Test-GuardRecord {
  param(
    $RecordedTargets,
    $ManifestTargets,
    [switch]$RequireBinaryUnchanged
  )
  if (@($RecordedTargets).Count -ne @($ManifestTargets).Count) { return $false }
  foreach ($manifestTarget in @($ManifestTargets)) {
    $recordedTarget = @($RecordedTargets | Where-Object Path -eq $manifestTarget.Path)
    if ($recordedTarget.Count -ne 1) { return $false }
    if ($RequireBinaryUnchanged -and $recordedTarget[0].BinaryUnchanged -ne $true) { return $false }
    $filters = @($recordedTarget[0].Filters)
    if ($filters.Count -ne 2) { return $false }
    $expectedKeys = @([string]$manifestTarget.V4,[string]$manifestTarget.V6)
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

Write-Host ''
Write-Host 'Claude / Clash 环境一键总检' -ForegroundColor Cyan
Write-Host ('检查时间：{0}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss zzz'))
Write-Host '本工具只读检查；不会启动 Claude Code 或 Claude Desktop。' -ForegroundColor DarkGray

if (-not (Test-Path -LiteralPath $validator)) {
  Add-Result FAIL '基础验证程序' "缺少文件：$validator"
} else {
  $engine = (Get-Process -Id $PID).Path
  $output = @(& $engine -NoProfile -ExecutionPolicy Bypass -File $validator 2>&1)
  $raw = $output | Where-Object { $_ -isnot [System.Management.Automation.ErrorRecord] } | Out-String
  $stderrLine = $output | Where-Object { $_ -is [System.Management.Automation.ErrorRecord] } | Select-Object -First 1
  try {
    $baseResult = $raw | ConvertFrom-Json
    if ($baseResult.Success) {
      Add-Result PASS 'Rule / TUN / Claude 规则' '基础验证 18/18；Clash 配置语法通过'
      Add-Result PASS '国内连通' "飞书 HTTP $($baseResult.FeishuHttpStatus)"
      Add-Result PASS 'ACCOUNT-US' "当前节点：$($baseResult.AccountNode)；$portChrome 出口：$($baseResult.SocksPublicIp)"
    } else {
      Add-Result FAIL 'Rule / TUN / Claude 规则' ('失败项：' + ($baseResult.FailedChecks -join ', '))
    }
  } catch {
    Add-Result FAIL '基础验证程序' ('输出不是有效 JSON' + $(if ($stderrLine) { "；stderr：$stderrLine" } else { '' }))
    $baseResult = $null
  }
}

if (Test-Path -LiteralPath $runtime) {
  $runtimeText = Get-Content -LiteralPath $runtime -Raw
  $strictRoute = $runtimeText -match '(?m)^\s*strict-route:\s*true\s*$'
  $dnsUdp = $runtimeText -match '(?m)^\s*-\s*any:53\s*$'
  $dnsTcp = $runtimeText -match '(?m)^\s*-\s*tcp://any:53\s*$'
  if ($strictRoute -and $dnsUdp -and $dnsTcp) {
    Add-Result PASS 'DNS 防旁路' 'strict-route=true；UDP/TCP 53 劫持已开启'
  } else {
    Add-Result FAIL 'DNS 防旁路' 'strict-route 或 UDP/TCP 53 劫持缺失'
  }
} else {
  Add-Result FAIL 'Clash 运行配置' "缺少文件：$runtime"
}

$exit65 = Test-ProxyExit http $portMixed
$exit66 = Test-ProxyExit socks5h $portChrome
$exit67 = Test-ProxyExit http $portDesktop
foreach ($entry in @(
  @{Name="Claude Code $portMixed";Value=$exit65},
  @{Name="Claude Chrome $portChrome";Value=$exit66},
  @{Name="Claude Desktop $portDesktop";Value=$exit67}
)) {
  if ($entry.Value.Succeeded) {
    Add-Result PASS $entry.Name ("出口：{0}" -f $entry.Value.IP)
  } else {
    Add-Result FAIL $entry.Name '专用入口无法取得有效公网 IPv4'
  }
}
$exitIps = @($exit65.IP,$exit66.IP,$exit67.IP) | Where-Object { $_ }
if ($exitIps.Count -eq 3 -and @($exitIps | Select-Object -Unique).Count -eq 1) {
  Add-Result PASS 'Claude 三入口一致性' "三个入口均为 $($exitIps[0])"
} else {
  Add-Result FAIL 'Claude 三入口一致性' ("$portMixed/$portChrome/$portDesktop：" + ($exitIps -join ' / '))
}

$browserShortcuts = @(
  (Join-Path $desktop 'Claude-账号1-专用Chrome.lnk'),
  (Join-Path $desktop 'Claude-账号2-专用Chrome.lnk')
)
$readyBrowser = $null
foreach ($shortcutPath in $browserShortcuts) {
  $shortcut = Read-Shortcut $shortcutPath
  if ($null -eq $shortcut) {
    Add-Result WARN ([IO.Path]::GetFileName($shortcutPath)) '快捷方式不存在'
    continue
  }
  $arguments = $shortcut.Arguments
  $ready =
    ($arguments -like '*--user-data-dir=*') -and
    ($arguments -like "*127.0.0.1:$portChrome*") -and
    ($arguments -like '*--host-resolver-rules=*') -and
    ($arguments -like '*--lang=en-US*') -and
    ($arguments -like '*disable_non_proxied_udp*')
  if ($ready) {
    Add-Result PASS ([IO.Path]::GetFileName($shortcutPath)) "独立 User Data、$portChrome、远端 DNS、en-US、WebRTC 防旁路参数齐全"
    if ($null -eq $readyBrowser) { $readyBrowser = $shortcutPath }
  } else {
    Add-Result FAIL ([IO.Path]::GetFileName($shortcutPath)) '专用浏览器参数不完整'
  }
}

if (Test-Path -LiteralPath $desktopMeta) {
  try {
    $meta = Get-Content -LiteralPath $desktopMeta -Raw | ConvertFrom-Json
    $profilePath = Join-Path $desktopConfigRoot ($meta.appliedId + '.json')
    $profile = Get-Content -LiteralPath $profilePath -Raw | ConvertFrom-Json
    if ($null -ne $machineClaudePolicy.disableAutoUpdates) {
      if ($machineClaudePolicy.egressProxyUrl -eq $expectedDesktopProxy) {
        Add-Result PASS 'Claude Desktop 本地代理' "HKLM egressProxyUrl=$expectedDesktopProxy"
      } else {
        Add-Result FAIL 'Claude Desktop 本地代理' "HKLM 设置了应用行为策略，但同层缺少 $portDesktop；本地配置会被忽略"
      }
    } elseif ($null -ne $userClaudePolicy.disableAutoUpdates) {
      if ($userClaudePolicy.egressProxyUrl -eq $expectedDesktopProxy) {
        Add-Result PASS 'Claude Desktop 本地代理' "HKCU egressProxyUrl=$expectedDesktopProxy"
      } else {
        Add-Result FAIL 'Claude Desktop 本地代理' "HKCU 设置了应用行为策略，但同层缺少 $portDesktop；本地配置会被忽略"
      }
    } elseif ($profile.egressProxyUrl -eq $expectedDesktopProxy) {
      Add-Result PASS 'Claude Desktop 本地代理' "本地 egressProxyUrl=$expectedDesktopProxy"
    } else {
      Add-Result FAIL 'Claude Desktop 本地代理' "当前应用配置没有指向 $portDesktop"
    }
  } catch {
    Add-Result FAIL 'Claude Desktop 本地代理' '无法读取当前应用配置'
  }
} else {
  Add-Result FAIL 'Claude Desktop 本地代理' '缺少 configLibrary 元数据'
}

$machineUpdatePolicy = $machineClaudePolicy.disableAutoUpdates
$userUpdatePolicy = $userClaudePolicy.disableAutoUpdates
if ($null -ne $machineUpdatePolicy) {
  if ([int]$machineUpdatePolicy -eq 1 -and $machineClaudePolicy.egressProxyUrl -eq $expectedDesktopProxy) {
    Add-Result PASS 'Claude Desktop 自动更新' "HKLM 已禁更，并在同层保留 $portDesktop 代理"
  } elseif ([int]$machineUpdatePolicy -eq 1) {
    Add-Result FAIL 'Claude Desktop 自动更新' "HKLM 已禁更，但同层缺少 $portDesktop 代理，会导致网络被直连防护拒绝"
  } else {
    Add-Result WARN 'Claude Desktop 自动更新' 'HKLM 策略允许自动更新，并覆盖当前用户设置'
  }
} elseif ([int]$userUpdatePolicy -eq 1 -and $userClaudePolicy.egressProxyUrl -eq $expectedDesktopProxy) {
  Add-Result PASS 'Claude Desktop 自动更新' "HKCU 已禁更，并在同层保留 $portDesktop 代理"
} elseif ([int]$userUpdatePolicy -eq 1) {
  Add-Result FAIL 'Claude Desktop 自动更新' "HKCU 已禁更，但同层缺少 $portDesktop 代理，会导致网络被直连防护拒绝"
} else {
  Add-Result WARN 'Claude Desktop 自动更新' '未禁用；Claude Desktop 默认约每 4 小时检查更新'
}

if (Test-Path -LiteralPath $guardManifest) {
  try {
    $manifest = Get-Content -LiteralPath $guardManifest -Raw | ConvertFrom-Json
    $manifestHash = (Get-FileHash -LiteralPath $guardManifest -Algorithm SHA256).Hash
    $package = Get-AppxPackage -Name Claude -ErrorAction SilentlyContinue
    $targets = @($manifest.Targets)
    $pluginTargets = @($targets | Where-Object Name -eq 'Claude Code VS Code 插件')
    $pluginRegistry = Join-Path $env:USERPROFILE '.vscode\extensions\extensions.json'
    $currentPluginPath = $null
    $pluginPathProblem = ''
    if (-not (Test-Path -LiteralPath $pluginRegistry)) {
      $pluginPathProblem = '缺少 VS Code extensions.json，无法确认当前插件路径'
    } else {
      $pluginRecords = @((Get-Content -LiteralPath $pluginRegistry -Raw | ConvertFrom-Json) | Where-Object { $_.identifier.id -ieq 'anthropic.claude-code' })
      if ($pluginRecords.Count -ne 1) {
        $pluginPathProblem = "VS Code 当前登记的 Claude Code 插件数量为 $($pluginRecords.Count)，预期 1"
      } elseif ($pluginRecords[0].metadata.pinned -ne $true) {
        $pluginPathProblem = 'VS Code Claude Code 插件未固定版本，自动更新可能更换未受保护的路径'
      } elseif ([string]$pluginRecords[0].metadata.targetPlatform -ne 'win32-x64') {
        $pluginPathProblem = "VS Code Claude Code 插件平台为 $($pluginRecords[0].metadata.targetPlatform)，预期 win32-x64"
      } else {
        $pluginRoot = [string]$pluginRecords[0].location.path
        if ($pluginRoot -match '^/([A-Za-z]:/.*)$') { $pluginRoot = $Matches[1] }
        $pluginRoot = $pluginRoot.Replace('/','\')
        $currentPluginPath = Join-Path $pluginRoot 'resources\native-binary\claude.exe'
      }
    }
    if ($pluginTargets.Count -ne 1) {
      $pluginPathProblem = "防护清单里的 VS Code 插件目标数量为 $($pluginTargets.Count)，预期 1"
    } elseif ($currentPluginPath -and $pluginTargets[0].Path -ine $currentPluginPath) {
      $pluginPathProblem = "VS Code 插件路径已变化：当前=$currentPluginPath；防护=$($pluginTargets[0].Path)"
    }
    $vsCodeSettings = Join-Path $env:APPDATA 'Code\User\settings.json'
    if (-not $pluginPathProblem -and (Test-Path -LiteralPath $vsCodeSettings)) {
      $settingsText = Get-Content -LiteralPath $vsCodeSettings -Raw
      $wrapperMatch = [regex]::Match($settingsText, '"claudeCode\.claudeProcessWrapper"\s*:\s*"([^"]*)"')
      if ($wrapperMatch.Success -and $wrapperMatch.Groups[1].Value.Trim()) {
        $pluginPathProblem = 'VS Code 配置了 claudeProcessWrapper，实际启动程序不再是受保护的插件 claude.exe'
      }
    }
    $validTargets = @($targets | Where-Object {
      (Test-Path -LiteralPath $_.Path) -and
      ((Get-FileHash -LiteralPath $_.Path -Algorithm SHA256).Hash -eq $_.SHA256)
    })
    $state = if (Test-Path -LiteralPath $guardState) { Get-Content -LiteralPath $guardState -Raw | ConvertFrom-Json } else { $null }
    $verify = if (Test-Path -LiteralPath $guardVerify) { Get-Content -LiteralPath $guardVerify -Raw | ConvertFrom-Json } else { $null }
    $install = if (Test-Path -LiteralPath $guardInstall) { Get-Content -LiteralPath $guardInstall -Raw | ConvertFrom-Json } else { $null }
    $stateReady =
      ($null -ne $state) -and
      ([string]$state.Status -like 'installed_*') -and
      ($state.ManifestSHA256 -eq $manifestHash) -and
      (@($state.Targets).Count -eq $targets.Count)
    $verifyReady =
      ($null -ne $verify) -and
      ($verify.Status -eq 'passed') -and
      ($verify.ManifestSHA256 -eq $manifestHash) -and
      ($verify.PackageVersionMatches -eq $true) -and
      (Test-GuardRecord -RecordedTargets $verify.Targets -ManifestTargets $targets -RequireBinaryUnchanged)
    $installReady =
      ($null -ne $install) -and
      ($install.Action -eq 'Install') -and
      ($install.Status -eq 'passed') -and
      (Test-GuardRecord -RecordedTargets $install.Installed -ManifestTargets $targets)
    if ($null -eq $package) {
      Add-Result FAIL 'Claude Desktop / Code 直连防护' '未检测到已安装的 Claude Desktop 包'
    } elseif ($package.Version.ToString() -ne $manifest.PackageVersion) {
      Add-Result FAIL 'Claude Desktop / Code 直连防护' "Desktop=$($package.Version)，防护清单=$($manifest.PackageVersion)；升级后的断线防直连尚未复验"
    } elseif ($pluginPathProblem) {
      Add-Result FAIL 'Claude Desktop / Code 直连防护' $pluginPathProblem
    } elseif ($validTargets.Count -ne $targets.Count) {
      Add-Result FAIL 'Claude Desktop / Code 直连防护' ("文件基线仅 {0}/{1} 一致；需复验：{2}。英文启动器会拒绝启动，请先维护。" -f $validTargets.Count,$targets.Count,((@($targets | Where-Object { $_.Path -notin @($validTargets.Path) }) | ForEach-Object Name) -join '、'))
    } elseif (-not $stateReady) {
      Add-Result FAIL 'Claude Desktop / Code 直连防护' '当前安装状态与最新防护清单不一致'
    } elseif (-not $verifyReady -and -not $installReady) {
      Add-Result FAIL 'Claude Desktop / Code 直连防护' '缺少与当前清单一致的管理员安装或 Verify 凭证'
    } else {
      $evidenceType = if ($verifyReady) { '管理员 Verify' } else { '管理员安装记录' }
      $ruleCount = $targets.Count * 2
      Add-Result PASS 'Claude Desktop / Code 直连防护' "Desktop=$($package.Version)；目标哈希 $($validTargets.Count)/$($targets.Count)；$ruleCount 条精确阻断规则有$evidenceType"
    }
  } catch {
    Add-Result FAIL 'Claude Desktop / Code 直连防护' ('防护记录无法完整核验：' + $_.Exception.Message)
  }
} else {
  Add-Result FAIL 'Claude Desktop / Code 直连防护' "缺少防护清单：$guardManifest"
}

if (-not (Test-Path -LiteralPath $rangeCheck)) {
  Add-Result FAIL 'Anthropic 网段直连阻断' "缺少检查脚本：$rangeCheck"
} else {
  try {
    $engine = (Get-Process -Id $PID).Path
    $rangeResult = (& $engine -NoProfile -ExecutionPolicy Bypass -File $rangeCheck -Action Check -Json 2>$null | Out-String) | ConvertFrom-Json
    if ($rangeResult.Succeeded) { Add-Result PASS 'Anthropic 网段直连阻断' $rangeResult.Evidence }
    else { Add-Result FAIL 'Anthropic 网段直连阻断' $rangeResult.Evidence }
  } catch {
    Add-Result FAIL 'Anthropic 网段直连阻断' ('检查失败：' + $_.Exception.Message)
  }
}

# 检测站自动读数：真实去 ping0.cc / ip.net.coffee 渲染取数后判定（iprisk.top 在第 7 层，ippure.com 无头拿不到）。
# 它只看出口 IP 读数，不代表专用 Chrome / Desktop 里登录、DNS、WebRTC、IPv6、语言和时区都正常，所以网页验收仍是人工项。
$web = Get-WebAcceptanceVerdict
Add-Result $web.Status '检测站自动读数' $web.Evidence
Add-Result MANUAL '网页最终验收' '专用 Chrome / Desktop 内置浏览器分别确认：Claude IP、DNS、WebRTC、IPv6、语言和时区全绿'

if ($Json) {
  # 给上层编排器（Invoke-ClaudeSafetyCheck.ps1）消费的机器可读输出
  $jsonFail = @($results | Where-Object Status -eq 'FAIL').Count
  Write-JsonResult ([pscustomobject]@{
    script  = 'Claude环境一键总检.ps1'
    time    = (Get-Date -Format 'o')
    ok      = ($jsonFail -eq 0)
    pass    = @($results | Where-Object Status -eq 'PASS').Count
    warn    = @($results | Where-Object Status -eq 'WARN').Count
    fail    = $jsonFail
    manual  = @($results | Where-Object Status -eq 'MANUAL').Count
    results = $results
  })
  if ($jsonFail -gt 0) { exit 1 }
  exit 0
}

Write-Host ''
foreach ($row in $results) {
  $color = switch ($row.Status) {
    'PASS' {'Green'}
    'WARN' {'Yellow'}
    'FAIL' {'Red'}
    default {'Cyan'}
  }
  Write-Host ('[{0,-6}] {1}' -f $row.Status,$row.Item) -ForegroundColor $color
  Write-Host ('         {0}' -f $row.Evidence)
}

$failCount = @($results | Where-Object Status -eq 'FAIL').Count
$warnCount = @($results | Where-Object Status -eq 'WARN').Count
Write-Host ''
if ($failCount -gt 0) {
  Write-Host "结论：不建议启动 Claude；存在 $failCount 个失败项、$warnCount 个提醒项。" -ForegroundColor Red
  exit 1
}
if ($warnCount -gt 0) {
  Write-Host "结论：日常运行检查通过；有 $warnCount 个防护提醒，不能把它们当成已验收。" -ForegroundColor Yellow
} else {
  Write-Host '结论：程序层检查全部通过；网页全绿后可进入日常使用。' -ForegroundColor Green
}

if ($OpenBrowser) {
  if ($null -eq $readyBrowser) {
    Write-Host '没有找到参数完整的专用 Chrome，未启动浏览器。' -ForegroundColor Red
    exit 1
  }
  Start-Process -FilePath $readyBrowser
  Write-Host ("已打开专用检测浏览器：{0}" -f ([IO.Path]::GetFileName($readyBrowser))) -ForegroundColor Cyan
}
