#requires -Version 7.0
<#
.SYNOPSIS
    第 9 层：套件自包含性检查（相对路径 / 外部依赖 / PowerShell 版本守卫 / 必需文件 / 快捷方式基线）。

.DESCRIPTION
    本套件承诺"整个目录拷到别处也能跑"。这个承诺必须能被机器验证，否则会悄悄烂掉。

    真实事故：套件化搬迁时，两个脚本的兄弟文件引用没跟着改
    （新布局把文件拆到了 scripts\apply\ 与 scripts\clash\ 两个目录），
    脚本跑到一半才 throw「缺少必需文件」—— 而当时的验收层全绿，没人发现。
    所以本层的存在意义就是：把这类"静态就能查出来"的断裂挡在运行之前。

    做法：用 AST 静态解析 scripts\**.ps1 里所有 Join-Path，把基准表达式还原成真实路径。
    能还原的形式：
      $PSScriptRoot / (Split-Path … -Parent) / 嵌套 Join-Path
      $root = Get-SkillRoot / $x = Get-ScriptsRoot / 由上述派生出的中间变量
    还原不出基准的（例如 Join-Path $layer.File $x）不报错，只计数跳过。

    然后按扩展名分两类：
      · 输入型（.ps1 .psm1 .psd1 .js .cjs .mjs .cs .yaml .yml .ico .exe .bat .cmd）与无扩展名目录
        —— 必须存在，缺了就是 FAIL
      · .json 等运行时生成的状态文件 —— 跳过（不跳必然误报）

    另外扫五件事：
      · 脚本里是否残留工作区绝对路径 —— 那是外部依赖，拷走就断
      · 是否还有脚本用 powershell.exe（Windows PowerShell 5.1）去调子脚本
        —— 无 BOM UTF-8 中文脚本会被按 ANSI 解析，直接乱码/失败
      · #requires -Version 7.0 的覆盖率（27 个脚本应全覆盖）
      · 「用 PowerShell 7 运行」右键入口是否已注册，并用 Shell.Application 真的把
        右键菜单枚举一遍确认它真的出现在菜单里（可选加固；默认没有，
        跑 scripts\apply\启用-PowerShell7入口.ps1 -Apply 启用，-Rollback 可撤）
      · Common.ps1 / Topology.psd1 / 关键脚本 / 各层 check / 快捷方式基线备份是否都在位

    注：本脚本自身不参与"外部依赖残留"和"5.1 残留"两行扫描，否则会自己匹配自己。
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
$scriptsRoot = Get-ScriptsRoot
$script:SkillRootAbs = $root
$script:ScriptsRootAbs = $scriptsRoot
Write-Banner '第 9 层 · 套件自包含性检查' 'AST 解析相对路径 · 外部依赖残留 · PowerShell 版本守卫 · 必需文件'

$SELF = 'Check-SuiteIntegrity.ps1'
$mustExistExt = @('.ps1', '.psm1', '.psd1', '.js', '.cjs', '.mjs', '.cs', '.yaml', '.yml', '.ico', '.exe', '.bat', '.cmd')

$resolvedHits = [System.Collections.Generic.List[object]]::new()
$brokenHits = [System.Collections.Generic.List[object]]::new()
$parseErrors = [System.Collections.Generic.List[object]]::new()
$extPathHits = [System.Collections.Generic.List[object]]::new()
$ps51Hits = [System.Collections.Generic.List[object]]::new()
$noRequires = [System.Collections.Generic.List[string]]::new()
$skipChildVar = 0; $skipGenerated = 0; $skipBase = 0; $skipProbe = 0

$varAst = [System.Management.Automation.Language.VariableExpressionAst]
$assignAst = [System.Management.Automation.Language.AssignmentStatementAst]
$cmdAst = [System.Management.Automation.Language.CommandAst]
$whileAst = [System.Management.Automation.Language.WhileStatementAst]
$strAst = [System.Management.Automation.Language.StringConstantExpressionAst]

function Resolve-NodeBase {
    param($Node, [string] $FileDir, [hashtable] $Vars)

    if ($null -eq $Node) { return $null }

    if ($Node -is [System.Management.Automation.Language.VariableExpressionAst]) {
        $n = $Node.VariablePath.UserPath
        if ($n -ieq 'PSScriptRoot') { return $FileDir }
        if ($n -match '[.:]') { return $null }
        if ($Vars.ContainsKey($n)) { return $Vars[$n] }
        return $null
    }

    if ($Node -is [System.Management.Automation.Language.ParenExpressionAst]) {
        return Resolve-NodeBase -Node $Node.Pipeline -FileDir $FileDir -Vars $Vars
    }

    if ($Node -is [System.Management.Automation.Language.PipelineAst]) {
        $pl = @($Node.PipelineElements)
        if ($pl.Count -ne 1) { return $null }
        return Resolve-NodeBase -Node $pl[0] -FileDir $FileDir -Vars $Vars
    }

    if ($Node -is [System.Management.Automation.Language.CommandExpressionAst]) {
        return Resolve-NodeBase -Node $Node.Expression -FileDir $FileDir -Vars $Vars
    }

    if ($Node -is [System.Management.Automation.Language.CommandAst]) {
        $name = $Node.GetCommandName()
        if (-not $name) { return $null }
        if ($name -ieq 'Get-SkillRoot') { return $script:SkillRootAbs }
        if ($name -ieq 'Get-ScriptsRoot') { return $script:ScriptsRootAbs }

        $els = @($Node.CommandElements)
        $pos = @($els | Select-Object -Skip 1 |
                Where-Object { $_ -isnot [System.Management.Automation.Language.CommandParameterAst] })

        if ($name -ieq 'Split-Path') {
            if ($pos.Count -lt 1) { return $null }
            $inner = Resolve-NodeBase -Node $pos[0] -FileDir $FileDir -Vars $Vars
            if (-not $inner) { return $null }
            return ([System.IO.Path]::GetDirectoryName($inner))
        }
        if ($name -ieq 'Join-Path') {
            if ($pos.Count -lt 2) { return $null }
            $b = Resolve-NodeBase -Node $pos[0] -FileDir $FileDir -Vars $Vars
            if (-not $b) { return $null }
            if ($pos[1] -isnot [System.Management.Automation.Language.StringConstantExpressionAst]) { return $null }
            return (Join-Path $b $pos[1].Value)
        }
        return $null
    }
    return $null
}

# ------------------------------------------------- 1. 逐脚本静态解析
Write-Section '相对路径解析（scripts\**.ps1 里的 Join-Path）'
$allScripts = @(Get-ChildItem -LiteralPath $scriptsRoot -Recurse -File -Filter '*.ps1' -ErrorAction SilentlyContinue |
        Sort-Object FullName)

foreach ($f in $allScripts) {
    $rel = $f.FullName.Substring($root.Length + 1)
    $tokens = $null; $errs = $null; $ast = $null
    try { $ast = [System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$tokens, [ref]$errs) }
    catch { $parseErrors.Add([pscustomobject]@{ File = $rel; Message = $_.Exception.Message }); continue }
    foreach ($e in @($errs)) { $parseErrors.Add([pscustomobject]@{ File = $rel; Message = "第 $($e.Extent.StartLineNumber) 行：$($e.Message)" }) }
    if (-not $ast) { continue }

    $text = [System.IO.File]::ReadAllText($f.FullName)
    if ($text -notmatch '#requires\s+-Version\s+7') { $noRequires.Add($rel) }

    if ($f.Name -ne $SELF) {
        # 工作区绝对路径：按行扫 —— 注释里写过也算问题（说明真的写死过）
        $n = 0
        $lines = @([System.IO.File]::ReadAllLines($f.FullName))
        foreach ($line in $lines) {
            $n++
            if ($line -match 'AI应用开发') { $extPathHits.Add([pscustomobject]@{ File = $rel; Line = $n; Text = $line.Trim() }) }
        }

        # powershell.exe 残留：按字符偏移扫，并剔除「注释里的提及」。
        # 注释里写"不要用 powershell.exe"是说明文字，不是调用；但字符串常量
        # （$exe = 'powershell.exe'）必须照旧算命中 —— 当年的真实事故就是字符串形态。
        $commentSpans = @($tokens | Where-Object {
                $_.Kind -eq [System.Management.Automation.Language.TokenKind]::Comment -or
                $_.Kind -eq [System.Management.Automation.Language.TokenKind]::BlockComment
            } | ForEach-Object { [pscustomobject]@{ S = $_.Extent.StartOffset; E = $_.Extent.EndOffset } })

        $lineStarts = [System.Collections.Generic.List[int]]::new()
        $lineStarts.Add(0)
        for ($ci = 0; $ci -lt $text.Length; $ci++) { if ($text[$ci] -eq [char]10) { $lineStarts.Add($ci + 1) } }

        foreach ($m in [regex]::Matches($text, 'powershell\.exe')) {
            $off = $m.Index
            if (@($commentSpans | Where-Object { $off -ge $_.S -and $off -lt $_.E }).Count -gt 0) { continue }
            $ln = 1
            for ($i = 0; $i -lt $lineStarts.Count; $i++) { if ($lineStarts[$i] -le $off) { $ln = $i + 1 } else { break } }
            $lnText = if ($ln -le $lines.Count) { $lines[$ln - 1].Trim() } else { '' }
            $ps51Hits.Add([pscustomobject]@{ File = $rel; Line = $ln; Text = $lnText })
        }
    }

    $fileDir = [System.IO.Path]::GetDirectoryName($f.FullName)

    # 惯用法①：「while (… Test-Path …) { $x = Split-Path $x -Parent }」向上找 Common.ps1，
    #   走完之后 $x 就是存放 Common.ps1 的目录（即 scripts\）。不认这个惯用法必然误报。
    $probeRanges = [System.Collections.Generic.List[object]]::new()
    foreach ($w in @($ast.FindAll({ param($n) $n -is $whileAst }, $true))) {
        $probeRanges.Add([pscustomobject]@{ S = $w.Condition.Extent.StartOffset; E = $w.Condition.Extent.EndOffset })
    }

    $allAssigns = @($ast.FindAll({ param($n) $n -is $assignAst }, $true) |
            Where-Object { $_.Left -is $varAst -and $_.Left.VariablePath.UserPath -notmatch '[.:]' })

    # 被赋值多次的变量 = 运行期被改写过（典型是向上查找的游标），静态判不出来 → 一律不猜。
    # 唯一例外是上面认得的 walk 惯用法，它的终值确定。
    $walkVars = @{}
    foreach ($w in @($ast.FindAll({ param($n) $n -is $whileAst }, $true))) {
        foreach ($a in @($w.Body.FindAll({ param($n) $n -is $assignAst }, $true))) {
            if ($a.Left -isnot $varAst) { continue }
            $vn = $a.Left.VariablePath.UserPath
            if ($a.Right.Extent.Text -match ('Split-Path\s+\$' + [regex]::Escape($vn) + '\s+-Parent')) { $walkVars[$vn] = $true }
        }
    }
    $assignCount = @{}
    foreach ($a in $allAssigns) {
        $vn = $a.Left.VariablePath.UserPath
        $assignCount[$vn] = 1 + [int]$assignCount[$vn]
    }

    $vars = @{}
    foreach ($a in $allAssigns) {
        $vn = $a.Left.VariablePath.UserPath
        if ($walkVars.ContainsKey($vn)) { $vars[$vn] = $script:ScriptsRootAbs; continue }
        if ([int]$assignCount[$vn] -gt 1) { continue }
        if ($vars.ContainsKey($vn)) { continue }
        $rv = Resolve-NodeBase -Node $a.Right -FileDir $fileDir -Vars $vars
        if ($rv) { $vars[$vn] = $rv }
    }

    $cmds = @($ast.FindAll({ param($n) $n -is $cmdAst }, $true) |
            Where-Object { $_.GetCommandName() -ieq 'Join-Path' })
    foreach ($cmd in $cmds) {
        # 惯用法②：while 条件里的 Join-Path 是"试探这个位置有没有"，找不到是正常的 → 不算依赖
        $cs = $cmd.Extent.StartOffset; $ce = $cmd.Extent.EndOffset
        if (@($probeRanges | Where-Object { $cs -ge $_.S -and $ce -le $_.E }).Count -gt 0) { $skipProbe++; continue }

        $els = @($cmd.CommandElements)
        $pos = @($els | Select-Object -Skip 1 |
                Where-Object { $_ -isnot [System.Management.Automation.Language.CommandParameterAst] })
        if ($pos.Count -lt 2) { continue }

        $child = $pos[1]
        if ($child -isnot $strAst) { $skipChildVar++; continue }
        $leaf = $child.Value
        $ext = [System.IO.Path]::GetExtension($leaf)
        if ($ext -and ($mustExistExt -notcontains $ext.ToLowerInvariant())) { $skipGenerated++; continue }

        $full = Resolve-NodeBase -Node $cmd -FileDir $fileDir -Vars $vars
        if (-not $full) { $skipBase++; continue }

        if (Test-Path -LiteralPath $full) {
            $resolvedHits.Add([pscustomobject]@{ File = $rel; Line = $cmd.Extent.StartLineNumber; Resolved = $full })
        }
        else {
            $brokenHits.Add([pscustomobject]@{
                    File     = $rel
                    Line     = $cmd.Extent.StartLineNumber
                    Leaf     = $leaf
                    Resolved = $full
                    Text     = $cmd.Extent.Text
                })
        }
    }
}

$skipNote = "（另有 $skipChildVar 条子路径是变量、$skipGenerated 条是 .json 等运行时生成、$skipBase 条基准非 skill 根派生、$skipProbe 条是向上查找时的试探 —— 均跳过）"
if ($brokenHits.Count -eq 0) {
    Add-CheckResult -Status 'PASS' -Item '脚本相对路径' `
        -Evidence "$($allScripts.Count) 个脚本、$($resolvedHits.Count) 条可用静态还原的兄弟文件引用全部解析成功 $skipNote"
}
else {
    foreach ($b in $brokenHits) {
        Add-CheckResult -Status 'FAIL' -Item $b.File -Evidence "第 $($b.Line) 行 → $($b.Leaf) 不存在：$($b.Resolved)"
    }
}

if ($parseErrors.Count -gt 0) {
    foreach ($p in $parseErrors) { Add-CheckResult -Status 'FAIL' -Item $p.File -Evidence "语法错误 $($p.Message)" }
}
else {
    Add-CheckResult -Status 'PASS' -Item '脚本语法' -Evidence "$($allScripts.Count) 个脚本全部通过 AST 解析"
}

# ------------------------------------------------- 2. 外部依赖残留
Write-Section '外部依赖残留（拷走就断的东西）'
if ($extPathHits.Count -eq 0) {
    Add-CheckResult -Status 'PASS' -Item '工作区绝对路径' -Evidence '脚本里没有任何指向工作区根目录的硬绑定，整套目录可整体搬移'
}
else {
    foreach ($h in $extPathHits) {
        Add-CheckResult -Status 'FAIL' -Item $h.File -Evidence "第 $($h.Line) 行硬编码了工作区绝对路径，拷走就断：$($h.Text)"
    }
}

# ------------------------------------------------- 3. PowerShell 版本守卫
Write-Section 'PowerShell 版本守卫（5.1 会按 ANSI 解析无 BOM UTF-8 中文脚本）'
Add-CheckResult -Status $(if ($PSVersionTable.PSVersion.Major -ge 7) { 'PASS' } else { 'FAIL' }) -Item '当前引擎' `
    -Evidence "PowerShell $($PSVersionTable.PSVersion)（$($PSVersionTable.PSEdition)）"
if ($ps51Hits.Count -eq 0) {
    $reqNote = if ($noRequires.Count -eq 0) { '全部脚本都带 #requires -Version 7.0' }
    else { "$($noRequires.Count)/$($allScripts.Count) 个脚本没写 #requires -Version 7.0（仅少一道保险，不影响正确性）" }
    Add-CheckResult -Status 'PASS' -Item 'powershell.exe 调用残留' -Evidence "0 处；脚本一律用 pwsh 7。$reqNote"
}
else {
    foreach ($h in $ps51Hits) {
        Add-CheckResult -Status 'FAIL' -Item $h.File -Evidence "第 $($h.Line) 行仍用 powershell.exe（5.1）调子脚本，中文会乱码：$($h.Text)"
    }
}

# 「用 PowerShell 7 运行」右键入口 —— 只写 HKCU 的可选加固，不启用也不影响主链路。
# 系统自带的「使用 PowerShell 运行」走的是 5.1，这个入口是给它旁边加一个不会被误选的替代。
$ps7Leaf = 'HKCU:\Software\Classes\SystemFileAssociations\.ps1'
$ps7Verb = $ps7Leaf + '\shell\RunWithPwsh7'
$ps7CmdKey = $ps7Verb + '\Command'
$ps7Exe = (Get-Process -Id $PID).Path
if (-not $ps7Exe -or $ps7Exe -notmatch 'pwsh\.exe$') { $ps7Exe = "$env:ProgramFiles\PowerShell\7\pwsh.exe" }
$ps7Want = '"{0}" -NoLogo -NoExit -ExecutionPolicy Bypass -File "%1"' -f $ps7Exe
$ps7Got = if (Test-Path -LiteralPath $ps7CmdKey) { (Get-Item -LiteralPath $ps7CmdKey).GetValue('') } else { $null }

if ($ps7Got -eq $ps7Want) {
    Add-CheckResult -Status 'PASS' -Item 'PS7 右键入口' `
        -Evidence '「用 PowerShell 7 运行」已注册；手工跑 .ps1 时不会误落到 5.1（撤销：-Rollback）'

    # 光看注册表不等于菜单里真有 —— 用 Shell.Application 真的把右键菜单枚举一遍。
    # 这一步纯只读、不弹窗、<1s，所以不需要人工目视。
    $ps7Probe = $script:SkillRoot + '\scripts\Claude环境一键总检.ps1'
    $ps7Names = @()
    $ps7ProbeErr = ''
    $shell = $null
    if (Test-Path -LiteralPath $ps7Probe) {
        try {
            $shell = New-Object -ComObject Shell.Application
            $folder = $shell.NameSpace([System.IO.Path]::GetDirectoryName($ps7Probe))
            $item = $folder.ParseName([System.IO.Path]::GetFileName($ps7Probe))
            if ($item) {
                foreach ($v in $item.Verbs()) {
                    $n = [string]$v.Name
                    if ($n) { $ps7Names += (($n -replace '&', '').Trim()) }
                }
            }
            else { $ps7ProbeErr = 'ParseName 返回空' }
        }
        catch { $ps7ProbeErr = $_.Exception.Message }
        finally { if ($shell) { try { [void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($shell) } catch { } } }
    }
    else { $ps7ProbeErr = "探测目标不存在：$ps7Probe" }

    $ps7Hit = @($ps7Names | Where-Object { $_ -match 'PowerShell 7' })
    if ($ps7Hit.Count -gt 0) {
        Add-CheckResult -Status 'PASS' -Item 'PS7 右键入口（实盘）' `
            -Evidence "Shell 右键菜单共 $($ps7Names.Count) 项，命中「$($ps7Hit[0])」"
    }
    elseif ($ps7ProbeErr) {
        Add-CheckResult -Status 'WARN' -Item 'PS7 右键入口（实盘）' `
            -Evidence "注册表已写对，但菜单枚举失败：$ps7ProbeErr（不影响主链路）"
    }
    else {
        Add-CheckResult -Status 'WARN' -Item 'PS7 右键入口（实盘）' `
            -Evidence "注册表已写对，但 Shell 菜单 $($ps7Names.Count) 项里没枚举到 → 重启资源管理器后复看"
    }
}
elseif ($ps7Got) {
    Add-CheckResult -Status 'WARN' -Item 'PS7 右键入口' `
        -Evidence "命令与期望不一致，重跑 -Apply 可覆盖。现在：$ps7Got"
}
else {
    Add-CheckResult -Status 'WARN' -Item 'PS7 右键入口' `
        -Evidence '未注册（可选加固，不影响主链路）；启用：pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Apply'
}

# ------------------------------------------------- 4. 必需文件在位
Write-Section '必需文件在位'
$mustHave = @(
    'scripts\Common.ps1'
    'config\Topology.psd1'
    'scripts\Invoke-ClaudeSafetyCheck.ps1'
    'scripts\Claude环境一键总检.ps1'
    'scripts\clash\Clash全局账号通道.js'
    'scripts\clash\验证Clash账号专用通道.ps1'
    'scripts\clash\Claude日常五项自检.ps1'
    'scripts\desktop\启动Claude英文浏览器.ps1'
    'scripts\desktop\维护文件\专用通道管理.ps1'
    'scripts\desktop\维护文件\修复共用DNS.ps1'
    'scripts\desktop\维护文件\应用直连防护管理.ps1'
    'scripts\apply\Repair-Shortcuts.ps1'
    'scripts\apply\更新Clash通用规则.ps1'
    'scripts\apply\安装Clash账号专用通道.ps1'
)
$layerScripts = @('Check-ProxyStack.ps1', 'Check-ClaudeCodeConfig.ps1', 'Check-ChromeEntry.ps1', 'Check-DesktopConfig.ps1',
    'Check-WindowsFallback.ps1', 'Check-PortConsistency.ps1', 'Check-IpReputation.ps1', 'Check-ClaudeIp.ps1', 'Check-SuiteIntegrity.ps1')
$missingMust = @($mustHave | Where-Object { -not (Test-Path -LiteralPath (Join-Path $root $_)) })
$missingLayers = @($layerScripts | Where-Object { -not (Test-Path -LiteralPath (Join-Path $scriptsRoot ('checks\' + $_))) })
if ($missingMust.Count -eq 0 -and $missingLayers.Count -eq 0) {
    Add-CheckResult -Status 'PASS' -Item '核心文件' -Evidence "关键脚本/真源 $($mustHave.Count) 个、各层 check $($layerScripts.Count) 个全部在位"
}
else {
    foreach ($m in $missingMust) { Add-CheckResult -Status 'FAIL' -Item '核心文件' -Evidence "缺少：$m" }
    foreach ($m in $missingLayers) { Add-CheckResult -Status 'FAIL' -Item 'check 层' -Evidence "缺少：scripts\checks\$m" }
}

# ------------------------------------------------- 5. 快捷方式基线备份
Write-Section '桌面快捷方式基线备份（还原源）'
$baselineDir = Join-Path $root 'assets\shortcuts'
$baselineLnks = @(if (Test-Path -LiteralPath $baselineDir) {
        Get-ChildItem -LiteralPath $baselineDir -Filter '*.lnk' -File -ErrorAction SilentlyContinue
    })
$declared = @($Topology.DesktopShortcuts)
$missingBaseline = @($declared | Where-Object { $baselineLnks.Name -notcontains $_.Name })
if ($baselineLnks.Count -ge 5 -and $missingBaseline.Count -eq 0) {
    Add-CheckResult -Status 'PASS' -Item 'assets\shortcuts' -Evidence "$($baselineLnks.Count) 个原版 .lnk 备份在位（3 个脚本类 + 2 个浏览器类），可作为还原源"
}
else {
    Add-CheckResult -Status 'WARN' -Item 'assets\shortcuts' `
        -Evidence "备份不全：现有 $($baselineLnks.Count) 个$(if ($missingBaseline.Count) { '；缺 ' + (($missingBaseline.Name) -join ', ') })"
}

$rows = @($script:CheckResults)
$code = Write-Verdict -Rows $rows `
    -FailHint '套件已经不自包含（兄弟引用断裂或残留外部依赖）—— 拷到别处会跑不起来，先照 Evidence 修。' `
    -WarnHint '快捷方式备份不全：桌面图标被误改时的还原能力会打折，建议从上游补回 assets\shortcuts。'

if ($Json) {
    Write-JsonResult @{
        script   = 'Check-SuiteIntegrity.ps1'
        ok       = ($code -eq 0)
        resolved = $resolvedHits
        broken   = $brokenHits
        results  = $rows
    }
}
exit $code
