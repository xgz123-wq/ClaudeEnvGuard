<#
.SYNOPSIS
    测试并打包 Claude 环境卫士（图形界面 ClaudeEnvGuard.exe + 命令行 ceg.exe）。
.DESCRIPTION
    默认：跑全部单元测试 → 构建两个 exe 到 build\bin。
    -Deploy：再把两个 exe 复制到上一级目录（D:\Tools\ClaudeEnvGuard），桌面图标指向的就是那里；
    覆盖前先把那里现有的两个 exe 另存为 ClaudeEnvGuard-v<旧版本>.exe、ceg-v<旧版本>.exe，已有同名备份就不动它；
    只留这一份上一版用来马上回退，更早的备份移到回收站（每个版本都在 GitHub Release 里）。
    依赖：Go（%LOCALAPPDATA%\Programs\go）、wails（%USERPROFILE%\go\bin）、Node。不用 conda。
#>
[CmdletBinding()]
param([switch] $Deploy, [switch] $SkipTests)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot

# 部署前留旧版：版本号问旧的 ceg.exe（exe 里没写版本资源）；问不到就用旧 exe 的修改时间
function Backup-Deployed([string] $Dest) {
    $gui = Join-Path $Dest 'ClaudeEnvGuard.exe'
    $cli = Join-Path $Dest 'ceg.exe'
    if (-not (Test-Path $gui) -and -not (Test-Path $cli)) { Write-Host '目标目录还没有程序，不用备份'; return }
    $ver = $null
    if (Test-Path $cli) {
        $out = @(& $cli version 2>$null)   # 先收完再取首行：管道里用 Select -First 会提前截断，$LASTEXITCODE 不会更新
        if ($LASTEXITCODE -eq 0 -and "$($out[0])".Trim() -match '^\d+\.\d+\.\d+$') { $ver = "$($out[0])".Trim() }
    }
    $tag = if ($ver) { "v$ver" } else { '旧-' + (Get-Item (@($gui, $cli) | Where-Object { Test-Path $_ } | Select-Object -First 1)).LastWriteTime.ToString('yyyyMMdd-HHmmss') }
    foreach ($src in $gui, $cli) {
        if (-not (Test-Path $src)) { continue }
        $bak = Join-Path $Dest ('{0}-{1}.exe' -f [IO.Path]::GetFileNameWithoutExtension($src), $tag)
        if (Test-Path $bak) { Write-Host "已有 $(Split-Path $bak -Leaf)，保留原备份不覆盖"; continue }
        Copy-Item $src $bak   # 复制不是移动：后面覆盖失败时，原程序还在
        Write-Host "旧版另存为 $(Split-Path $bak -Leaf)"
    }
    # 只留上一版：更早的备份移到回收站，不直接删
    Add-Type -AssemblyName Microsoft.VisualBasic
    Get-ChildItem $Dest -File | Where-Object { $_.Name -match '^(ClaudeEnvGuard|ceg)-(v|旧-).+\.exe$' -and $_.BaseName -notlike "*-$tag" } | ForEach-Object {
        [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile($_.FullName, 'OnlyErrorDialogs', 'SendToRecycleBin')
        Write-Host "更早的备份 $($_.Name) 已移到回收站"
    }
}
$env:PATH = "$env:LOCALAPPDATA\Programs\go\bin;$env:USERPROFILE\go\bin;$env:PATH"
# 国内拉依赖走 goproxy.cn；不改系统代理设置，只对本进程生效
$env:GOPROXY = 'https://goproxy.cn,direct'
foreach ($v in 'HTTPS_PROXY', 'HTTP_PROXY', 'ALL_PROXY', 'https_proxy', 'http_proxy', 'all_proxy') { Remove-Item "env:$v" -ErrorAction SilentlyContinue }

Push-Location $root
try {
    # 新克隆的仓库没有 frontend\dist，main 包的 go:embed 会编译失败；先放占位文件，wails build 会重新生成
    if (-not (Test-Path frontend\dist)) {
        New-Item -ItemType Directory frontend\dist | Out-Null
        Set-Content frontend\dist\placeholder.txt 'wails build 会覆盖本目录'
    }
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
        Backup-Deployed $dest
        Copy-Item build\bin\ClaudeEnvGuard.exe, build\bin\ceg.exe -Destination $dest -Force
        Write-Host "已部署到 $dest" -ForegroundColor Green
    }
}
finally {
    Pop-Location
}
