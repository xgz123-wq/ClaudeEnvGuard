#requires -Version 7.0
# 查询或下载并审核 Claude Desktop 官方 MSIX；不提权、不安装、不启动 Claude。
# -CheckOnly：只向官方入口查最新版本号并与已装版本比较，不建目录、不下载；Status=up_to_date 或 update_available。
# 不带 -CheckOnly：已是最新时 Status=up_to_date；否则下载并审核，Status=downloaded，给出一键更新所需的 PackagePath 与 SHA256。
[CmdletBinding()]
param(
    [string]$Proxy = 'http://127.0.0.1:43867',
    [switch]$CheckOnly
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSStyle.OutputRendering = 'PlainText'

# 复用底层 Inspect：签名、包家族、发布者、架构、主程序声明全部在那里审核；失败抛错并带原始输出。
function Get-Inspection {
    param([string]$Path)
    $out = & (Join-Path $PSHOME 'pwsh.exe') -NoProfile -NonInteractive -File (Join-Path $PSScriptRoot '更新ClaudeDesktop.ps1') -Action Inspect -PackagePath $Path 2>&1 | Out-String
    if ($LASTEXITCODE -ne 0) { throw ('安装包审核未通过：' + $out.Trim()) }
    return ($out | ConvertFrom-Json)
}

$result = [ordered]@{At=(Get-Date -Format o);Status='running';OS=$null;Architecture=$null;InstalledVersion=$null;LatestVersion=$null;SourceUrl=$null;DownloadUrl=$null;PackagePath=$null;SHA256=$null;Signature=$null;NextCommand=$null;Error=$null}
try {
    if (-not $IsWindows) { throw '本流程只支持 Windows MSIX 安装；macOS 需另行编写。' }
    $result.OS = 'win32'
    # 以操作系统真实架构为准，不看 pwsh 进程架构；与已装包对不上就停止，避免跨架构替换。
    $osArch = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    $arch = switch ($osArch) {
        ([Runtime.InteropServices.Architecture]::X64) { 'x64' }
        ([Runtime.InteropServices.Architecture]::Arm64) { 'arm64' }
        default { throw ('不支持的系统架构：' + $osArch) }
    }
    $result.Architecture = $arch
    Import-Module Appx -UseWindowsPowerShell -WarningAction SilentlyContinue
    $packages = @(Get-AppxPackage -Name Claude)
    if ($packages.Count -ne 1) { throw '当前用户 Claude 安装包不唯一或不存在；首次安装不走本流程。' }
    $installed = $packages[0]
    $result.InstalledVersion = [string]$installed.Version
    if ([string]$installed.Architecture -ne $arch) { throw ('已装包架构 ' + $installed.Architecture + ' 与系统架构 ' + $arch + ' 不一致，停止。') }

    # 固定来源：Anthropic Windows 部署说明给出的最新版跳转入口。HEAD 返回 405，只能用 GET 读 307 的 Location。
    $result.SourceUrl = "https://claude.ai/api/desktop/win32/$arch/msix/latest/redirect"
    # 不跟随跳转时 pwsh 会报“超过最大跳转次数”，这里只要响应头，故压掉该错误。
    $response = Invoke-WebRequest -Uri $result.SourceUrl -Method Get -Proxy $Proxy -MaximumRedirection 0 -SkipHttpErrorCheck -TimeoutSec 60 -ErrorAction SilentlyContinue
    if (-not $response) { throw '官方入口无响应；检查 43867 通道。' }
    $location = [string]($response.Headers['Location'] | Select-Object -First 1)
    if ($response.StatusCode -notin 301,302,303,307,308 -or -not $location) { throw ('官方入口没有返回跳转，状态码：' + $response.StatusCode) }
    $downloadUri = [Uri]::new([Uri]$result.SourceUrl, $location)
    # 2026-09-25 实测跳到 https://downloads.claude.ai/releases/win32/<架构>/<三段版本>/Claude-<提交哈希>.msix。
    if ($downloadUri.Scheme -ne 'https' -or $downloadUri.Host -notmatch '(^|\.)claude\.ai$') { throw ('跳转地址不在 claude.ai 的 HTTPS 下：' + $downloadUri) }
    $result.DownloadUrl = $downloadUri.AbsoluteUri

    # 地址里的版本号只用来提前判断“已是最新”；真正的版本以下载后包内 Manifest 为准。
    # 地址是三段版本、已装包是四段，补齐第四段再比较，否则 [version] 会判为不等。
    $urlVersion = [regex]::Match($downloadUri.AbsolutePath, '/(\d+\.\d+\.\d+(\.\d+)?)/').Groups[1].Value
    if ($urlVersion -and $urlVersion.Split('.').Count -eq 3) { $urlVersion += '.0' }
    if ($urlVersion -and [version]$urlVersion -eq [version]$result.InstalledVersion) {
        $result.LatestVersion = $urlVersion
        $result.Status = 'up_to_date'
        $result | ConvertTo-Json -Depth 8
        exit 0
    }
    if ($CheckOnly) {
        if (-not $urlVersion) { throw ('跳转地址里没有版本号，无法只查不下：' + $downloadUri.AbsolutePath) }
        $result.LatestVersion = $urlVersion
        $result.Status = 'update_available'
        $result | ConvertTo-Json -Depth 8
        exit 0
    }

    $downloadDir = Join-Path $env:LOCALAPPDATA 'ClaudeNetworkGuard\Desktop安装包下载'
    $null = New-Item -ItemType Directory -Path $downloadDir -Force
    # 已按版本名落盘且审核通过的包直接复用，重跑 Plan 不再重复拉 293MB。
    $final = Join-Path $downloadDir ('Claude-' + $urlVersion + '.msix')
    $inspection = $null
    if ($urlVersion -and (Test-Path -LiteralPath $final)) {
        try {
            $inspection = Get-Inspection -Path $final
            if ($inspection -and [version]$inspection.NewVersion -eq [version]$urlVersion) {
                $result.DownloadUrl = $downloadUri.AbsoluteUri + '（本地已有审核通过的包，未重新下载）'
            } else {
                $inspection = $null
            }
        } catch { $inspection = $null }
    }
    if (-not $inspection) {
        # 临时名下载，审核通过后才改成 Claude-<版本>.msix；未通过的包当场删除，不留残留。
        $partial = Join-Path $downloadDir ('Claude-download-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.msix')
        try {
            Invoke-WebRequest -Uri $downloadUri -Proxy $Proxy -OutFile $partial -TimeoutSec 1800
            $inspection = Get-Inspection -Path $partial
        } catch {
            Remove-Item -LiteralPath $partial -ErrorAction SilentlyContinue
            throw
        }
        $final = Join-Path $downloadDir ('Claude-' + $inspection.NewVersion + '.msix')
        Move-Item -LiteralPath $partial -Destination $final -Force
        if ((Get-FileHash -LiteralPath $final).Hash -ne $inspection.SHA256) { throw '改名后安装包哈希变化。' }
    }
    $result.LatestVersion = $inspection.NewVersion
    $result.PackagePath = $final
    $result.SHA256 = $inspection.SHA256
    $result.Signature = $inspection.Signature
    $result.Status = if ([version]$inspection.NewVersion -eq [version]$result.InstalledVersion) { 'up_to_date' } else { 'downloaded' }
    $result.NextCommand = "一键更新ClaudeDesktop.ps1 -Action Update -PackagePath '$final' -ApprovedSHA256 '$($inspection.SHA256)'（CLI 同时更新时再加 -CliApprovedSHA256）"
    $result | ConvertTo-Json -Depth 8
} catch {
    $result.Status = 'failed'
    $result.Error = $_.Exception.Message
    $result | ConvertTo-Json -Depth 8
    exit 1
}
