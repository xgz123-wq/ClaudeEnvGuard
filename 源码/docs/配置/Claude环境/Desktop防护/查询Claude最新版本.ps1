#requires -Version 7.0
# 只查不下：向官方来源查 Claude Desktop、Claude Code CLI、VS Code 插件的最新版本，与本机已装版本比较。
# 不下载、不建目录、不提权、不安装、不启动 Claude 或 VS Code；用户问“需不需要更新”时跑这一个。
# 输出 JSON；每项 Status=up_to_date / update_available / failed。任一项 failed 时退出码 1。
[CmdletBinding()]
param(
    [string]$Proxy = 'http://127.0.0.1:43867'
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSStyle.OutputRendering = 'PlainText'

function New-Row([string]$Name, [string]$Source) {
    [ordered]@{Product=$Name;Installed=$null;Latest=$null;Status='running';Source=$Source;Error=$null}
}
function Complete-Row($Row) {
    if ($Row.Installed -and $Row.Latest) {
        $Row.Status = if ([version]$Row.Latest -gt [version]$Row.Installed) { 'update_available' } else { 'up_to_date' }
    } elseif ($Row.Status -eq 'running') {
        $Row.Status = 'failed'
        if (-not $Row.Error) { $Row.Error = '本机版本或官方版本没有读到。' }
    }
    [pscustomobject]$Row
}

$rows = @()

# Desktop：复用下载脚本的只查模式，固定来源与架构判断都在那里。
$desktop = New-Row 'Claude Desktop' 'claude.ai/api/desktop/win32/<架构>/msix/latest/redirect'
try {
    $out = & (Join-Path $PSHOME 'pwsh.exe') -NoProfile -NonInteractive -File (Join-Path $PSScriptRoot '下载ClaudeDesktop官方包.ps1') -CheckOnly -Proxy $Proxy 2>&1 | Out-String
    $d = $out | ConvertFrom-Json
    if ($d.Status -eq 'failed') { throw $d.Error }
    $desktop.Installed = [string]$d.InstalledVersion
    $desktop.Latest = [string]$d.LatestVersion
} catch { $desktop.Error = $_.Exception.Message }
$rows += Complete-Row $desktop

# CLI：本机版本读全局包的 package.json，不运行 claude；最新版只问 npm 官方源。
$cli = New-Row 'Claude Code CLI' 'registry.npmjs.org @anthropic-ai/claude-code'
try {
    $pkg = Join-Path $env:APPDATA 'npm\node_modules\@anthropic-ai\claude-code\package.json'
    if (-not (Test-Path -LiteralPath $pkg)) { throw ('没有找到全局 CLI：' + $pkg) }
    $cli.Installed = [string](Get-Content -LiteralPath $pkg -Raw | ConvertFrom-Json).version
    $latest = (& npm view '@anthropic-ai/claude-code' version --registry https://registry.npmjs.org 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $latest -notmatch '^\d+\.\d+\.\d+$') { throw ('npm view 没有返回版本号：' + $latest) }
    $cli.Latest = $latest
} catch { $cli.Error = $_.Exception.Message }
$rows += Complete-Row $cli

# VS Code 插件：本机版本以 extensions.json 当前登记为准（旧版本目录可能还在磁盘上）；最新版问 VS Code 官方市场，只看 win32-x64。
$plugin = New-Row 'VS Code Claude Code 插件' 'marketplace.visualstudio.com anthropic.claude-code win32-x64'
try {
    $registry = Join-Path $env:USERPROFILE '.vscode\extensions\extensions.json'
    if (-not (Test-Path -LiteralPath $registry)) { throw ('没有找到 ' + $registry) }
    $records = @((Get-Content -LiteralPath $registry -Raw | ConvertFrom-Json) | Where-Object { $_.identifier.id -ieq 'anthropic.claude-code' })
    if ($records.Count -ne 1) { throw ('extensions.json 登记的 Claude Code 插件数量为 ' + $records.Count + '，预期 1') }
    $plugin.Installed = [string]$records[0].version
    # 顺带报告防护是否覆盖当前插件目录；换了目录就给出新程序的指纹与签名，供 -PluginApprovedSHA256 审核。
    $pluginRoot = [string]$records[0].location.path
    if ($pluginRoot -match '^/([A-Za-z]:/.*)$') { $pluginRoot = $Matches[1] }
    $exe = Join-Path $pluginRoot.Replace('/','\') 'resources\native-binary\claude.exe'
    $protected = @((Get-Content -LiteralPath (Join-Path $PSScriptRoot '应用过滤目标.json') -Raw | ConvertFrom-Json).Targets.Path)
    $plugin.ProtectedPathMatches = [bool]($protected -contains $exe)
    if (-not $plugin.ProtectedPathMatches -and (Test-Path -LiteralPath $exe)) {
        $plugin.CurrentExe = $exe
        $plugin.CurrentSHA256 = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash
        $sig = Get-AuthenticodeSignature -LiteralPath $exe
        $plugin.Signature = [string]$sig.Status + '；' + [string]$sig.SignerCertificate.Subject
    }
    $body = '{"filters":[{"criteria":[{"filterType":7,"value":"anthropic.claude-code"}]}],"flags":16}'
    $r = Invoke-RestMethod -Uri 'https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery' -Method Post -Body $body -ContentType 'application/json' -Headers @{Accept='application/json;api-version=7.2-preview.1'} -Proxy $Proxy -TimeoutSec 60
    $versions = @($r.results[0].extensions[0].versions | Where-Object { $_.targetPlatform -eq 'win32-x64' } | ForEach-Object { [version]$_.version } | Sort-Object -Descending)
    if ($versions.Count -eq 0) { throw '官方市场没有返回 win32-x64 版本。' }
    $plugin.Latest = [string]$versions[0]
} catch { $plugin.Error = $_.Exception.Message }
$rows += Complete-Row $plugin

[pscustomobject]@{At=(Get-Date -Format o);Results=$rows} | ConvertTo-Json -Depth 5
if (@($rows | Where-Object Status -eq 'failed').Count) { exit 1 }
