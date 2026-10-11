---
name: claude-maintenance
description: 在本项目维护 Claude Desktop App、Claude Code CLI 和 VS Code Claude Code 插件。用于更新前检查、受控更新、手动更新后的防护同步、残留清理及维护文档收尾。复用本项目 Docs 脚本；普通网络排障使用 claude-safe-network。
---

# Claude 更新与防护同步

本文件是维护 SOP 的唯一正文；由本文件向上三级定位项目根目录，根目录缺少 `docs/配置/Claude环境/` 就停止。路径均相对项目根，脚本一律用 PowerShell 7。Docs 是活动源，Skill 里的脚本副本只做同步。

## 快速流程（默认走这条）

只跑两条命令就能完成三端更新，不要读脚本源码，也不要为找参数去通读别的文档：

1. `docs/配置/Claude环境/Desktop防护/更新三端Claude.ps1 -Action Plan`
   一次输出三端版本对比、四个受保护目标的指纹，以及唯一需要执行的那条命令（结果里的 `Command` 字段）；有 claude 进程在跑就报 `blocked_running`，先请用户退出再重跑。
2. 用 `Command` 字段的值执行（PowerShell 7，全程只弹一次 UAC）。成功看 `Status=passed`，明细在 `Maintenance` 给出的 `ResultPath`。

用户已明确要求更新、现场也没有 claude 进程时，可直接改用 `-Run`，省掉第 1 步的往返。

**不要做的三件事**：不翻 `更新ClaudeDesktop.ps1`、`应用直连防护管理.ps1` 等源码找参数（脚本输出已含全部字段）；不为“留一份干净通过记录”重跑一次 UAC（首次结果如实写进日志即可）；不重复确认用户已经说过的更新意图。

下面的章节是这套流程背后的依据与例外，只在快速流程报错、或要做非更新类维护时才需要读。

## 识别用户要做什么

| 用户意图 | 动作 |
|---|---|
| 需不需要更新、看看能否更新 | 只查不下：跑 `Desktop防护/查询Claude最新版本.ps1`，向官网查三个产品最新版并与本机比较，报告谁落后；不下载、不跑下载脚本 |
| 检查防护状态 | `一键更新ClaudeDesktop.ps1 -Action Verify`（需要 UAC，先告知用户） |
| 更新 Desktop / CLI | 按「快速流程」执行 |
| 更新 VS Code 插件 | 用户在 VS Code 里装好新版（保持固定版本）后走「快速流程」，脚本会自动带上 `-PluginApprovedSHA256` 迁移防护；不顺带更新 npm CLI |
| 已手动更新，请同步 | 跳过安装，从「快速流程」第 1 步开始 |

“Claude Code Desktop”等含混称呼不等于三个产品全更新；无法确定对象时只问这一项。用户已明确要求更新时不重复确认。UAC 是系统授权，不能承诺零交互。

## 分工：脚本做确定性步骤，Agent 只做判断

| 由脚本完成（不要手工重做、不要重新搜索） | 由 Agent 判断 |
|---|---|
| 只查三个产品是否落后：`查询Claude最新版本.ps1`；下载与签名、Manifest 审核：`下载ClaudeDesktop官方包.ps1`（`-CheckOnly` 只查 Desktop） | 现场是否适合更新：Claude 进程、策略、上次失败的遗留 |
| 编排上述两步并汇总四个目标的指纹与现成命令：`更新三端Claude.ps1` | 审阅 `Plan` 输出里的指纹与签名，决定是否执行 |
| 规则迁移、CLI 指纹刷新、Verify、总检、Skill 副本同步、清理安装包：`一键更新ClaudeDesktop.ps1` | 审核 CLI 新文件的指纹和签名，决定是否批准 |
| 过期备份与遗留安装包的清单：`清理维护残留.ps1` | 把清理清单列给用户，得到确认后再删除 |
| 程序与代理检查：`Claude环境一键总检.ps1`、启动器 `-CheckOnly` | 脚本报错时读日志定位原因，按维护说明恢复，不重跑碰运气 |
| | 清单目标数量变化等脚本不支持的情况：先给方案，不假称已有自动化 |
| | 收尾写文档：维护日志一节、README 一行 |

## 固定来源：不要重新搜索

2026-09-25 实测确认；只有脚本报错或结果对不上时才重新查。

| 项目 | 固定值 |
|---|---|
| Desktop 官方入口 | `https://claude.ai/api/desktop/win32/<x64\|arm64>/msix/latest/redirect`，经 `http://127.0.0.1:43867`；HEAD 返回 405，只能用 GET 读 307 跳转地址 |
| Desktop 实际下载地址 | `https://downloads.claude.ai/releases/win32/<架构>/<三段版本>/Claude-<提交哈希>.msix`；地址里的版本只用来预判，以包内 Manifest 为准 |
| Desktop 依据 | [Anthropic Windows 部署说明](https://support.claude.com/en/articles/12622703-deploy-claude-desktop-for-windows)；`claude.com/download` 的 Windows 按钮走同一接口的 `win32/<架构>/setup/…`（exe 安装器），本机装的是 MSIX 包，所以用 `msix`，不从网页抓链接 |
| 路径里的 `win32` | 是 Windows 平台名（沿用 Node.js 的叫法），不代表 32 位；位数看后面的架构段 `x64` / `arm64` |
| CLI 包名与位置 | `@anthropic-ai/claude-code`，npm 全局安装，前缀 `%APPDATA%\npm` |
| CLI 查最新版 | `npm view @anthropic-ai/claude-code version --registry https://registry.npmjs.org` |
| 插件查最新版 | VS Code 官方市场 `https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery`，按 `anthropic.claude-code` 查，只取 `win32-x64`；本机版本以 `~/.vscode/extensions/extensions.json` 当前登记为准 |
| CLI 安装 | `npm install -g @anthropic-ai/claude-code@<版本> --registry https://registry.npmjs.org`；本机默认源若是国内镜像（如 `registry.npmmirror.com`）也不用；不用 `npm update -g` |
| 受保护的四个程序 | 以 `Desktop防护/应用过滤目标.json` 的 `Targets` 顺序为准：Desktop `claude.exe`、CLI `bin\claude.exe`、CLI 原生包 `claude-code-win32-x64\claude.exe`、VS Code 插件 `resources\native-binary\claude.exe` |
| 证据与备份 | `%LOCALAPPDATA%\ClaudeNetworkGuard\`；Codex 桌面版（MSIX 打包应用）写的内容实际在 `%LOCALAPPDATA%\Packages\OpenAI.Codex_<发布者哈希>\LocalCache\Local\ClaudeNetworkGuard\` |
| 安装包缓存 | `%ProgramData%\ClaudeNetworkGuard\`、`…\ClaudeNetworkGuard\Desktop安装包下载\`；更新成功后自动清空 |

## 更新流程

1. **确认现场**：读版本（从包元数据读，不启动 Claude）、Claude 进程、HKLM `disableAutoUpdates=1` 与同层 `egressProxyUrl`、CLI 的 `DISABLE_AUTOUPDATER=1`。缺失或冲突先报告，不顺带改用户设置；不输出认证文件或密钥。发现 Claude 正在运行就停止安装，不强杀，也不在 Claude Code 会话里更新自身，交给退出后的 Codex 或独立维护进程执行。
2. **准备**：先跑 `查询Claude最新版本.ps1` 确认谁落后；Desktop 落后且用户要更新时，普通权限运行 `下载ClaudeDesktop官方包.ps1` 下载并审核；需要更新 CLI 时，按固定来源表查版本并安装指定版本。
3. **审核**：按 `Targets` 顺序读取四个程序的 SHA256 和 Anthropic 签名，逐个核对；不能直接从磁盘取指纹就批准。
4. **执行（只弹一次 UAC）**：`一键更新ClaudeDesktop.ps1 -Action Update`，传 `-PackagePath` 和 `-ApprovedSHA256`（Desktop 有新版时），`-PluginApprovedSHA256 '<h>'`（插件换了版本目录时；指纹和签名由 `查询Claude最新版本.ps1` 的插件行给出，审核后填入），以及 `-CliApprovedSHA256 '<h1>','<h2>','<h3>','<h4>'`（CLI 或插件同路径文件变化时；插件已迁移时第 4 个填新插件指纹）。工作进程依次完成插件迁移、指纹刷新、Desktop 迁移、Verify、总检、启动条件检查、Skill 副本同步、安装包清理，结果写入 `统一维护-*\结果.json`。不要再单独以管理员身份跑 `应用直连防护管理.ps1 -Action Refresh`。
5. **失败时**：停下后续步骤，保留防护、日志和安装包，按 `Desktop防护/更新维护说明.md` 区分注册前、注册后，用 `-ResumeJournal` 续接。没有旧安装包就不承诺降级。

插件只经 VS Code 官方入口手动更新 `anthropic.claude-code`；当前内置 `claude.exe` 已纳入 WFP。总检要求活动插件唯一、固定版本、平台和路径与清单一致且未配置 wrapper。插件版本目录变化时，`查询Claude最新版本.ps1` 的插件行会显示 `ProtectedPathMatches=false` 并给出新程序指纹和签名；审核后经 `-PluginApprovedSHA256` 迁移：先给新路径加规则、改清单和状态，最后撤旧规则，不跑联网探针。新路径以 VS Code `extensions.json` 当前登记为准，不猜测。旧版本目录由 VS Code 自己清理。

## 收尾：清理与文档

- **清理**：运行 `docs/配置/Claude环境/清理维护残留.ps1`（默认只列清单）。清单为空就结束；不为空就逐条列给用户，确认后加 `-Yes` 删除。规则固定：名称带日期、距今 7 天及以上的备份和日志目录删除；`受控更新-*`、`统一维护-*`、`基线刷新备份-*` 在每个位置各保留最新一份；探针程序不动。
- **维护日志**：`维护日志.md` 只保留最近一轮维护的完整一节；开始新一轮时，先把上一节压成「历史」表里的一行。同一轮的补充就地改那一节，不追加新节。
- **README**：只更新当前版本、验证结果和指向维护日志的链接。
- **说明文档**：`更新维护说明.md` 只在流程或边界变化时改；本 Skill 只在固定来源或分工变化时改。同一事实只写在一处，别处链接过去。
- **交接**：只在任务未完成、要换会话时写 `docs/handoff/`；任务完成、内容并入维护日志后，经用户确认删除该交接。

## 完成标准

回复说明更新对象、前后版本、Verify 结果、清理结果和未验证项，并区分三件事：配置文件检查、管理员实际规则核验、真实应用验收。维护成功至少要求：签名审核通过、管理员 Verify 通过（4/4 文件、8/8 规则）、活动 VS Code 插件路径与清单一致、总检 0 FAIL、规定的 6 个同步文件哈希一致、维护日志已更新。任一项未满足，不能写“全部完成”。

每次 Desktop、CLI 或插件更新后，按 `docs/配置/Claude环境/说明/DNS分层说明.md` 的「Claude 域名定期复核」比对官方网络要求与实际路由：先运行 `docs/配置/Claude环境/Clash/检查Claude官方域名.ps1`，退出码 `0` 无需处理，`1` 表示有未归类新域名（由模型判断专属或共享并报告，更新由用户手动做），`2` 表示抓取、解析或本地清单出问题（报告给用户）；域名变化不直接从示例或旧清单照抄到 Clash。

不改无关代理、DNS、手选节点、认证信息或自动更新策略，不注册后台任务。
