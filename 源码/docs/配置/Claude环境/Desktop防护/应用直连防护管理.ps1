#requires -Version 7.0
param(
 [ValidateSet('Test','Install','Verify','Rollback','Refresh','MigratePlugin')][string]$Action='Verify',
 # Refresh 只适用于路径、Desktop 版本均未变化；必须显式传入人工核对过的目标哈希（按清单顺序）。
 # MigratePlugin 只传一个：VS Code 当前登记的新插件 claude.exe 的已审核哈希。
 [string[]]$ApprovedSHA256,
 # 同路径维护可显式跳过联网探针；仍核验签名、全部现有规则及刷新后的文件基线。
 [switch]$SkipNetworkProbe
)
$ErrorActionPreference='Stop'
$helper=Join-Path $PSScriptRoot '应用直连过滤.cs'
$manifestPath=Join-Path $PSScriptRoot '应用过滤目标.json'
$statePath=Join-Path $PSScriptRoot '应用直连防护状态.json'
$reportName=if($Action -eq 'Verify'){'应用直连防护-Verify.json'}elseif($Action -eq 'Refresh'){'应用直连防护-Refresh.json'}else{'应用直连防护-最近操作.json'}
$reportPath=Join-Path $PSScriptRoot $reportName
$probePath=Join-Path $env:LOCALAPPDATA 'ClaudeNetworkGuard\claude-network-probe.exe'
$probeScript=Join-Path $PSScriptRoot '独立网络探针.cjs'
$probe4=[guid]'72eeed17-9129-4be6-a40d-72e9a1e05b34'
$probe6=[guid]'8ba52bf3-c742-42bb-9c13-eb14ba66d2ea'
$record=[ordered]@{At=(Get-Date -Format o);Action=$Action;Status='running';ClaudeLaunched=$false;GlobalNetworkChanged=$false}
function Save-Report { $record|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $reportPath -Encoding utf8 }
function Assert-Closed {
 if(@(Get-Process -Name Claude -ErrorAction SilentlyContinue).Count){throw 'Claude 正在运行，停止配置；本脚本不会启动或结束 Claude。'}
}
function Select-Filter($filters) {
 @($filters|Select-Object Key,Layer,Id,Flags,NativeAppPath,Enabled,Persistent,ProviderIsNull,ExactAppIdCondition,NonLoopbackCondition,IsOwnedExactBlock)
}
function Invoke-Probe([string]$Executable) {
 $probeOutput=& $Executable $probeScript
 if($LASTEXITCODE -ne 0){throw '独立测试程序执行失败。'}
 return ($probeOutput|ConvertFrom-Json)
}
function Resolve-ControlProbe {
 # 对照程序：PATH 里第一个不是测试探针的 node.exe，证明未受保护的程序不受影响；不写死本机安装位置。
 $found=@(Get-Command node.exe -CommandType Application -All -ErrorAction SilentlyContinue|ForEach-Object Source|Where-Object {$_ -and $_ -ine $probePath})
 if($found.Count -eq 0){throw '找不到对照用的 node.exe：请确认 Node.js 已安装并在 PATH 中。'}
 return $found[0]
}
function Test-Guard {
 Assert-Closed
 if(-not(Test-Path -LiteralPath $probePath)){throw '独立测试程序不存在。'}
 if(Get-NetTCPConnection -State Listen -LocalPort 43997 -ErrorAction SilentlyContinue){throw '测试断线端口被占用，停止。'}
 $record.Test=[ordered]@{Probe=$probePath;Before=(Invoke-Probe $probePath)}
 if(-not $record.Test.Before.results.directTcp4.ok -or -not $record.Test.Before.results.nativeDnsDefault.ok -or -not $record.Test.Before.results.localProxy.ok -or -not $record.Test.Before.results.stunUdp4.ok){throw '测试基线不满足要求，不能把原有网络故障误判为阻断成功。'}
 try {
  $record.Test.Filters=Select-Filter ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Add($probePath,$probe4,$probe6,$true))
  # Add 返回前已关闭其 WFP 会话；这里测试持久规则不依赖安装程序常驻。
  $record.Test.Protected=Invoke-Probe $probePath
  $record.Test.Control=Invoke-Probe (Resolve-ControlProbe)
 } finally {
  $record.Test.Removed=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Remove($probe4,$probe6)
 }
 $record.Test.After=Invoke-Probe $probePath
 $r=$record.Test.Protected.results
 $checks=[ordered]@{
  DirectTcp4Denied=(-not $r.directTcp4.ok -and $r.directTcp4.code -eq 'EACCES')
  DirectTcpDnsDenied=(-not $r.directTcpDns.ok -and $r.directTcpDns.code -eq 'EACCES')
  StunUdp4Blocked=(-not $r.stunUdp4.ok -and $r.stunUdp4.code -in @('TIMEOUT','EACCES'))
  NativeDnsDefaultFailed=(-not $r.nativeDnsDefault.ok)
  NativeDnsPublicFailed=(-not $r.nativeDnsPublic.ok)
  ProxyHttpsSucceeded=$r.localProxy.ok
  AbsentProxyFailed=(-not $r.absentProxy.ok -and $r.absentProxy.code -eq 'ECONNREFUSED')
  UnfilteredControlUnaffected=($record.Test.Control.results.directTcp4.ok -and $record.Test.Control.results.nativeDnsDefault.ok -and $record.Test.Control.results.localProxy.ok -and $record.Test.Control.results.stunUdp4.ok)
  ProbeRestored=($record.Test.After.results.directTcp4.ok -and $record.Test.After.results.nativeDnsDefault.ok -and $record.Test.After.results.localProxy.ok -and $record.Test.After.results.stunUdp4.ok)
  ProbeFiltersRemoved=($null -eq [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get($probe4) -and $null -eq [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get($probe6))
 }
 $record.Test.Checks=$checks
 $record.Test.UdpNote='UDP send callback does not prove delivery. Acceptance uses valid STUN replies before/control/after and failure while filtered, plus DNS UDP failures.'
 $record.Test.IPv6='Baseline had no usable IPv6 route; filter presence verified, IPv6 packet enforcement not proven.'
 $record.Test.Passed=-not (@($checks.Values|Where-Object {$_ -ne $true}).Count)
 Save-Report
 if(-not $record.Test.Passed){throw '独立过滤验收有未通过项目，没有安装 Claude 规则。'}
}
try {
 if($SkipNetworkProbe -and $Action -ne 'Refresh'){throw 'SkipNetworkProbe 仅适用于 Refresh。'}
 Add-Type -LiteralPath $helper
 $record.Abi=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::ValidateAbi()
 $manifest=Get-Content -LiteralPath $manifestPath -Raw|ConvertFrom-Json
 if($manifest.Targets.Count -lt 1){throw '目标清单为空，需要重新审核。'}
 $targetPaths=@($manifest.Targets|ForEach-Object {$_.Path})
 if(@($targetPaths|Select-Object -Unique).Count -ne $manifest.Targets.Count){throw '目标清单包含重复程序路径。'}
 $keys=[guid[]]@($manifest.Targets|ForEach-Object {[guid]$_.V4;[guid]$_.V6})
 if(@($keys|Select-Object -Unique).Count -ne ($manifest.Targets.Count*2)){throw 'GUID 清单无效。'}
 if($Action -in @('Test','Install','Rollback','Refresh','MigratePlugin')){
  Assert-Closed
  if(-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw '写入系统过滤规则需要管理员权限。'}
 }
 if($Action -eq 'Refresh'){
  if((Get-AppxPackage -Name Claude).Version.ToString() -ne $manifest.PackageVersion){throw 'Desktop 版本已变，请使用版本迁移流程，不能只刷新哈希。'}
  if(@($ApprovedSHA256).Count -ne $manifest.Targets.Count){throw '请按清单顺序提供全部已审核的 SHA256，禁止自动接受未知文件。'}
  $oldState=Get-Content -LiteralPath $statePath -Raw|ConvertFrom-Json
  if($oldState.Status -notlike 'installed_*' -or $oldState.ManifestSHA256 -ne (Get-FileHash -LiteralPath $manifestPath).Hash){throw '原安装状态与清单不一致，停止刷新。'}
  $verified=@(for($i=0;$i -lt $manifest.Targets.Count;$i++){
   $target=$manifest.Targets[$i]
   $actual=(Get-FileHash -LiteralPath $target.Path -Algorithm SHA256).Hash
   if($ApprovedSHA256[$i] -notmatch '^[A-Fa-f0-9]{64}$' -or $actual -ne $ApprovedSHA256[$i]){throw ('文件与已审核哈希不符：'+$target.Name)}
   if($actual -ne $target.SHA256){
    $signature=Get-AuthenticodeSignature -LiteralPath $target.Path
    if($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O="?Anthropic, PBC"?(,|$)'){throw ('新文件缺少有效的 Anthropic 签名：'+$target.Name)}
   }
   [pscustomobject]@{Name=$target.Name;Path=$target.Path;Filters=(Select-Filter ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Verify($target.Path,[guid]$target.V4,[guid]$target.V6,$true)))}
  })
  $record.ExistingRulesVerified=$verified
  # 仅在独立探针上添加/撤销测试规则；Claude 的现有规则始终保留。
  if($SkipNetworkProbe){
   $record.Test=[ordered]@{Status='skipped';Reason='同路径基线维护，仅核验现有规则与文件；未执行联网阻断测试。'}
  }else{Test-Guard}
  Assert-Closed
  for($i=0;$i -lt $manifest.Targets.Count;$i++){
   if((Get-FileHash -LiteralPath $manifest.Targets[$i].Path).Hash -ne $ApprovedSHA256[$i]){throw '测试期间程序文件变化，停止刷新。'}
  }
  $backup=Join-Path $env:LOCALAPPDATA ('ClaudeNetworkGuard\基线刷新备份-'+(Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
  $null=New-Item -ItemType Directory -Path $backup
  $verifyPath=Join-Path $PSScriptRoot '应用直连防护-Verify.json'
  $restorePaths=@($manifestPath,$statePath,$verifyPath)|Where-Object {Test-Path -LiteralPath $_}
  foreach($path in $restorePaths){Copy-Item -LiteralPath $path -Destination $backup}
  $record.Backup=$backup
  try {
   for($i=0;$i -lt $manifest.Targets.Count;$i++){$manifest.Targets[$i].SHA256=$ApprovedSHA256[$i].ToUpperInvariant()}
   $manifest.PreparedAt=Get-Date -Format o
   $manifest.Status='refreshed_same_paths_pending_desktop_e2e'
   $manifest|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $manifestPath -Encoding utf8
   $oldState.ManifestSHA256=(Get-FileHash -LiteralPath $manifestPath).Hash
   $oldState.Targets=$verified
   $oldState|Add-Member -NotePropertyName RevalidatedAt -NotePropertyValue (Get-Date -Format o) -Force
   $oldState|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $statePath -Encoding utf8
   & (Join-Path $PSHOME 'pwsh.exe') -NoProfile -File $PSCommandPath -Action Verify
   if($LASTEXITCODE -ne 0){throw '刷新后的管理员 Verify 未通过。'}
  } catch {
   foreach($path in $restorePaths){Copy-Item -LiteralPath (Join-Path $backup ([IO.Path]::GetFileName($path))) -Destination $path -Force}
   throw
  }
 }
 if($Action -eq 'MigratePlugin'){
  # 插件升级换版本目录：先给新路径加规则并核验，清单与状态一起改，最后才撤旧规则；不跑联网探针，与 Desktop 迁移同一做法。
  if(@($ApprovedSHA256).Count -ne 1 -or $ApprovedSHA256[0] -notmatch '^[A-Fa-f0-9]{64}$'){throw 'MigratePlugin 需要且只需要一个已审核的新插件 SHA256。'}
  $approved=$ApprovedSHA256[0].ToUpperInvariant()
  $registry=Join-Path $env:USERPROFILE '.vscode\extensions\extensions.json'
  $records=@((Get-Content -LiteralPath $registry -Raw|ConvertFrom-Json)|Where-Object {$_.identifier.id -ieq 'anthropic.claude-code'})
  if($records.Count -ne 1 -or $records[0].metadata.pinned -ne $true -or [string]$records[0].metadata.targetPlatform -ne 'win32-x64'){throw 'VS Code 登记的 Claude Code 插件须唯一、已固定版本且为 win32-x64。'}
  $pluginRoot=[string]$records[0].location.path
  if($pluginRoot -match '^/([A-Za-z]:/.*)$'){$pluginRoot=$Matches[1]}
  $newPath=Join-Path $pluginRoot.Replace('/','\') 'resources\native-binary\claude.exe'
  $pluginTargets=@($manifest.Targets|Where-Object {$_.Path -match '\\\.vscode\\extensions\\anthropic\.claude-code-[^\\]+\\resources\\native-binary\\claude\.exe$'})
  if($pluginTargets.Count -ne 1){throw '清单里应恰好有一个 VS Code 插件目标。'}
  $old=$pluginTargets[0]|ConvertTo-Json -Depth 5|ConvertFrom-Json
  if($old.Path -ieq $newPath){throw '插件路径没有变化；同路径换文件请用 Refresh。'}
  if((Get-FileHash -LiteralPath $newPath -Algorithm SHA256).Hash -ne $approved){throw '新插件程序与已审核哈希不符。'}
  $signature=Get-AuthenticodeSignature -LiteralPath $newPath
  if($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Subject -notmatch 'O="?Anthropic, PBC"?(,|$)'){throw '新插件程序缺少有效的 Anthropic 签名。'}
  $state=Get-Content -LiteralPath $statePath -Raw|ConvertFrom-Json
  if($state.Status -notlike 'installed_*' -or $state.ManifestSHA256 -ne (Get-FileHash -LiteralPath $manifestPath).Hash){throw '原安装状态与清单不一致，停止迁移。'}
  # 旧插件目录可能已被 VS Code 清掉，旧规则用 GUID 读回核对；其余三个目标照常按路径核验。
  foreach($key in @([guid]$old.V4,[guid]$old.V6)){$f=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get($key);if($null -eq $f -or -not $f.IsOwnedExactBlock -or -not $f.Enabled){throw '旧插件规则缺失或被改动，停止迁移。'}}
  $others=@($manifest.Targets|Where-Object {$_.Path -ine $old.Path}|ForEach-Object {[pscustomobject]@{Name=$_.Name;Path=$_.Path;Filters=(Select-Filter ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Verify($_.Path,[guid]$_.V4,[guid]$_.V6,$true)))}})
  $backup=Join-Path $env:LOCALAPPDATA ('ClaudeNetworkGuard\插件迁移-'+(Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
  $null=New-Item -ItemType Directory -Path $backup
  $verifyPath=Join-Path $PSScriptRoot '应用直连防护-Verify.json'
  $restorePaths=@($manifestPath,$statePath,$verifyPath)|Where-Object {Test-Path -LiteralPath $_}
  foreach($path in $restorePaths){Copy-Item -LiteralPath $path -Destination $backup}
  $newV4=[guid]::NewGuid();$newV6=[guid]::NewGuid()
  $record.Migration=[ordered]@{OldPath=$old.Path;NewPath=$newPath;NewSHA256=$approved;NewV4=[string]$newV4;NewV6=[string]$newV6;Backup=$backup}
  Save-Report
  try {
   $null=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Add($newPath,$newV4,$newV6,$true)
   $newFilters=Select-Filter ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Verify($newPath,$newV4,$newV6,$true))
   $pluginTargets[0].Path=$newPath;$pluginTargets[0].SHA256=$approved;$pluginTargets[0].V4=[string]$newV4;$pluginTargets[0].V6=[string]$newV6
   $manifest.PreparedAt=Get-Date -Format o
   $manifest.Status='migrated_current_paths_pending_desktop_e2e'
   $manifest|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $manifestPath -Encoding utf8
   $state.ManifestSHA256=(Get-FileHash -LiteralPath $manifestPath).Hash
   $state.Targets=@($manifest.Targets|ForEach-Object {$t=$_;if($t.Path -ieq $newPath){[pscustomobject]@{Name=$t.Name;Path=$t.Path;Filters=$newFilters}}else{@($others|Where-Object {$_.Path -ieq $t.Path})[0]}})
   $state.Status='installed_current_paths_pending_desktop_e2e'
   $state|Add-Member -NotePropertyName RevalidatedAt -NotePropertyValue (Get-Date -Format o) -Force
   $state|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $statePath -Encoding utf8
  } catch {
   # 旧规则还在：撤掉新规则、恢复清单与状态，回到迁移前。
   $added=[guid[]]@(@($newV4,$newV6)|Where-Object {$null -ne [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get($_)})
   if($added.Count){$record.MigrationRollback=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Remove($added)}
   foreach($path in $restorePaths){Copy-Item -LiteralPath (Join-Path $backup ([IO.Path]::GetFileName($path))) -Destination $path -Force}
   throw
  }
  # 新规则和新清单都已生效后才撤旧规则；撤除失败时新旧规则并存，不影响防护。
  $removed=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Remove([guid]$old.V4,[guid]$old.V6)
  if($removed -ne 2 -or $null -ne [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get([guid]$old.V4) -or $null -ne [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get([guid]$old.V6)){throw '旧插件规则清理结果不符；新规则已生效，旧规则需人工核对。'}
  $record.Migration.OldRulesRemoved=$removed
 }
 if($Action -eq 'Test'){Test-Guard}
 if($Action -eq 'Install'){
  foreach($target in $manifest.Targets){
   if((Get-FileHash -LiteralPath $target.Path).Hash -ne $target.SHA256){throw ('程序已更新，需要重新核验：'+$target.Name)}
  }
  if(Test-Path -LiteralPath $statePath){
   $previousState=Get-Content -LiteralPath $statePath -Raw|ConvertFrom-Json
   if($previousState.Status -ne 'removed'){throw '防护状态记录仍为已安装；请先 Verify 或 Rollback，避免重复安装。'}
  }
  foreach($key in $keys){if($null -ne [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get($key)){throw '规则已存在但状态不完整，请先核验。'}}
  Test-Guard
  Assert-Closed
  try {
   $installed=@(foreach($target in $manifest.Targets){
    [pscustomobject]@{Name=$target.Name;Path=$target.Path;Filters=(Select-Filter ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Add($target.Path,[guid]$target.V4,[guid]$target.V6,$true)))}
   })
   $record.Installed=$installed
   [pscustomobject]@{InstalledAt=(Get-Date -Format o);Status='installed_current_paths_pending_desktop_e2e';ManifestSHA256=(Get-FileHash -LiteralPath $manifestPath).Hash;HelperSHA256=(Get-FileHash -LiteralPath $helper).Hash;Targets=$installed;BootTime=$false;DesktopLaunched=$false}|ConvertTo-Json -Depth 9|Set-Content -LiteralPath $statePath -Encoding utf8
  } catch {
   $record.InstallationRollback=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Remove($keys)
   throw
  }
 }
  if($Action -eq 'Verify'){
   $record.ManifestSHA256=(Get-FileHash -LiteralPath $manifestPath -Algorithm SHA256).Hash
   $record.Targets=@(foreach($target in $manifest.Targets){
   [pscustomobject]@{Name=$target.Name;Path=$target.Path;BinaryUnchanged=((Get-FileHash -LiteralPath $target.Path).Hash -eq $target.SHA256);Filters=(Select-Filter ([ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Verify($target.Path,[guid]$target.V4,[guid]$target.V6,$true)))}
  })
  $record.PackageVersion=(Get-AppxPackage -Name Claude).Version.ToString()
  $record.PackageVersionMatches=($record.PackageVersion -eq $manifest.PackageVersion)
  if(-not $record.PackageVersionMatches -or @($record.Targets|Where-Object {-not $_.BinaryUnchanged}).Count){throw '程序版本/文件已变化，旧路径规则不足以代表更新后的覆盖。'}
 }
 if($Action -eq 'Rollback'){
  $record.Removed=[ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Remove($keys)
  if(Test-Path -LiteralPath $statePath){
   $old=Get-Content -LiteralPath $statePath -Raw|ConvertFrom-Json
   $old.Status='removed';$old|Add-Member -NotePropertyName RemovedAt -NotePropertyValue (Get-Date -Format o) -Force
   $old|ConvertTo-Json -Depth 9|Set-Content -LiteralPath $statePath -Encoding utf8
  }
 }
 $record.Status='passed'; Save-Report
 Write-Output ('完成：'+$Action+'；记录已保存。没有启动 Claude，没有改变全机 DNS、路由或防火墙开关。')
} catch {
 $record.Status='failed';$record.Error=$_.Exception.Message;Save-Report
 throw
}
