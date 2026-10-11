# Claude 安全出口链路 · 一体化套件

> 让一台 Windows 机器上的 **Claude Code + Claude Desktop + 专用 Chrome** 稳定走同一条出口，并且随时能用检查脚本证明。
> 本文件给人看；命令、红线、误判坑、场景工作流都在 [SKILL.md](./SKILL.md)，AI 按那份执行。

| 项 | 值 |
|---|---|
| 平台 | Windows 10 / 11 x64 |
| 依赖 | Clash Verge Rev（内核 mihomo）、PowerShell **7**、Google Chrome、Claude Code CLI、Claude Desktop（MSIX） |
| 配置主体 | Clash Verge Rev 的**全局扩展脚本** `Script.js`（不是订阅文件） |
| 自包含 | 整个目录拷到别处照样能跑，不依赖外部路径 |
| 与 docs 的关系 | 桌面图标实际运行的是 `docs\配置\Claude环境\`（日常维护走 claude-maintenance）；本目录是自带副本和检查工具，两边同名脚本路径写法不同，不要互相覆盖 |

## 它解决什么问题

| 痛点 | 本套件的答案 |
|---|---|
| 「现在到底安全吗」没人能回答 | 一条命令跑 9 层检查，逐项给证据 |
| 换订阅后不知道要不要改配置 | 全局脚本用 `include-all-proxies: true` 与订阅解耦，配置不动，只重跑 IP 检测 |
| 换节点后三个入口悄悄走了不同出口 | 用 Cloudflare trace 逐入口比对 `ip / loc / colo / warp` |
| 改端口漏改某个文件 | 端口收敛到 `config\Topology.psd1` 单一真源，不在真源里的同族端口报 WARN |

- **三个入口**：Claude Code `43865`（HTTP）、专用 Chrome `43866`（SOCKS5）、Claude Desktop `43867`（HTTP），都汇到 `ACCOUNT-US` 组。
- **只代理 Claude**：TUN + 规则分流，系统代理关闭。
- **四层兜底**：置顶规则防走错组；TUN strict-route + 53 劫持防 DNS 旁路；WFP 精确阻断防直连；端口漂移检测防漏改。
- **指定网段补充**：Windows 防火墙阻断 `160.79.104.0/23` 的直接出站连接；`scripts\apply\Anthropic网段直连阻断.ps1` 默认只读检查，管理员运行 `-Action Install` 可安装。它不覆盖 Claude 的全部地址。

## 适用条件

| 类别 | 前置条件 |
|---|---|
| 系统 | Windows 10 / 11 x64，已装 pwsh 7 |
| 代理客户端 | Clash Verge Rev（不是 Clash for Windows / ClashX），TUN 开、系统代理关、全局扩展脚本已启用 |
| 入口与组 | 三个 listener 已建好；`ACCOUNT-US` 组由全局脚本注入（不在订阅里） |
| 权限 | 日常检查不需要管理员；WFP 实盘核对要点一次 UAC，结果缓存 30 天 |

- **不适用**：Linux / macOS；其他 Clash 客户端；想从零搭建（本套件只做核对、按 SOP 变更、排障）；想要「防检测、伪装流量」（明确不做）。
- **常见误解**：Claude Code 不需要 Proxifier。`~\.claude\settings.json` 的 `env` 字段（`HTTPS_PROXY` 等指向 `43865`）就是进程级代理注入。

## 快速开始

1. **确认端口**：`43865`、`43866`、`43867` 都在监听，归属进程是 `verge-mihomo`。
2. **全量体检**（约 1 分钟）：`pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1`
3. **读结果**：`FAIL = 0` 才算程序层通过；`WARN` 逐条看提示；有 `FAIL` 先别启动 Claude，按 [排查手册](./references/02-验收与排查手册.md) 查。
4. **人工验收**（可选）：`pwsh -File scripts\Claude环境一键总检.ps1 -OpenBrowser`

- **全部命令**（快查、只跑某层、改端口清单、快捷方式还原、自包含自检、PS7 右键入口）见 [SKILL.md 入口命令](./SKILL.md)。

## 闭环由哪几层组成

按依赖顺序，上层断了下层会悄悄失效：

| 层 | 管什么 | 自动检查 |
|---|---|---|
| **L1** Clash 全局配置 | 全局扩展脚本：组 / listener / 置顶规则 / DNS 分层 | `Check-ProxyStack.ps1` |
| **L2** 代理入口与运行时 | TUN / 系统代理 / 端口监听 / 归属进程 | 同上 |
| **L3** Claude Code | `~\.claude\settings.json` 的 `env` 代理 | `Check-ClaudeCodeConfig.ps1` |
| **L4** 专用 Chrome | 账号隔离 User Data + 防旁路参数 | `Check-ChromeEntry.ps1` |
| **L5** Claude Desktop | 注册表策略 + configLibrary + 禁更同层 | `Check-DesktopConfig.ps1` |
| **L6** Windows 系统兜底 | WFP 直连阻断 / 指定 Anthropic 网段防火墙规则 / IPv6 / hosts / 快捷方式目标存在性 | `Check-WindowsFallback.ps1`、`Claude环境一键总检.ps1` |
| **L7** 出口验收 | IP 信誉 + Cloudflare trace + 无头渲染网页层 | `Check-IpReputation.ps1`、`Check-ClaudeIp.ps1` |
| 贯穿 | 端口一致性（真源 vs 字面量 vs 实际） | `Check-PortConsistency.ps1` |
| 贯穿套件 | 相对路径 / 语法 / 外部路径残留 / PS 版本守卫 | `Check-SuiteIntegrity.ps1` |

逐层合格线见 [05-闭环清单](./references/05-闭环清单.md)。

## 已知限制

- **网页验收**：`ping0.cc` / `iprisk.top` / `ip.net.coffee` 已用无头 Chrome 自动判定；`ippure.com` 仍需人眼（接口要混淆 JS 现算的签名头）。
- **ping0 限流**：连跑多次偶尔被 Cloudflare 挑战页拦下，脚本报 `WARN` 并自动重试一次，隔一会儿再跑即可。
- **Desktop 直连阻断**：从未做端到端实测；只核验规则存在、版本匹配和 30 天时效。
- **残留风险**：Node 侧独立 DNS 调用不在 WFP 精确规则覆盖内，只靠 TUN + 53 劫持兜底。
- **订阅级 rules 覆写**：本套**不依赖**它。Claude 的组、置顶规则、listener 与 DNS policy 全部由全局扩展脚本注入；换订阅后在 `ACCOUNT-US` 手选一次节点即可，订阅侧不需要补写任何覆写。
- **未验证**：`egressProxyUrl` 是否支持 `socks5://`，官方文档未写明。
- **端口字面量**：部分旧文件端口仍是硬编码（多为一次性脚本），改端口时用 `Check-PortConsistency.ps1 -Json` 出清单照单同步。

## 文档索引

| 文件 | 什么时候看 |
|---|---|
| [SKILL.md](./SKILL.md) | 命令、红线、误判坑、场景 A–G 工作流 |
| [references/00-baseline.md](./references/00-baseline.md) | 本机基线待填模板（历史数值见维护日志） |
| [references/01-架构与配置主体.md](./references/01-架构与配置主体.md) | `Script.js` 改了什么、WFP / 注册表 / configLibrary 细节 |
| [references/02-验收与排查手册.md](./references/02-验收与排查手册.md) | 出问题了，按故障决策树查 |
| [references/03-变更与回滚SOP.md](./references/03-变更与回滚SOP.md) | 要动配置（换订阅、Desktop 升级、改端口、快捷方式、回滚） |
| [references/04-Ip信誉检测.md](./references/04-Ip信誉检测.md) | IP 检测能测到哪一步、为什么有的站要人眼 |
| [references/05-闭环清单.md](./references/05-闭环清单.md) | 完整的一套包含哪些项、合格线在哪 |
