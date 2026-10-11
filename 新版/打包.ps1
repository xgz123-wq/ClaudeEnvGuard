<#
.SYNOPSIS
    测试并打包 Claude 环境卫士（图形界面 ClaudeEnvGuard.exe + 命令行 ceg.exe）。
.DESCRIPTION
    默认：跑全部单元测试 → 构建两个 exe 到 build\bin。
    -Deploy：再把两个 exe 复制到上一级目录（D:\Tools\ClaudeEnvGuard），桌面图标指向的就是那里。
    依赖：Go（%LOCALAPPDATA%\Programs\go）、wails（%USERPROFILE%\go\bin）、Node。不用 conda。
#>
[CmdletBinding()]
param([switch] $Deploy, [switch] $SkipTests)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$env:PATH = "$env:LOCALAPPDATA\Programs\go\bin;$env:USERPROFILE\go\bin;$env:PATH"
# 国内拉依赖走 goproxy.cn；不改系统代理设置，只对本进程生效
$env:GOPROXY = 'https://goproxy.cn,direct'
foreach ($v in 'HTTPS_PROXY', 'HTTP_PROXY', 'ALL_PROXY', 'https_proxy', 'http_proxy', 'all_proxy') { Remove-Item "env:$v" -ErrorAction SilentlyContinue }

Push-Location $root
try {
    if (-not $SkipTests) {
        Write-Host '== 单元测试 ==' -ForegroundColor Cyan
        go test ./...
        if ($LASTEXITCODE) { throw '单元测试失败，停止打包' }
    }
    Write-Host '== 构建图形界面 ==' -ForegroundColor Cyan
    wails build -trimpath -ldflags '-s -w'
    if ($LASTEXITCODE) { throw '图形界面构建失败' }
    Write-Host '== 构建命令行 ==' -ForegroundColor Cyan
    go build -trimpath -ldflags '-s -w' -o build\bin\ceg.exe .\cmd\ceg
    if ($LASTEXITCODE) { throw '命令行构建失败' }
    Get-ChildItem build\bin\*.exe | ForEach-Object { '{0,-22} {1,6:N1} MB' -f $_.Name, ($_.Length / 1MB) }

    if ($Deploy) {
        $dest = Split-Path $root -Parent
        $running = Get-Process ClaudeEnvGuard -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$dest\*" }
        if ($running) { throw "环境卫士正在运行（PID $($running.Id -join ',')），请先关闭窗口再部署" }
        Copy-Item build\bin\ClaudeEnvGuard.exe, build\bin\ceg.exe -Destination $dest -Force
        Write-Host "已部署到 $dest" -ForegroundColor Green
    }
}
finally {
    Pop-Location
}
