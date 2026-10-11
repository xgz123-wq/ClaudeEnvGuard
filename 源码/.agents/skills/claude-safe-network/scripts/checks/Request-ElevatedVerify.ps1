#requires -Version 7.0
<#
.SYNOPSIS
    「一次 UAC 换 30 天免提权」——把只有管理员才看得到的实盘结论固化成缓存。

.DESCRIPTION
    Windows 的 WFP 过滤器实盘只有管理员进程读得到。这不是脚本写得不好，
    而是 Windows 的设计：netsh wfp show filters 和直接调 FwpmFilterGetById0
    两条路在非提权下都返回 ERROR_ACCESS_DENIED (5)，实测确认过。

    于是本脚本把「需要管理员」这件事收敛成一次性的：

      Request-ElevatedVerify.ps1 -Refresh     弹出一次 UAC，提权跑完核对并写缓存
      Request-ElevatedVerify.ps1 -Status      只看当前缓存是不是新鲜（不弹 UAC）
      Request-ElevatedVerify.ps1 -Worker      提权上下文里真正干活的那个（一般不用手调）

    之后 30 天内，任何普通权限下跑的总检都能直接读缓存放行，不再打扰用户。
    缓存只存结论（时间 / 条数 / 布尔 / 版本号），不含订阅、账号、端口等任何敏感信息。

    提权只用于「读」：核对 WFP 实盘 + 刷新 Desktop 直连阻断的 Verify.json，
    不会写任何系统配置。
#>
[CmdletBinding()]
param(
    [switch]$Refresh,
    [switch]$Worker,
    [switch]$Status,
    [switch]$Json,
    [int]$MaxAgeDays = 30
)

$ErrorActionPreference = 'Stop'

$here = [System.IO.Path]::GetDirectoryName($PSCommandPath)
$scriptsRoot = [System.IO.Path]::GetDirectoryName($here)
$root = [System.IO.Path]::GetDirectoryName($scriptsRoot)
$common = $scriptsRoot + '\Common.ps1'
$cachePath = $root + '\config\elevated-verify-cache.json'
$cacheDir = $root + '\config'
$manifestPath = $root + '\scripts\desktop\维护文件\应用过滤目标.json'
$helperPath = $root + '\scripts\desktop\维护文件\应用直连过滤.cs'
$managerPath = $root + '\scripts\desktop\维护文件\应用直连防护管理.ps1'

. $common

function Read-Cache {
    if (-not (Test-Path -LiteralPath $cachePath)) { return $null }
    try { return (Get-Content -LiteralPath $cachePath -Raw -Encoding UTF8 | ConvertFrom-Json) }
    catch { return $null }
}

function Write-Cache {
    param([hashtable]$Data)
    if (-not (Test-Path -LiteralPath $cacheDir)) { $null = New-Item -ItemType Directory -Path $cacheDir -Force }
    $json = ($Data | ConvertTo-Json -Depth 6)
    [System.IO.File]::WriteAllText($cachePath, $json, (New-Object System.Text.UTF8Encoding($false)))
}

function Get-CacheAge {
    param($Cache)
    if (-not $Cache -or -not $Cache.At) { return $null }
    try { return [math]::Round(((Get-Date) - [datetime]$Cache.At).TotalDays, 2) } catch { return $null }
}

# ------------------------------------------------------------------ -Worker：提权后才跑得动
if ($Worker) {
    $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if (-not $isAdmin) {
        Write-Host '[FAIL] -Worker 需要在管理员上下文运行（请用 -Refresh 让脚本自己提权）'
        exit 3
    }

    $rows = @()
    $expected = 0
    $found = 0
    $err = ''

    try {
        if (-not ('ClaudeDesktopNetworkGuard.DirectConnectionFilter' -as [type])) {
            Add-Type -LiteralPath $helperPath
        }
        $abi = [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::ValidateAbi()
        $man = Get-Content -LiteralPath $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
        $manifestHash = (Get-FileHash -LiteralPath $manifestPath -Algorithm SHA256).Hash
        $wfpKeys = @($man.Targets | ForEach-Object { [string]$_.V4; [string]$_.V6 })

        foreach ($t in $man.Targets) {
            $expected += 2
            $v4 = $false
            $v6 = $false
            try {
                # Verify 是权威判定：连原生 app-id 路径 blob、层、persistent 一起比。
                # 任一条缺失就抛，抛出后下面用 Get 逐条定位到底是哪一条。
                $null = [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Verify($t.Path, [guid]$t.V4, [guid]$t.V6, $true)
                $v4 = $true
                $v6 = $true
            }
            catch {
                foreach ($pair in @(@($t.V4, 'V4'), @($t.V6, 'V6'))) {
                    try {
                        $f = [ClaudeDesktopNetworkGuard.DirectConnectionFilter]::Get([guid]$pair[0])
                        if ($f -and $f.Enabled -and $f.IsOwnedExactBlock) {
                            if ($pair[1] -eq 'V4') { $v4 = $true } else { $v6 = $true }
                        }
                    }
                    catch { }
                }
            }
            $found += (@($v4, $v6) | Where-Object { $_ }).Count
            $rows += [ordered]@{
                Name = [string]$t.Name
                V4   = $v4
                V6   = $v6
                Ok   = ($v4 -and $v6)
            }
        }
    }
    catch { $err = $_.Exception.Message }
    if (-not $abi) { $abi = '' }

    # 顺手刷新第 4 层要用的 Desktop Verify.json（子进程跑，避免两处各自 Add-Type 打架）
    $deskStatus = ''
    $deskAt = ''
    $pkgVer = ''
    if (Test-Path -LiteralPath $managerPath) {
        $r = Invoke-ChildPwsh -ScriptPath $managerPath -Arguments @('-Action', 'Verify') -TimeoutSec 120
        if ($r -and $r.ExitCode -eq 0) {
            $vj = $root + '\scripts\desktop\维护文件\应用直连防护-Verify.json'
            if (Test-Path -LiteralPath $vj) {
                try {
                    $o = Get-Content -LiteralPath $vj -Raw -Encoding UTF8 | ConvertFrom-Json
                    $deskStatus = [string]$o.Status
                    if ($o.At) { $deskAt = [string]$o.At }
                    if ($o.PackageVersion) { $pkgVer = [string]$o.PackageVersion }
                }
                catch { }
                # Verify.json 是 Docs 与 Skill 必须一致的文件之一：在项目里时把通过的新记录同步回 Docs，
                # 只在两边清单完全相同时同步；清单不同说明有一边没跟上，保留差异让总检暴露。
                if ($deskStatus -eq 'passed') {
                    $dir = [IO.DirectoryInfo]$root
                    while ($null -ne $dir -and -not (Test-Path -LiteralPath (Join-Path $dir.FullName 'docs\配置\Claude环境\Desktop防护'))) { $dir = $dir.Parent }
                    if ($null -ne $dir) {
                        $docs = Join-Path $dir.FullName 'docs\配置\Claude环境\Desktop防护'
                        $docsManifest = Join-Path $docs '应用过滤目标.json'
                        if ((Test-Path -LiteralPath $docsManifest) -and (Get-FileHash -LiteralPath $docsManifest).Hash -eq (Get-FileHash -LiteralPath $manifestPath).Hash) {
                            try { Copy-Item -LiteralPath $vj -Destination (Join-Path $docs '应用直连防护-Verify.json') -Force } catch { }
                        }
                    }
                }
            }
        }
    }

    $data = @{
        At                  = (Get-Date).ToString('o')
        IsAdmin             = $true
        WfpExpected         = $expected
        WfpFound            = $found
        WfpPassed           = (($expected -gt 0) -and ($found -eq $expected))
        ManifestSHA256      = $manifestHash
        WfpKeys             = $wfpKeys
        WfpDetail           = $rows
        WfpAbi              = $abi
        WfpError            = $err
        DesktopVerifyStatus = $deskStatus
        DesktopVerifyAt     = $deskAt
        PkgVersion          = $pkgVer
        MaxAgeDays          = $MaxAgeDays
        PwshVersion         = $PSVersionTable.PSVersion.ToString()
        Machine             = $env:COMPUTERNAME
    }
    Write-Cache $data

    if ($Json) {
        $data | ConvertTo-Json -Depth 6
    }
    else {
        Write-Host ('WFP 实盘核对：' + $found + '/' + $expected + ' 在位')
        if ($err) { Write-Host ('  错误：' + $err) }
        Write-Host ('Desktop 直连阻断 Verify.json：' + $deskStatus)
        Write-Host ('缓存已写入：' + $cachePath)
    }
    if ($data.WfpPassed) { exit 0 } else { exit 1 }
}

# ------------------------------------------------------------------ -Status：只看缓存
if ($Status) {
    $c = Read-Cache
    $age = Get-CacheAge $c
    if (-not $c) {
        Write-Host '[待办] 还没有提权核对缓存'
        Write-Host ('         跑一次：' + $PSCommandPath + ' -Refresh   （会弹一次 UAC）')
        exit 4
    }
    $limit = [int]$c.MaxAgeDays
    if ($limit -le 0) { $limit = $MaxAgeDays }
    $fresh = ($age -ne $null -and $age -le $limit -and (Test-ElevatedVerifyCacheBinding -Cache $c))
    $wfpText = '未通过'
    if ($c.WfpPassed) { $wfpText = '通过' }
    if ($Json) {
        [pscustomobject]@{ AgeDays = $age; Fresh = $fresh; MaxAgeDays = $limit; WfpFound = $c.WfpFound; WfpExpected = $c.WfpExpected; WfpPassed = $c.WfpPassed; DesktopVerifyStatus = $c.DesktopVerifyStatus; At = $c.At } | ConvertTo-Json -Depth 4
    }
    else {
        Write-Host ('缓存时间：' + $c.At + '   （' + $age + ' 天前，有效期 ' + $limit + ' 天）')
        Write-Host ('WFP 实盘：' + $c.WfpFound + '/' + $c.WfpExpected + '  ' + $wfpText)
        Write-Host ('Desktop Verify：' + $c.DesktopVerifyStatus)
        $freshText = '已过期，建议 -Refresh 重跑'
        if ($fresh) { $freshText = '新鲜，普通权限总检会直接用' }
        Write-Host ('状态：' + $freshText)
    }
    if ($fresh -and $c.WfpPassed) { exit 0 } else { exit 1 }
}

# ------------------------------------------------------------------ -Refresh：自我提权重入
if ($Refresh) {
    $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    $started = Get-Date

    if ($isAdmin) {
        # 已经在管理员上下文，直接当 worker 用
        & $PSCommandPath -Worker -MaxAgeDays $MaxAgeDays
        exit $LASTEXITCODE
    }

    $pwsh = $script:PwshExe
    if (-not $pwsh -or -not (Test-Path -LiteralPath $pwsh)) { $pwsh = $env:ProgramFiles + '\PowerShell\7\pwsh.exe' }
    if (-not (Test-Path -LiteralPath $pwsh)) {
        Write-Host '[FAIL] 找不到 PowerShell 7 的 pwsh.exe'
        exit 3
    }

    Write-Host ''
    Write-Host '即将弹出 1 次 UAC 管理员确认 —— 只用来「读」WFP 实盘，不改任何系统配置。'
    Write-Host '点「是」之后大约 3~8 秒跑完，结论会缓存 30 天。'
    Write-Host ''

    # -Verb RunAs 不能和输出重定向混用，所以让子进程把结论写进缓存文件，父进程读完再报。
    $argstr = '-NoLogo -NoProfile -ExecutionPolicy Bypass -File "' + $PSCommandPath + '" -Worker -MaxAgeDays ' + $MaxAgeDays
    $code = -1
    try {
        $p = Start-Process -FilePath $pwsh -Verb RunAs -Wait -PassThru -ArgumentList $argstr -ErrorAction Stop
        $code = $p.ExitCode
    }
    catch {
        Write-Host ''
        Write-Host '[WARN]  提权被取消或失败，缓存未更新。'
        Write-Host ('        原因：' + $_.Exception.Message)
        Write-Host '        需要时随时重跑本命令即可。'
        exit 2
    }

    $c = Read-Cache
    $ok = ($c -and $c.At -and ([datetime]$c.At -ge $started))
    if (-not $ok) {
        Write-Host '[WARN]  提权进程已结束，但没有读到新的缓存内容，请重试。'
        exit 1
    }

    $age = Get-CacheAge $c
    $wfpText = '有缺失！'
    if ($c.WfpPassed) { $wfpText = '在位' }
    Write-Host ''
    Write-Host ('[PASS]  提权核对完成（' + $age + ' 天前 = 刚刚）')
    Write-Host ('        WFP 实盘：' + $c.WfpFound + '/' + $c.WfpExpected + '  ' + $wfpText)
    Write-Host ('        Desktop 直连阻断：' + $c.DesktopVerifyStatus)
    Write-Host ('        缓存：' + $cachePath + '   （' + $c.MaxAgeDays + ' 天内有效）')
    if ($c.WfpPassed) { exit 0 } else { exit 1 }
}

# ------------------------------------------------------------------ 无参：给人看的说明
$c = Read-Cache
$age = Get-CacheAge $c
Write-Host ''
Write-Host 'Request-ElevatedVerify —— 提权实盘核对器'
Write-Host ''
Write-Host '  -Refresh   弹一次 UAC，提权核对 WFP 实盘 + 刷新 Desktop Verify.json，结论缓存 30 天'
Write-Host '  -Status    只读当前缓存是否新鲜（不弹 UAC）'
Write-Host '  -Worker    提权上下文内部使用，一般不用手调'
Write-Host ''
if ($c) {
    $limit = [int]$c.MaxAgeDays
    if ($limit -le 0) { $limit = $MaxAgeDays }
    $fresh = ($age -ne $null -and $age -le $limit -and (Test-ElevatedVerifyCacheBinding -Cache $c))
    $freshCn = '已过期'
    if ($fresh) { $freshCn = '新鲜' }
    Write-Host ('当前缓存：' + $c.At + '  （' + $age + ' 天前，有效期 ' + $limit + ' 天，' + $freshCn + '）')
    Write-Host ('  WFP 实盘 ' + $c.WfpFound + '/' + $c.WfpExpected + '，Desktop Verify ' + $c.DesktopVerifyStatus)
}
else {
    Write-Host '当前缓存：无'
}
Write-Host ''
