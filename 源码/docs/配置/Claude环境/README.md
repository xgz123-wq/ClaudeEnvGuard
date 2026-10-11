# Claude 配置总览

> 更新日期：2026-10-06
> `docs/配置/Claude环境/` 是 Claude/Clash 配置的唯一位置；桌面快捷方式和总检按「脚本目录往上 3 层 = 项目根」定位，不要改目录名或层级。

## 目录

| 位置 | 内容 |
|---|---|
| `Claude环境一键总检.ps1` | 桌面「Claude-环境一键总检」，只读检查 |
| `Anthropic网段直连阻断.ps1` | 检查或安装 `160.79.104.0/23` 出站阻断规则；默认只读 |
| `启动Claude英文浏览器.ps1`、`Claude官方图标.ico` | 桌面「Claude-英文浏览器」，检查通过才启动 Desktop |
| `Clash/` | Clash 全局脚本副本（与现场 `Script.js` 一致）、通道验证脚本、桌面「Claude-五项自检」 |
| `Desktop防护/` | Desktop/Code 直连防护、官方包下载与受控更新、三端更新编排（`更新三端Claude.ps1`）、43867 通道管理、自动更新开关，以及运行状态 json |
| `说明/` | [日常使用](说明/日常使用.md)、[Anthropic 网段直连阻断](说明/Anthropic网段直连阻断.md)、[账号浏览器](说明/账号浏览器.md)、[DNS 分层](说明/DNS分层说明.md)、[架构讲解](说明/架构讲解.html)（图解，双击打开） |
| `维护日志.md` | 最近一轮维护的结论与证据，更早的压成历史表 |
| `清理维护残留.ps1` | 列出并清理过期备份与遗留安装包（默认只列清单，加 `-Yes` 才删） |

- **一次性工具**：安装、回滚、修复 DNS、创建账号浏览器等只在 Skill [claude-safe-network](../../../.agents/skills/claude-safe-network/SKILL.md) 的 `scripts/` 里，本目录不放副本。

## 当前状态（原机器 2026-09-25 参考值，新机器以实测为准）

| 项目 | 现状 |
|---|---|
| Claude Desktop | `2.9939.2.0`，自动更新关闭 |
| Claude Code CLI | `2.1.282`，指纹已同步 |
| Claude Code VS Code 插件 | 安装后固定版本，并把当前插件内置 `claude.exe` 路径纳入防护 |
| 直连防护 | 4 个程序、8 条 WFP 规则 |
| 验证 | 一键总检 13 PASS / 1 MANUAL（2026-09-25） |

| 入口 | 路线 |
|---|---|
| Claude Code CLI / VS Code 插件 | HTTP `43865`，Claude 域名按规则进 `ACCOUNT-US` |
| 专用 Chrome | SOCKS `43866 → ACCOUNT-US` |
| Claude Desktop | HTTP `43867 → ACCOUNT-US` |

## 按任务阅读

| 要做什么 | 入口 |
|---|---|
| 理解整体架构、出问题定位到哪一层 | [架构讲解](说明/架构讲解.html) |
| 每天启动与检查 | [日常使用](说明/日常使用.md) |
| 每周或更新后复核 Claude 域名 | 运行 `Clash/检查Claude官方域名.ps1`；分工与新域名处理见 [DNS 分层与域名维护](说明/DNS分层说明.md#claude-域名定期复核) |
| 安装或检查 Anthropic 网段直连阻断 | [Anthropic 网段直连阻断](说明/Anthropic网段直连阻断.md)；首次部署后总检会验证 |
| 更新 Desktop / CLI / 插件，或手动更新后补同步 | 跑 `Desktop防护/更新三端Claude.ps1 -Action Plan`，按输出的 `Command` 执行；通用流程见 [claude-maintenance](../../../.agents/skills/claude-maintenance/SKILL.md) |
| Desktop 更新细则 | [更新维护说明](Desktop防护/更新维护说明.md) |
| 网络排障、改端口；换订阅后的复核流程 | Skill [claude-safe-network](../../../.agents/skills/claude-safe-network/SKILL.md) |
| 历次维护与未验收项 | [维护日志](维护日志.md) |

## 约定

- **维护默认**：不启动 Desktop、Code 或浏览器；不改 Clash 生效配置、`ACCOUNT-US` 手选节点、DNS、HKLM 策略。
- **验收边界**：程序层检查通过不等于真实网页、登录和外部工具都已验证；旧截图、旧版号、旧 IP 不能替代现场检查。
