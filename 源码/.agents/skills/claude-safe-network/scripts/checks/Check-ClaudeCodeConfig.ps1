#requires -Version 7.0
<#
.SYNOPSIS
    第 2 层：Claude Code settings.json 检查。

.DESCRIPTION
    只读 ~\.claude\settings.json，核对「进程级代理」这条最关键的链路：
      · env.HTTP_PROXY / HTTPS_PROXY / ALL_PROXY 是否都指向 Clash 混合端口
      · env.NO_PROXY 是否放行本机与局域网
      · 绝对不能出现 ANTHROPIC_BASE_URL（指向中转 = 主动把流量交给第三方）
      · 自动更新是否按预期被关掉（用户是故意关的，属预期态）
      · 系统级环境变量里不允许出现 HTTP_PROXY 等（会和 TUN 打架）
#>
[CmdletBinding()]
param([switch] $Json, [switch] $OpenSettings)

$ErrorActionPreference = 'Continue'
$__d = $PSScriptRoot
while ($__d -and -not (Test-Path (Join-Path $__d 'Common.ps1'))) { $__d = Split-Path $__d -Parent }
if (-not $__d) { throw 'Common.ps1 未找到，skill 目录不完整。' }
. (Join-Path $__d 'Common.ps1')

Reset-CheckResults
Write-Banner '第 2 层 · Claude Code 配置检查' '~\.claude\settings.json 的进程级代理链路'

$settingsPath = Join-Path $env:USERPROFILE '.claude\settings.json'
$expectedProxy = "http://127.0.0.1:$($Ports.Mixed)"
$payload = [ordered]@{ settingsPath = $settingsPath }

if (-not (Test-Path $settingsPath)) {
    Add-CheckResult -Status 'FAIL' -Item 'settings.json' -Evidence "不存在：$settingsPath"
}
else {
    try {
        $s = Get-Content -LiteralPath $settingsPath -Raw | ConvertFrom-Json
        Add-CheckResult -Status 'PASS' -Item 'settings.json' -Evidence "可解析；$((Get-Item $settingsPath).Length) 字节，改于 $((Get-Item $settingsPath).LastWriteTime.ToString('yyyy-MM-dd HH:mm'))"
        $envBlock = $s.env

        if ($null -eq $envBlock) {
            Add-CheckResult -Status 'FAIL' -Item 'env 代理块' -Evidence '缺少 env —— Claude Code 不会走任何代理'
        }
        else {
            $map = @{}
            foreach ($prop in $envBlock.PSObject.Properties) { $map[$prop.Name] = [string]$prop.Value }

            foreach ($k in 'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY') {
                $val = $map[$k]
                if (-not $val) {
                    Add-CheckResult -Status 'WARN' -Item "env.$k" -Evidence '未设置'
                }
                elseif ($val.TrimEnd('/') -eq $expectedProxy.TrimEnd('/')) {
                    Add-CheckResult -Status 'PASS' -Item "env.$k" -Evidence $val
                }
                else {
                    Add-CheckResult -Status 'FAIL' -Item "env.$k" -Evidence "$val（应为 $expectedProxy）"
                }
            }

            $noProxy = $map['NO_PROXY']
            if ($noProxy -match '127\.0\.0\.1' -and $noProxy -match 'localhost') {
                Add-CheckResult -Status 'PASS' -Item 'env.NO_PROXY' -Evidence $noProxy
            }
            elseif ($noProxy) {
                Add-CheckResult -Status 'WARN' -Item 'env.NO_PROXY' -Evidence "$noProxy（建议含 localhost,127.0.0.1,*.local）"
            }
            else {
                Add-CheckResult -Status 'WARN' -Item 'env.NO_PROXY' -Evidence '未设置 —— 本机回环请求可能被塞进代理'
            }

            $relay = $map.Keys | Where-Object { $_ -match 'ANTHROPIC_BASE_URL|ANTHROPIC_API_URL|ANTHROPIC_AUTH_TOKEN|CLAUDE_CODE_USE_BEDROCK|CLAUDE_CODE_USE_VERTEX' }
            if ($relay) {
                Add-CheckResult -Status 'FAIL' -Item '中转 / 第三方网关变量' -Evidence ("出现了不该有的：" + ($relay -join ', '))
            }
            else {
                Add-CheckResult -Status 'PASS' -Item '无中转变量' -Evidence '未设 ANTHROPIC_BASE_URL 等中转指向'
            }

            if ($map['DISABLE_AUTOUPDATER']) {
                Add-CheckResult -Status 'PASS' -Item 'env.DISABLE_AUTOUPDATER' -Evidence "$($map['DISABLE_AUTOUPDATER'])（你是有意关的；升级走手动）"
            }
            else {
                Add-CheckResult -Status 'WARN' -Item 'env.DISABLE_AUTOUPDATER' -Evidence '未关 —— 自动升级可能撞上 WFP 清单里的旧路径哈希'
            }

            if ($map['BROWSER']) {
                $b = $map['BROWSER']
                Add-CheckResult -Status $(if (Test-Path $b) { 'PASS' } else { 'WARN' }) -Item 'env.BROWSER' -Evidence "$b$(if (-not (Test-Path $b)) { '（文件不存在）' })"
            }
        }
        $payload['settings'] = $s
    }
    catch { Add-CheckResult -Status 'FAIL' -Item 'settings.json' -Evidence ("JSON 解析失败：" + $_.Exception.Message) }
}

Write-Section '系统级环境变量（不允许出现代理指向）'
foreach ($scope in 'Process', 'User', 'Machine') {
    $found = @()
    foreach ($k in 'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'ANTHROPIC_BASE_URL') {
        $v = [Environment]::GetEnvironmentVariable($k, $scope)
        if ($v) { $found += "$k=$v" }
    }
    Add-CheckResult -Status $(if ($found.Count -eq 0) { 'PASS' } else { 'FAIL' }) -Item "$scope 作用域" `
        -Evidence $(if ($found.Count -eq 0) { '干净' } else { $found -join '; ' })
}

Write-Section '相邻配置文件（仅记录，不评判）'
foreach ($f in @('.claude\mcp.json', '.claude.json', '.claude\settings.local.json')) {
    $p = Join-Path $env:USERPROFILE $f
    if (Test-Path $p) { Add-CheckResult -Status 'PASS' -Item $f -Evidence "$((Get-Item $p).Length) 字节" }
}
# 凭据文件只确认存在性，绝不读取内容
$cred = Join-Path $env:USERPROFILE '.claude\.credentials.json'
Add-CheckResult -Status 'PASS' -Item '.claude\.credentials.json' -Evidence $(if (Test-Path $cred) { '存在（凭据文件，本工具不读取内容）' } else { '不存在' })

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows -FailHint 'Claude Code 的代理链路不完整，先修 settings.json 再启动。'

if ($OpenSettings -and (Test-Path $settingsPath)) { Start-Process notepad.exe $settingsPath }

if ($Json) {
    Write-JsonResult @{ script = 'Check-ClaudeCodeConfig.ps1'; ok = ($code -eq 0); detail = $payload; results = $rows }
}
exit $code
