# 变更与回滚 SOP

> 总原则：**能只改开关就不改文件；能只改全局脚本就不碰订阅。** 每次变更后必跑一键总检。
> Claude 域名的每周与更新后复核方法只维护在 `docs/配置/Claude环境/说明/DNS分层说明.md` 的「Claude 域名定期复核」；新增域名须核对官方清单与实际连接后再改规则。
>
> | SOP | 什么时候用 | 要动系统吗 |
> |---|---|---|
> | [SOP-1](#sop-1-导入新订阅后复核最常用) | 导入新订阅 / 换节点后复核 | 否 |
> | [SOP-2](#sop-2-claude-desktop-升级后复核最容易翻车) | Claude Desktop 升级后 | 否（但需提权重签 WFP） |
> | [SOP-3](#sop-3-改端口或改组名) | 改端口 / 改组名 | 否 |
> | [SOP-3B](#sop-3b-桌面快捷方式校验与还原不接管) | 怀疑桌面快捷方式被改 | 否 |
> | [SOP-3C](#sop-3c-启用--撤销-powershell-7-右键入口可选加固纯-hkcu) | 想给 `.ps1` 加「用 PS7 运行」 | 否，纯 HKCU |
> | [SOP-3D](#sop-3d-提权复核-wfp-实盘唯一需要点-uac-的一步) | 第 5 层报 WFP 未复核 | **点一次 UAC**（30 天一次） |
> | [SOP-4](#sop-4-回滚) | 要撤销某项变更 | 视变更而定 |
> | [SOP-5](#sop-5-变更后必须做的事不分场景) | 任何变更后的收尾 | 否 |

## SOP-1 导入新订阅后复核（最常用）

**核心结论：脚本与规则一行都不用改。** 因为 `Script.js` 的 `ACCOUNT-US` 组声明了
`include-all-proxies: true`，会自动吃下新订阅的全部节点；Claude 置顶规则是全局注入的，与订阅无关。

### 5 步复核清单

| 步 | 做什么 | 期望 | 不通过怎么办 |
|---|---|---|---|
| 1 | **在 `ACCOUNT-US` 组里手动挑一个节点** | 组非空、已选中 | 自动填充**不会自动选中**。若组为空 → 新订阅没有可用节点，换订阅 |
| 2 | **跑 9 层一键全检**（pwsh 7） | `0 FAIL`，退出码 `0`；WARN 逐条确认 | 有 FAIL 按 `02-验收与排查手册.md` 决策树查 |
| 3 | **跑 IP 检测两个脚本**：`checks\Check-IpReputation.ps1` + `checks\Check-ClaudeIp.ps1` | 见 `04-Ip信誉检测.md` 合格线 | 命中机房/黑名单 → 换节点或换订阅 |
| 4 | **人工看 `ippure.com`** 确认新出口（唯一仍需人眼的站） | 页面 IP 与脚本一致 | 不一致 → 说明某个入口没走 `ACCOUNT-US` |
| 5 | （可选，与 Claude 链路无关）旧订阅的 per-profile 覆写里若留着 `delete:` 补丁 | 目标在新订阅里仍存在 | 不存在 → 删掉那行（无害但不干净）。**本套不依赖订阅级覆写，不做这步也不影响 Claude 链路** |

### 换订阅后**自动生效**的部分（不用管）

`ACCOUNT-US` 组重建 · Claude 专用域名 + IP-ASN 置顶规则 · `43866` / `43867` listener ·
TUN `strict-route` + `dns-hijack` · Claude 专属 DNS policy · `geox-url.asn` 兜底。

### 换订阅后**需要人工**的部分

① 手动重选节点（见上表步 1）；② 新订阅自带的 Claude 规则会被自动清掉（`isManagedClaudeRule()`），
**不需要手工去重**；③（可选）清理旧订阅遗留的 `delete:` 补丁 —— 本套不依赖订阅级覆写，不做也不影响。

### 不要做的事

- ❌ 不要在系统环境变量里设 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `ANTHROPIC_BASE_URL`
- ❌ 不要给 Claude 设 `ANTHROPIC_BASE_URL` 指向中转
- ❌ 不要在 TUN 开着的同时再开系统代理（双抓、难排查）
- ❌ 不要开启 TUN 的自动更新（用户有意固定版本）
- ❌ 不要在 Claude 使用期间狂切节点/国家

## SOP-2 Claude Desktop 升级后复核（最容易翻车）

Desktop 是 MSIX 包，升级会换目录名 `WindowsApps\Claude_<版本>_x64__pzs8sxrjxfjjc`。
**WFP 精确阻断用的是 AppID（exe 完整路径）条件，路径一变规则就全部失效且不报错。**

### 必做 3 件事

1. **重签 WFP 8 条阻断**（需管理员）：
   - 更新 `维护文件\应用过滤目标.json` 里的 4 个 exe 路径与 SHA256（`PackageVersion` 也要改）
   - 用**管理员**运行 `应用直连防护管理.ps1` 的 Install
   - 再用**管理员**运行 Verify，确认凭证是 `passed`（不要只看 `Install` 记录就收工）
2. **复验注册表同层**：`HKLM\SOFTWARE\Policies\Claude` 里 `disableAutoUpdates=1` **与**
   `egressProxyUrl=http://127.0.0.1:43867` 必须**同时存在**。缺任一个 → Desktop 会**忽略代理回退直连**。
3. **复验 43867 出口**：
   ```powershell
   curl.exe --max-time 10 -s --proxy http://127.0.0.1:43867 https://api.ipify.org
   ```
   应返回美国住宅出口 IP，且与 43865 / 43866 一致。

### 顺带检查

- `%LOCALAPPDATA%\Claude-3p\configLibrary\_meta.json` 的 `appliedId` 是否仍指向有效文件
- 桌面 Claude 相关 `.lnk` 是否仍存在、是否与 `assets\shortcuts\` 基线一致（用 `Repair-Shortcuts.ps1` 预览模式比对，它只报告不写盘）
- 跑 `启动Claude英文浏览器.ps1 -CheckOnly` 应 `[PASS]` 而非 `[BLOCK]`

### 已知历史

Desktop 每次升级都会换 `WindowsApps\Claude_<版本>_x64__pzs8sxrjxfjjc` 目录名，WFP 精确规则随之**全部失效且不报错** —— 这已多次发生，属常态，升级后按 SOP-2 重签即可。

## SOP-3 改端口或改组名

**先改真源，再照清单同步，最后跑全检。**

端口 / 组名的**唯一真源**是 `config\Topology.psd1` 的 `Ports` / `Groups`。
编排器、9 个 checks、`Repair-Shortcuts.ps1` 都从这里取，改真源即对它们生效。

但仍有 **legacy 字面量**需要手工同步（文件和行数以 `Check-PortConsistency.ps1 -Json` 实时输出为准）：

| 类别 | 说明 |
|---|---|
| `Script.js`（`scripts\clash\Clash全局账号通道.js` → live） | listener 端口、组名、规则目标、DNS policy 的 `#ACCOUNT-US` |
| `~\.claude\settings.json` | `ALL_PROXY / HTTPS_PROXY / HTTP_PROXY` |
| Desktop 专用 Chrome 快捷方式 | `--proxy-server=socks5://127.0.0.1:43866` |
| `HKLM\SOFTWARE\Policies\Claude` | `egressProxyUrl` |
| `configLibrary` profile 文件 | 同上 |
| `scripts\apply\` / `scripts\desktop\维护文件\` 下的一次性安装/回滚脚本 | 多为已执行完的历史脚本 |

### 正确流程

1. **拿清单**（不用人工找）：
   ```powershell
   pwsh -File scripts\checks\Check-PortConsistency.ps1 -Json
   ```
   输出的「字面量清单」直接给出 `文件:行号`，照单逐行同步。
2. 改 `config\Topology.psd1`。
3. 跑一次确认**无漂移 WARN**：
   ```powershell
   pwsh -File scripts\checks\Check-PortConsistency.ps1
   ```
   该脚本会扫同族端口号（`438xx` / `789x` / `909x` / `1080x`），**凡不在真源里的报 WARN**，这就是漏改告警。
4. 跑 `Invoke-ClaudeSafetyCheck.ps1` 确认 9 层 0 FAIL。
5. 若改了 Desktop 端口，**管理员**重跑 WFP 之外还要复验注册表 `egressProxyUrl`；若改了 Chrome 端口，
   手工同步两个专用 Chrome `.lnk` 的 `--proxy-server`（桌面即用户资产，套件不再改写它）。

### 为什么不全改成读真源

这些字面量散落在多个旧文件里，风险大于收益（很多是一次性已执行完的脚本）。
折中方案 = **真源 + 清单 + 漂移告警**，如实记录，不假装全自动。

## SOP-3B 桌面快捷方式：校验与还原（**不接管**）

> **原则**：桌面 `.lnk` 是**用户资产**，本套件不生成、不改写、不指定它们指向哪里。
> `Repair-Shortcuts.ps1` 唯一的动作是「**把桌面文件覆盖回原始基线**」。

```powershell
pwsh -File scripts\apply\Repair-Shortcuts.ps1           # 预览（默认，不写盘）
pwsh -File scripts\apply\Repair-Shortcuts.ps1 -Apply    # 用原版备份覆盖桌面文件
```

- **预览模式**逐个比对桌面 `.lnk` 与 `assets\shortcuts\` 基线的 SHA256，报 `same` / `drift` / `missing`。
- 只有 `-Apply` 才写盘，写盘后立刻复核哈希。
- 桌面另有 `Claude|KK-` 匹配但不在基线里的 `.lnk` → 只打 `[INFO]`，不动。

**基线内容**：本分享版**不带**原机器的 `.lnk`，`assets\shortcuts\` 里只有 `说明.md`。部署后应包含 3 个脚本类
（`Claude-环境一键总检` / `Claude-五项自检` / `Claude-英文浏览器`，参数指回 `docs\配置\` 原路径）
与各账号专用 Chrome 快捷方式；把这些桌面快捷方式复制进本目录，作为还原源。复制之前
`Check-SuiteIntegrity` 的快捷方式备份项报 FAIL，属预期。

### 为什么是这个方向

真实失效方向是 `skill 目录被移动/改名/删除 → 若图标曾指向 skill 内脚本则图标立刻失效`；
而 `docs\配置\` 是只读归档、位置稳定。所以「指回 `docs\配置\`」比「指向 skill 内部」更耐久。

位置无关的校验由第 3 层（`Check-ChromeEntry.ps1`）与第 5 层（`Check-WindowsFallback.ps1`）负责，
两者都只判断 **`-File` 指向的脚本是否存在**，不要求它在 skill 里；
第 9 层（`Check-SuiteIntegrity.ps1`）另外确认 `assets\shortcuts\` 备份仍在位。

### 已知的历史残留（不影响运行）

`scripts\desktop\维护文件\共用DNS修复状态.json` 与 `安装状态.json` 里的 `"Original"` 字段是
2026-09-10 那次操作写下的**历史记录**（值为 `docs\配置\...` 老路径），与它们配套的
`修复共用DNS.ps1` / `专用通道管理.ps1` 的「恢复」分支会往 `Original` 写回。
这两个操作**早已执行完成**，脚本是历史一次性工具。**若将来真要重跑它们的回滚分支，先把 `Original` 改成 skill 内路径。**


## SOP-3C 启用 / 撤销 PowerShell 7 右键入口（可选加固，纯 HKCU）

> **前提认知**：`powershell.exe`(5.1) 与 `pwsh.exe`(7) 是**两个不同名字、不同目录**的命令，
> **不存在冲突，也不需要"统一"**。5.1 是 Windows 系统组件，**不可卸载**（两个系统计划任务依赖它）。
> 本 SOP 只做一件小事：往 `.ps1` 的右键菜单里加一个「用 PowerShell 7 运行」。

```powershell
pwsh -File scripts\apply\启用-PowerShell7入口.ps1            # 报告当前状态（默认，不写盘）
pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Apply     # 写入 HKCU
pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Rollback  # 撤销 + 剪枝空键
```

**写入点（唯一）**：`HKCU:\Software\Classes\SystemFileAssociations\.ps1\shell\RunWithPwsh7`

| 值 | 内容 |
|---|---|
| `(default)` | `用 PowerShell 7 运行(&R)` |
| `Icon` | `<pwsh.exe 全路径>` |
| `Command\(default)` | `"<pwsh.exe 全路径>" -NoLogo -NoExit -ExecutionPolicy Bypass -File "%1"` |

**为什么用 `SystemFileAssociations\.ps1\shell\<Verb>`**：这是给某个扩展名加右键动词的**正确机制**，
**不改 `UserChoice` 关联、不需要管理员、不影响 Windows 自带的「使用 PowerShell 运行」(5.1)**；
`HKCU\Software\Classes` 会自动合并进 `HKCR` 供 Explorer 读取。

**验收**：
1. 脚本输出 `用 PowerShell 7 运行(&R)` 与期望命令一致（`PASS`）；
2. 第 9 层 `Check-SuiteIntegrity.ps1` 报 `[PASS] PS7 右键入口`（`-Rollback` 后变 `[WARN] 未注册（可选加固）`，**不是 FAIL**）；
3. **第 9 层已做实盘枚举**：`Check-SuiteIntegrity.ps1` 会通过 `Shell.Application` 的 `FolderItem.Verbs()`
   真实枚举该项的右键菜单（实测 20 项），命中「用 PowerShell 7 运行(&R)」即 PASS，未命中记 **WARN**（不记 FAIL）。
   想自己再目视一次也行：右键任意 `.ps1` → Win11 可能在「**显示更多选项**」里。

**刻意不做**：

| 不做 | 原因 |
|---|---|
| 卸载 5.1 | 系统组件 + `UninstallSMB1ClientTask` / `UninstallSMB1ServerTask` 两个计划任务依赖 |
| 把 `powershell.exe` 别名 / 替换成 pwsh 7 | 破坏系统组件调用面，风险 >> 收益 |
| 改 `.ps1` 双击默认关联 | `.ps1 UserChoice` 有哈希保护（`Hash=1IKizRopsZI=`），改写风险高；且 Win11 默认「记事本打开」是**故意的安全设计** |

**回滚**：`-Rollback` 一条命令删 verb 键并剪枝空 `shell` / `.ps1` 键（脚本已实测剪枝干净）。
即使不跑它，手工删 `HKCU:\Software\Classes\SystemFileAssociations\.ps1\shell\RunWithPwsh7` 整棵子树也一样。


## SOP-3D 提权复核 WFP 实盘（**唯一需要点 UAC 的一步**）

**什么时候要跑**：第 5 层报 `[WARN] Windows WFP 实盘核对：30 天内未提权复核`；或刚升级过 Claude Desktop。

**为什么必须提权**：WFP 内核过滤器只有管理员能读，非提权一律 `0x00000005 (ERROR_ACCESS_DENIED)`。
但"要提权"不等于"要人工判断" —— 本套件把它压成了**一条命令 + 点一次「是」**：

```powershell
# 弹一次 UAC，点「是」。之后写入缓存，30 天内第 5 层自动转 PASS，不再打扰你。
pwsh -File scripts\checks\Request-ElevatedVerify.ps1 -Refresh

# 随时查缓存还有效多久（有效且通过 = 退出码 0；无缓存 = 4；失效/不通过 = 1）
pwsh -File scripts\checks\Request-ElevatedVerify.ps1 -Status
```

| 结果 | 含义 |
|---|---|
| `[PASS]` 第 5 层 `Windows WFP 实盘核对` | 8 条过滤器实盘在位，缓存与当前清单绑定且新鲜（默认 30 天） |
| `[WARN] ... 30 天内未提权复核` | 缓存过期或还没建。跑 `-Refresh` 即可 |
| `[FAIL] ...` | 实盘确实缺规则 → 走 SOP-2 用 `应用直连防护管理.ps1` 重签 |
| `-Refresh` 退出码 `2` | 你在 UAC 弹窗点了「否」。**什么都没改**，随时可重跑 |

**它做了什么（可审计）**：读 `维护文件\应用过滤目标.json` 的 4 个目标 → 逐个 `Verify()`
（只读，零副作用）→ 记录 `PackageVersionMatches` 供第 4 层判断"Desktop 升级后是否已重签" →
写入绑定清单 SHA256、Desktop 版本和全部 GUID 的 `config\elevated-verify-cache.json`。**不写任何系统配置**。

**红线**：提权只用于「读 WFP 实盘 + 写这个缓存」。任何改系统配置的动作都不走提权路径。

**缓存过期后不会静默放行**：过期即 `[WARN]`，绝不会自己降级成 PASS。


## SOP-4 回滚

按时间顺序的项目内已有备份（`account-routing-backups\` 在 `%APPDATA%\io.github.clash-verge-rev.clash-verge-rev\` 下）：

| 备份目录（在 `维护文件\` 或同层） | 对应操作 |
|---|---|
| `account-routing-backups\20260910-121858-ClaudeDesktop专用入口` | 09-10 Desktop 专用通道建立（`安装状态.json` 记录 `Mode=global`） |
| `account-routing-backups\20260910-140516-共用DNS修复` | 09-10 共用 DNS 修复（`共用DNS修复状态.json`） |

`安装状态.json` / `共用DNS修复状态.json` 里记录了当时的 `ProfileId`、`ProxyUrl`、`Selections`、脚本哈希，
可以据此对比「现在 vs 当时」判断被改了什么。

**回滚步骤**：① 停 Claude（Code + Desktop）；② 从备份目录还原对应文件；③ 重跑一键总检；
④ 若涉及 WFP，用管理员重跑 Install + Verify。

## SOP-5 变更后必须做的事（不分场景）

1. 跑 **9 层一键全检**：
   ```powershell
   pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1
   ```
   期望 `0 FAIL`、退出码 `0`，WARN 逐条确认。（快查加 `-Fast`，不弹 UAC；只想某项加 `-Only proxy,desktop`）
   若 `ping0.cc` **没**被网站风控挑战页拦下（正常情况下是通的），L7 会多出 5 条 PASS、少 1 条 WARN，基线变 `119 / 1 / 0 / 0`（120 行）——
   这说明站点此刻没限流，是更好的结果。反过来，短时间内反复自动跑会触发它的挑战页，L7 从 12 行降为 8 行，多出一条 WARN。
   注意：**正常情况下结果行里不会出现 `MANUAL`**。只有 WFP 提权缓存过期且当前会话无法弹 UAC 时，L5/L4 才会降级为 `WARN`（不会 `FAIL`），
   此时跑 `pwsh -File scripts\checks\Request-ElevatedVerify.ps1 -Refresh` 点一次 UAC 即可恢复（实测约 9 秒，管 30 天）。
2. 跑 **人工验收主入口**（期望 14 项全 PASS）：
   ```powershell
   pwsh -File scripts\Claude环境一键总检.ps1 -OpenBrowser
   ```
3. 跑**网页层复核**出口信誉（若换过节点/订阅）：`ping0.cc` 与 `ip.net.coffee/claude/` 已由第 7/8 层自动渲染取数；
   只剩 `ippure.com` 需人眼，跑 `pwsh -File scripts\checks\Check-ClaudeIp.ps1 -OpenBrowser` 会直接打开并打印交接清单。
4. 做**人工网页验收** `https://ip.net.coffee/claude/`（六项全绿）—— 想自己复核时用，第 8 层已自动做了同样的事。
5. 若第 5 层报 WFP 未复核，跑一次 `pwsh -File scripts\checks\Request-ElevatedVerify.ps1 -Refresh`（见 SOP-3D）。
6. 更新 `references/00-baseline.md` 的实测数据与日期。

