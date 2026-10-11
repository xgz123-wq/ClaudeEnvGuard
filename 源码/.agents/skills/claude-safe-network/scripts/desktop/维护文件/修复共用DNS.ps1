#requires -Version 7.0
param([ValidateSet('Prepare','Harden','Apply','Rollback')][string]$Action='Prepare')
$ErrorActionPreference='Stop'
$base=Join-Path $env:APPDATA 'io.github.clash-verge-rev.clash-verge-rev'
$runtime=Join-Path $base 'clash-verge.yaml'
$checkFile=Join-Path $base 'clash-verge-check.yaml'
$liveScript=Join-Path $base 'profiles\Script.js'
$sourceScript=Join-Path (Split-Path (Split-Path $PSScriptRoot -Parent) -Parent) 'clash\Clash全局账号通道.js'
$core='C:\Program Files\Clash Verge\verge-mihomo.exe'
$utf8=[Text.UTF8Encoding]::new($false)
$stateFile=Join-Path $PSScriptRoot '共用DNS修复状态.json'
# 复用已核验的本机接口与快照函数；不执行原脚本的操作分支。
$tokens=$null; $errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot '专用通道管理.ps1'),[ref]$tokens,[ref]$errors)
foreach($name in @('Assert-ClaudeClosed','Invoke-Core','Read-Snapshot','Restore-Selections','Test-CoreConfig')) {
  $def=$ast.Find({param($a) $a -is [Management.Automation.Language.FunctionDefinitionAst] -and $a.Name -eq $name},$true)
  if(-not $def){throw "缺少复用函数 $name"}
  . ([scriptblock]::Create($def.Extent.Text))
}
Assert-ClaudeClosed
if($Action -eq 'Prepare') {
  if(Test-Path $stateFile){throw '已有状态记录，先核对，避免覆盖备份。'}
  $snapshot=Read-Snapshot
  $backup=Join-Path $base ('account-routing-backups\'+(Get-Date -Format 'yyyyMMdd-HHmmss')+'-共用DNS修复')
  $null=New-Item -ItemType Directory -Path $backup
  $files=@(); $index=0
  foreach($path in @($sourceScript,$liveScript,$runtime,$checkFile)) {
    $index++
    $copy=Join-Path $backup ($index.ToString()+'-'+[IO.Path]::GetFileName($path))
    Copy-Item -LiteralPath $path -Destination $copy
    $files+=@{Original=$path;Backup=$copy;BeforeHash=(Get-FileHash $path).Hash;Candidate=$copy+'.candidate'+[IO.Path]::GetExtension($path);InstalledHash=$null}
  }
  $state=[pscustomobject]@{Status='preparing';RecordedAt=(Get-Date -Format o);BackupDirectory=$backup;Mode=$snapshot.Mode;Selections=$snapshot.Selections;CorePid=@(Get-Process verge-mihomo).Id;Files=$files}
  [IO.File]::WriteAllText($stateFile,($state|ConvertTo-Json -Depth 8),$utf8)
  @'
import json,sys,pathlib,yaml,copy
s=json.loads(pathlib.Path(sys.argv[1]).read_text(encoding='utf-8-sig'))
servers=['https://1.1.1.1/dns-query#ACCOUNT-US','https://8.8.8.8/dns-query#ACCOUNT-US']
for f in s['Files']:
    p=pathlib.Path(f['Backup']); text=p.read_text(encoding='utf-8-sig')
    if f['Original'].endswith('.js'):
        marker='  if (!Array.isArray(config.listeners)) {'
        assert text.count(marker)==1
        addition='''  // 2026-09-10 用户授权：共用网页 DNS 经专用线路加密解析，覆盖内置浏览器任意网站。
  config.dns.enable = true;
  config.dns.nameserver = accountDnsServers.slice();
  config.dns.fallback = [];
  config.dns["direct-nameserver"] = accountDnsServers.slice();
  // 节点自身的引导解析保持原值，避免代理建立前产生循环依赖。

'''
        text=text.replace(marker,addition+marker).replace('// 通用/国内 DNS 保持订阅无关，避免影响飞书和日常网络。','// 下方共用 DNS 加固会影响其他应用的域名解析，不改变其分流规则。')
        pathlib.Path(f['Candidate']).write_text(text,encoding='utf-8')
    else:
        old=yaml.safe_load(text); new=copy.deepcopy(old)
        new['dns'].update({'enable':True,'nameserver':servers,'fallback':[],'direct-nameserver':servers})
        assert {k:v for k,v in old.items() if k!='dns'}=={k:v for k,v in new.items() if k!='dns'}
        pathlib.Path(f['Candidate']).write_text(yaml.safe_dump(new,allow_unicode=True,sort_keys=False),encoding='utf-8')
print('候选文件已生成；只更改共用网页解析器，节点、规则、监听、TUN不变。')
'@ | python - $stateFile
  if($LASTEXITCODE -ne 0){throw '候选生成失败'}
  foreach($f in $files){if($f.Original.EndsWith('.js')){& node --check $f.Candidate;if($LASTEXITCODE -ne 0){throw '脚本语法失败'}}else{Test-CoreConfig $f.Candidate}}
  $state.Status='prepared'
  [IO.File]::WriteAllText($stateFile,($state|ConvertTo-Json -Depth 8),$utf8)
  '候选校验通过，尚未应用。'
  exit
}
$state=Get-Content $stateFile -Raw|ConvertFrom-Json
if($Action -eq 'Harden') {
  if($state.Status -ne 'applied_pending_real_browser_acceptance'){throw '先核对 DNS 阶段状态'}
  foreach($f in $state.Files){if((Get-FileHash $f.Original).Hash -ne $f.InstalledHash){throw '后续已有修改'}}
  @'
import json,sys,pathlib,yaml
s=json.loads(pathlib.Path(sys.argv[1]).read_text(encoding='utf-8-sig'))
for f in s['Files']:
    text=pathlib.Path(f['Original']).read_text(encoding='utf-8-sig')
    if f['Original'].endswith('.js'):
        marker='  if (!Array.isArray(config.listeners)) {'
        assert text.count(marker)==1
        text=text.replace(marker,'''  // Windows 多网卡 DNS 旁路防护：ETW 已确认 WLAN 与 TUN 并行代查。
  config.tun = config.tun || {};
  config.tun["strict-route"] = true;
  config.tun["dns-hijack"] = ["any:53", "tcp://any:53"];

'''+marker)
    else:
        c=yaml.safe_load(text)
        c['tun']['strict-route']=True
        c['tun']['dns-hijack']=['any:53','tcp://any:53']
        text=yaml.safe_dump(c,allow_unicode=True,sort_keys=False)
    pathlib.Path(f['Candidate']).write_text(text,encoding='utf-8')
'@ | python - $stateFile
  if($LASTEXITCODE -ne 0){throw '严格 DNS 候选生成失败'}
  foreach($f in $state.Files){if($f.Original.EndsWith('.js')){& node --check $f.Candidate;if($LASTEXITCODE -ne 0){throw '脚本语法失败'}}else{Test-CoreConfig $f.Candidate};$f.BeforeHash=(Get-FileHash $f.Original).Hash}
  $state.Status='prepared'
  [IO.File]::WriteAllText($stateFile,($state|ConvertTo-Json -Depth 8),$utf8)
  '严格 DNS 候选校验通过；未应用。'
  exit
}
if($Action -eq 'Apply') {
  if($state.Status -ne 'prepared'){throw '不是待应用状态'}
  foreach($f in $state.Files){if((Get-FileHash $f.Original).Hash -ne $f.BeforeHash){throw '配置在准备后改变，停止覆盖'}}
  try {
    foreach($f in $state.Files){Copy-Item -LiteralPath $f.Candidate -Destination $f.Original -Force}
    Assert-ClaudeClosed
    $null=Invoke-Core 'PUT' '/configs?force=true' @{path=$runtime;payload=''}
    Restore-Selections $state
    if(@(Get-Process verge-mihomo).Id -ne $state.CorePid){throw '核心进程发生变化'}
    foreach($f in $state.Files){$f.InstalledHash=(Get-FileHash $f.Original).Hash}
    $state.Status='applied_pending_real_browser_acceptance'
  }catch{
    Assert-ClaudeClosed
    foreach($f in $state.Files){Copy-Item -LiteralPath $f.Backup -Destination $f.Original -Force}
    $null=Invoke-Core 'PUT' '/configs?force=true' @{path=$runtime;payload=''}
    Restore-Selections $state
    $state.Status='rolled_back_after_error'
    [IO.File]::WriteAllText($stateFile,($state|ConvertTo-Json -Depth 8),$utf8)
    throw
  }
}else{
  if($state.Status -ne 'applied_pending_real_browser_acceptance'){throw '当前状态不允许自动恢复'}
  foreach($f in $state.Files){if((Get-FileHash $f.Original).Hash -ne $f.InstalledHash){throw '后续已有修改，停止恢复'}}
  foreach($f in $state.Files){Copy-Item -LiteralPath $f.Backup -Destination $f.Original -Force}
  $null=Invoke-Core 'PUT' '/configs?force=true' @{path=$runtime;payload=''}
  Restore-Selections $state
  $state.Status='rolled_back'
}
[IO.File]::WriteAllText($stateFile,($state|ConvertTo-Json -Depth 8),$utf8)
$state|Select-Object Status,CorePid,BackupDirectory|ConvertTo-Json
