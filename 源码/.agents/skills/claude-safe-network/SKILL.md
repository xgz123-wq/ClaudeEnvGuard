---
name: claude-safe-network
description: Claude Code / Claude Desktop 安全出口链路的核对、验收、变更与排障（Clash Verge Rev + TUN + 全局扩展脚本架构），是一套自包含的一体化套件：内置 9 层自动检查编排器、IP 信誉检测、Clash 全局脚本、Claude Code / Chrome / Desktop / Windows 兜底配置、套件自包含性自检、PowerShell 7 统一入口与回滚脚本。当用户要检查 Claude 网络配置是否安全稳定、核对出口 IP 信誉（ping0.cc / ippure.com / iprisk.top / ip.net.coffee）、验证三个专用入口（43865 Claude Code / 43866 专用 Chrome / 43867 Desktop）是否走同一个住宅出口、换订阅后复核、Claude Desktop 升级后复验、改端口或改组名、校验或还原桌面快捷方式、统一用 PowerShell 7 跑脚本、或排查 Claude 连不上 / 规则失效 / DNS 泄漏 / 直连旁路时使用。触发词：Claude 安全配置、Clash 出口、ACCOUNT-US、43865、43866、43867、egressProxyUrl、直连阻断、WFP、换订阅复核、一键总检、五项自检、九层检查、IP 信誉检测、IP 检测、TUN 规则、代理配置检查、闭环清单、套件自包含性、PowerShell 7 入口、pwsh、回滚。
---

# Claude 安全出口链路 · 一体化套件

## 这是什么

这台机器上「安全、稳定、可靠地用 Claude Code + Claude Desktop」的**自包含配置知识库 + 可执行工具集**。

针对的是一套**已经建好并在运行**的架构。本 skill 的职责是**核对现状、按 SOP 变更、按清单排障**，不是从零搭一套。

**自包含原则**：`scripts/`、`config/`、`assets/` 全在本目录内，**零外部路径引用**。
把整个目录拷到别处也能跑。桌面图标实际运行的是 `docs\配置\Claude环境\`（活动源，维护走 claude-maintenance）；本目录的同名脚本路径写法不同，两边不要互相覆盖。

## 30 秒认知（先记这 6 句）

1. **配置的真正主体不是订阅，而是 Clash Verge Rev 的「全局扩展脚本」** `Script.js`。它跟订阅解耦，换订阅自动适配。
2. **三个端口就是三条腿**：`43865` 给 Claude Code（HTTP）、`43866` 给专用 Chrome（SOCKS5）、`43867` 给 Claude Desktop（HTTP）。
3. **三条腿必须汇到同一个出口 IP**（以本机实测为准）。出口不一致 = 立刻停下排查。
4. **只代理 Claude，不做全局代理**：TUN + 规则分流，系统代理必须关闭。
5. **兜底有四层**：置顶规则兜「走错组」、TUN strict-route + 53 劫持兜「DNS 旁路」、WFP 精确阻断兜「绕开代理直连」、端口一致性漂移检测兜「改端口漏改」。
6. **唯一真源是 `config\Topology.psd1`**：端口 / 组名 / 阈值 / 账号 / 快捷方式全从它取。改端口先改它。

## 架构一图

```
Claude Code ──HTTP──┐
专用 Chrome ─SOCKS5─┼──► Clash Verge Rev ──► ACCOUNT-US 代理组 ──► 美国住宅出口
Claude Desktop─HTTP─┘        (mihomo)          (手工选线路)          <出口IP>
   43865 / 43866 / 43867          │
                                  ├─ 置顶规则：Claude/Anthropic 域名 + IP-ASN 399358
                                  ├─ DNS：Claude 专属域名走 DoH over ACCOUNT-US
                                  ├─ TUN：strict-route + dns-hijack(any:53)
                                  └─ WFP：8 条精确阻断（4 个 claude.exe × V4/V6）
```

## 目录结构

```
claude-safe-network/                      ← 整个目录可拷贝，零外部引用
├── SKILL.md                               本文件（给 AI 读：触发词 / 能力 / 工作流 / 红线）
├── README.md                              给读者读：定位 / 适用条件 / 能力一览 / 快速开始 / 已知限制
├── config/
│   ├── Topology.psd1                      ★ 唯一真源：端口 / 组名 / 阈值 / 账号 / 快捷方式 / 交接 URL
│   └── *.txt *.yaml *.js                  模板类（Chrome 快捷方式 / 浏览器规范 / 规则脚本样本）
├── scripts/
│   ├── Common.ps1                         ★ 套件基石：真源加载 + 输出渲染 + 网络/子进程工具 + JSON 交接
│   ├── Invoke-ClaudeSafetyCheck.ps1       ★ 9 层编排器（一键跑全部，支持 -Fast / -Only / -Json）
│   ├── Claude环境一键总检.ps1               ★ 人工验收主入口（含 Anthropic 网段规则、检测站自动读数，网页最终验收为 MANUAL）
│   ├── checks/     Check-ProxyStack / ClaudeCodeConfig / ChromeEntry / DesktopConfig
│   │               WindowsFallback / PortConsistency / IpReputation / ClaudeIp / SuiteIntegrity
│   ├── clash/      Clash全局账号通道.js（全局扩展脚本权威副本）+ 验证 / 五项自检
│   ├── desktop/    启动Claude英文浏览器.ps1
│   │   └── 维护文件/  WFP 直连防护（.cs + 管理脚本）、DNS 修复、通道管理、自动更新管理 + 运行时状态 JSON
│   ├── apply/      安装 / 修复 / 更新类（Anthropic网段直连阻断 / Repair-Shortcuts 校验与还原 /
│   │               修复Clash全机DNS / 创建账号浏览器 / 启用-PowerShell7入口 / …）
│   └── rollback/   回滚Clash配置.ps1
├── assets/
│   ├── ico/Claude官方图标.ico
│   └── shortcuts/*.lnk                    5 个快捷方式原版备份（= 原始基线，指回 docs\配置\；**还原源，勿改**）
└── references/
    ├── 00-baseline.md                     本机基线待填模板（历史数值不入档）
    ├── 01-架构与配置主体.md                Script.js 六步逻辑、注册表 / WFP / configLibrary 细节
    ├── 02-验收与排查手册.md                验证工具、故障决策树、环境与编码坑
    ├── 03-变更与回滚SOP.md                 换订阅 / Desktop 升级 / 改端口 / 桌面快捷方式校验与还原 / 回滚
    ├── 04-Ip信誉检测.md                    ★ IP 信誉检测能力（五个入口的可测性矩阵与手法）
    └── 05-闭环清单.md                      ★ 闭环逐层对照（L1–L7 + 端口层：检查项 / 脚本 / 合格线）
```

## 入口命令（全部用 **pwsh 7**，不是 `powershell.exe`）

```powershell
# ① 一键全检（9 层，约 1 分钟，含联网；耗时随检测站响应波动）
pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1

# ② 快查（跳过联网层 proxy / chrome / ip / claudeip，只剩本机配置层，约 10～16s；不弹 UAC，提权缓存不匹配只报 WARN）
pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1 -Fast

# ③ 只跑某几层
pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1 -Only proxy,code,desktop

# ④ 机器可读（输出里带 ===JSON-BEGIN=== / ===JSON-END=== 标记块）
pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1 -Fast -Json

# ⑤ 人工验收主入口（含网段阻断检查；-OpenBrowser 才打开交接网页）
pwsh -File scripts\Claude环境一键总检.ps1 -OpenBrowser

# ⑥ 改端口后：拿「文件:行号」字面量清单，照单同步
pwsh -File scripts\checks\Check-PortConsistency.ps1 -Json

# ⑦ 桌面快捷方式：核对 / 还原（默认只报告；桌面是用户资产，本套件不接管）
pwsh -File scripts\apply\Repair-Shortcuts.ps1
pwsh -File scripts\apply\Repair-Shortcuts.ps1 -Apply

# ⑧ 套件自包含性自检（改完脚本 / 搬家后必跑）
pwsh -File scripts\checks\Check-SuiteIntegrity.ps1

# ⑨ 换订阅后只跑这两条（其余配置一行不动）
pwsh -File scripts\checks\Check-IpReputation.ps1
pwsh -File scripts\checks\Check-ClaudeIp.ps1

# ⑩ Anthropic 官方 API/Console IPv4 网段：默认只读检查；安装需管理员
pwsh -File scripts\apply\Anthropic网段直连阻断.ps1
pwsh -File scripts\apply\Anthropic网段直连阻断.ps1 -Action Install

# ⑪ .ps1 右键入口：加 / 撤「用 PowerShell 7 运行」（默认只报告；只写 HKCU，不需要管理员）
pwsh -File scripts\apply\启用-PowerShell7入口.ps1
pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Apply
pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Rollback
```

**层名**：`proxy` / `code` / `chrome` / `desktop` / `windows` / `ports` / `ip` / `claudeip` / `suite`

**健康判定**：现行判定以脚本实时输出为准：`0 FAIL`、退出码 `0` 即通过，`WARN` 逐条确认。
历史参考（2026-09-13 实测，后来检查项有增加，不作为现行验收线）：全量 `114 PASS / 2 WARN / 0 FAIL / 0 MANUAL`，`-Fast` `61 PASS / 0 WARN / 0 FAIL / 0 MANUAL`。

2 条 `WARN` 都是良性/可自愈：iprisk「IP 使用时间较短」（新 IP 必然扣分）、
`ping0.cc` 被 Cloudflare Turnstile 挑战页拦下（**站点按出口 IP 临时限流**，与本机配置无关；隔一会儿再跑即恢复）。
WFP 实盘核对已降级为 `pwsh -File scripts\checks\Request-ElevatedVerify.ps1 -Refresh` + 点一次 UAC，结果缓存 **30 天**（`-Fast` 永不自动弹 UAC），
过期或版本不匹配会降级成 `WARN`（**绝不静默放行**）。四个检测站里 `ping0.cc` / `iprisk.top` / `ip.net.coffee`
已用无头 Chrome 真实渲染自动读数（总检里叫「检测站自动读数」），只看出口 IP；专用 Chrome / Desktop 的网页最终验收仍由用户人眼确认，`ippure.com` 也只能人眼看。

## 能力一：配置核对（闭环清单）

见 [references/05-闭环清单.md](./references/05-闭环清单.md)。L1–L7 是**依赖顺序**（另有两层「贯穿」检查）：

| 层 | 管什么 | 自动检查 |
|---|---|---|
| L1 Clash 全局配置 | 全局扩展脚本：组 / listener / 置顶规则 / DNS 分层 | `Check-ProxyStack.ps1` |
| L2 代理入口与运行时 | TUN / 系统代理 / 端口监听 / 归属进程 | 同上 |
| L3 Claude Code | `~\.claude\settings.json` 的 `env` 代理 | `Check-ClaudeCodeConfig.ps1` |
| L4 专用 Chrome | 账号隔离 User Data + 防旁路参数 | `Check-ChromeEntry.ps1` |
| L5 Claude Desktop | 注册表策略 + configLibrary + 禁更同层 | `Check-DesktopConfig.ps1` |
| L6 Windows 系统兜底 | WFP 直连阻断 / IPv6 / hosts / 桌面快捷方式目标存在性 | `Check-WindowsFallback.ps1` |
| L7 出口验收 | IP 信誉 + Cloudflare trace + 四个检测站（3 个已无头渲染自动化，仅 `ippure.com` 需人眼） | `Check-IpReputation.ps1`＋`Check-ClaudeIp.ps1` |
| 贯穿：端口一致性 | 真源 vs 字面量 vs 实际监听 | `Check-PortConsistency.ps1` |
| 贯穿：套件自包含性 | 相对路径 / 语法 / 外部路径残留 / PS 版本守卫（含 `#requires` 覆盖 + PS7 右键入口**实盘枚举**） | `Check-SuiteIntegrity.ps1` |
| 按需：提权复核 | 内核 WFP 实盘（一次 UAC + 30 天缓存） | `Request-ElevatedVerify.ps1` |

## 能力二：IP 信誉检测

见 [references/04-Ip信誉检测.md](./references/04-Ip信誉检测.md)。核心结论：

| 入口 | 能自动测吗 | 手法 |
|---|---|---|
| `cloudflare.com/cdn-cgi/trace` | ✅ **主力** | 逐入口 curl，比 `ip` / `loc` / `colo` / `warp` 四入口是否一致 |
| `iprisk.top` | ✅ | `/badge/<IP>` 拿 SVG 分数；`/ip/<IP>` 拿服务端渲染的全部字段 |
| `ping0.cc` | ✅ **无头 Chrome 真实渲染** | `--dump-dom` 后取风控值 / IP 类型 / 原生 IP / 共享人数 / ASN；偶发被 Turnstile 挑战页拦下时会识别出来并重试一次 |
| `ip.net.coffee/claude/` | ✅ **无头 Chrome 真实渲染** | SPA，必须 `--virtual-time-budget=8000` 等 JS 跑完；取专向出口 / Trust Score / IP 属性 / 风险信号 / 滥用记录 / 支持地区 |
| `ippure.com` | ❌ | 接口需混淆 JS 生成的签名头 `x-t` → 浏览器人工（**唯一必看项**） |

**换订阅 / 换节点之后，配置一行都不改，只需要重跑 `Check-IpReputation.ps1` + `Check-ClaudeIp.ps1`，再看一眼 `ippure.com`。**
原理：`Script.js` 的 `ACCOUNT-US` 组声明了 `include-all-proxies: true`，自动吃下新订阅全部节点。

无头渲染的三条硬性要求（缺一不可）：**真实 UA**（否则 ping0.cc 回 41 字节空页）、
**独立 `--user-data-dir`**（否则 Chrome 把 URL 转交给现有实例后立刻退出）、
**`--virtual-time-budget`**（否则 SPA 只拿到半成品页）。详见 [references/04-Ip信誉检测.md](./references/04-Ip信誉检测.md)。

## 硬红线（永远不做）

- ❌ 不把订阅 URL token、节点 uuid、REALITY public-key 写进任何回复 / 文档 / 提交。
- ❌ 不读、不打印 `~/.claude/.credentials.json`。
- ❌ 不用本目录的副本覆盖 `docs\配置\Claude环境\` 里的脚本与文档（那是活动源，除非用户明确要求）。
- ❌ 不承诺「规避检测 / 伪装流量」这类目标；只做「出口干净 + 不泄漏 + 不旁路」。
- ❌ 不因为一份不完整的枚举就否定用户文档里「已验证通过」的结论 —— **先怀疑自己的取证方法**（见下）。

## 四大误判坑（已踩过，务必避开）

| 坑 | 错误做法 | 正确做法 |
|---|---|---|
| **注册表** | 猜路径去查 `HKLM\SOFTWARE\Policies\Anthropic\Claude` | 真实路径是 `HKLM\SOFTWARE\Policies\Claude`（**没有** `Anthropic` 层） |
| **防火墙** | 把两个直连阻断机制混为一谈 | Claude 程序的精确 WFP 过滤器不在 `Get-NetFirewallRule` 里；`160.79.104.0/23` 的系统出站规则在防火墙规则表里，用 `scripts\apply\Anthropic网段直连阻断.ps1` 核对。网段规则不覆盖 Claude 全部地址。 |
| **运行脚本** | 用 Windows PowerShell 5.1 跑 | 必须用 **pwsh 7**：脚本是无 BOM UTF-8，5.1 会按 GBK 读 → 整片解析失败 |
| **PowerShell 版本** | 机器上同时有 `powershell.exe`(5.1) 和 `pwsh.exe`(7)，以为「冲突了，卸掉一个」 | **不是冲突，是正常共存**：5.1 是 Windows 系统组件（**不可卸载**），7 是独立安装。正确解法 = **脚本一律显式用 pwsh 7 全路径调用**，跨版本调用时写 `#requires -Version 7.0` |

## 端口集中化的真实取舍（如实说明，不假装全自动）

| 做完了 | 说明 |
|---|---|
| ✅ 新代码零硬编码 | 编排器、9 个 checks、`Repair-Shortcuts.ps1` 全部从 `config\Topology.psd1` 取端口 / 组名 / 阈值 |
| ✅ 主入口已改为取真源 | `Claude环境一键总检.ps1` 的 `$portMixed/$portChrome/$portDesktop` 来自真源 |
| ✅ 漂移检测 | `Check-PortConsistency.ps1` 扫同族端口号（`438xx`/`789x`/`909x`/`1080x`），**凡不在真源里的报 WARN** |
| ⚠️ **保留** | **legacy 端口字面量**（多为一次性已执行完的安装/回滚脚本）**未改动**；文件和行数以 `Check-PortConsistency.ps1 -Json` 实时输出为准 |

**为什么不全改**：这些字面量散落在多个旧文件里，改动风险大于收益。折中方案 = **真源 + 清单 + 告警**：
真源只有一处，`Check-PortConsistency.ps1` 能直接输出「文件:行号」清单，改端口时照单同步即可（见 SOP-3）。

## 标准工作流

### 场景 A：例行健康检查（用户问「现在安全吗 / 还好吗」）

```powershell
pwsh -File scripts\Invoke-ClaudeSafetyCheck.ps1
```

现行判定以脚本实时输出为准：`0 FAIL`、退出码 `0` 即通过，`WARN` 逐条确认。
有 FAIL → 按 [02-验收与排查手册.md](./references/02-验收与排查手册.md) 的决策树走；有 WARN → 逐条确认（常见良性 WARN 见上方）。
要人工验收再加跑 `Claude环境一键总检.ps1 -OpenBrowser`。

### 场景 B：导入新订阅后复核

**脚本与规则一行都不用改**，只做 3 件事：① 在 `ACCOUNT-US` 组里手动挑节点（自动填充**不会自动选中**）；
② 跑 IP 检测两个脚本 + 三站人眼确认；③ 跑一键全检确认 0 FAIL。
详见 [03-变更与回滚SOP.md](./references/03-变更与回滚SOP.md)。

### 场景 C：Claude Desktop 更新

- **入口**：用户说“读取 Docs 配置总览，更新 Claude Desktop”时，按 [claude-maintenance](../claude-maintenance/SKILL.md) 执行，Desktop 细则见 `docs/配置/Claude环境/Desktop防护/更新维护说明.md`。
- **执行**：先用 Docs 真源中的 `下载ClaudeDesktop官方包.ps1` 下载并审核，再用 `一键更新ClaudeDesktop.ps1` 更新；CLI 指纹刷新也通过它的 `-CliApprovedSHA256` 完成，管理员工作集中在一次授权中。
- **启动**：默认不打开 Desktop、Code、浏览器或探针。
- **顺序**：先保护新版路径，再注册新版，最后检查并同步记录。
- **限制**：保持当前路线；失败后保留现场，不反复提权试验。
### 场景 D：改端口 / 改组名

不要直接改。先改 `config\Topology.psd1`，再跑 `Check-PortConsistency.ps1 -Json` 拿清单逐行同步，最后跑全检。详见 SOP-3。

### 场景 E：桌面快捷方式失效 / skill 被移动

桌面快捷方式是**用户资产**，本套件**不接管、不改写**它们的 `-File` 目标；`Repair-Shortcuts.ps1` 只做一件事：
把桌面 **还原**成原始基线（指回 `docs\配置\`），并从 `assets\shortcuts\` 的 5 个原版备份逐字节复核。

```powershell
pwsh -File scripts\apply\Repair-Shortcuts.ps1           # 预览：报告 same / drift / missing
pwsh -File scripts\apply\Repair-Shortcuts.ps1 -Apply    # 写入：从原版备份覆盖回桌面并复核哈希
```

因此 **skill 目录被移动或删除，都不会弄坏桌面图标**（图标指回的是 `docs\配置\` 原路径）。
位置无关的可用性校验由第 3 / 5 / 9 层负责。详见 SOP-3B。

### 场景 F：Claude 连不上 / 疑似泄漏

**国内网站正常、走代理的全挂**时，先跑 `ping -f -l 1472 8.8.8.8`：超时 = 宽带链路限包（MTU 黑洞），直接按决策树「第 -1 步」处理，**不换订阅、不换节点**。

其余按故障决策树走：先看端口监听 → 再看组选中 → 再看规则 → 再看 DNS。详见 [02-验收与排查手册.md](./references/02-验收与排查手册.md)。

### 场景 G：手工跑脚本时想确保用的是 PowerShell 7

**先纠正一个常见误解**：机器上 `powershell.exe`(5.1) 与 `pwsh.exe`(7) 是**两个不同名字、不同目录**的命令，**不存在冲突，也不需要"统一"**。5.1 是 Windows 系统组件，不可卸载。

本套件做的三层保障：

1. **全部 `.ps1` 首行都有 `#requires -Version 7.0`** → 被 5.1 跑到会当场明确拒绝，而不是按 GBK 解析中文后抛乱码错。
2. **第 9 层会静态扫描**：任何脚本里若残留 `powershell.exe` 调用 → `FAIL`；`#requires` 覆盖不全 → `WARN`。
3. **可选加固：`.ps1` 右键「用 PowerShell 7 运行」**（只写 `HKCU`，不改系统关联、不需要管理员）：

```powershell
pwsh -File scripts\apply\启用-PowerShell7入口.ps1            # 报告
pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Apply     # 写入
pwsh -File scripts\apply\启用-PowerShell7入口.ps1 -Rollback  # 撤销
```

**不要做**：卸载 5.1；把 `powershell.exe` 别名成 pwsh 7；改 `.ps1` 双击默认关联（`UserChoice` 有哈希保护，且 Win11 默认「记事本打开」是故意的安全设计）。详见 SOP-3C。

## 参考文件

| 文件 | 内容 |
|---|---|
| [README.md](./README.md) | **给读者看的入口**：定位 / 适用条件 / 快速开始 / 闭环层次 / 已知限制（人先看这份） |
| [references/00-baseline.md](./references/00-baseline.md) | 本机基线待填模板（历史数值见维护日志） |
| [references/01-架构与配置主体.md](./references/01-架构与配置主体.md) | `Script.js` 六步逻辑、`profiles.yaml` 结构、注册表 / WFP / configLibrary 细节 |
| [references/02-验收与排查手册.md](./references/02-验收与排查手册.md) | 四大 IP 信誉站、四套验证工具、故障决策树、环境与编码坑 |
| [references/03-变更与回滚SOP.md](./references/03-变更与回滚SOP.md) | 换订阅 / Desktop 升级 / 改端口或组名 / 校验与还原桌面快捷方式 / 回滚 |
| [references/04-Ip信誉检测.md](./references/04-Ip信誉检测.md) | IP 信誉检测：五入口可测性矩阵、iprisk 解析法、人工交接、合格线速查 |
| [references/05-闭环清单.md](./references/05-闭环清单.md) | 闭环逐层对照（L1–L7 + 端口层）+ 三入口对应表 |

