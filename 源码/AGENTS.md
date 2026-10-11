# Claude 环境：部署与维护约定

> 给 Claude Code / Codex 等读取 AGENTS.md 的工具使用。
> 本目录是分享版：原机器的账号别名、出口 IP、状态文件已去掉；README 里标「参考值」的版本号以本机实测为准。

## 入口

| 要做什么 | 入口 |
|---|---|
| 了解整体、给用户解释 | `README.md`；图解给用户看 `docs/配置/Claude环境/说明/架构讲解.html` |
| 目录、代理路线 | `docs/配置/Claude环境/README.md` |
| 安装、检查、排障、回滚工具 | `.agents/skills/claude-safe-network/SKILL.md` |
| 部署后的 Desktop / CLI 更新 | `.agents/skills/claude-maintenance/SKILL.md` |

## 首次部署（新机器）

按顺序做，每步先向用户说明要改什么，确认后再执行；每个脚本先读文件头部说明，有只读 / 预览模式的先跑只读模式。

| 步骤 | 做什么 | 依据 |
|---|---|---|
| 0 前置 | 确认 Windows 10/11、PowerShell 7、Clash Verge Rev、Chrome、Claude Code、Claude Desktop 已安装；用户已导入订阅 | Skill README「适用条件」 |
| 1 Clash | 把 `docs/配置/Claude环境/Clash/Clash全局账号通道.js` 装为 Verge 全局扩展脚本；开 TUN、关系统代理；请用户在 `ACCOUNT-US` 手选节点 | Skill `scripts/apply/安装Clash账号专用通道.ps1`、`references/01-架构与配置主体.md` |
| 2 IPv6 与 DNS | 网卡、Clash 全局、Clash DNS 覆写三层关闭 IPv6 | `docs/配置/Claude环境/说明/DNS分层说明.md` |
| 3 Claude Code | `~/.claude/settings.json` 的 `env` 设 `HTTPS_PROXY` / `HTTP_PROXY` / `ALL_PROXY` 为 `http://127.0.0.1:43865`，`NO_PROXY` 为 `localhost,127.0.0.1,*.local`；只改 `env`，不动其他字段 | Skill `SKILL.md` |
| 4 专用 Chrome | 每个账号建独立浏览器：`创建账号浏览器.ps1 -Alias 账号1`（别名由用户定） | Skill `scripts/apply/创建账号浏览器.ps1`、`docs/配置/Claude环境/说明/账号浏览器.md` |
| 5 Desktop 通道 | `Desktop防护/专用通道管理.ps1 -Action Install`（43867 与 HKLM `egressProxyUrl`）；`Claude自动更新管理.ps1 -Action Disable` | `docs/配置/Claude环境/Desktop防护/更新维护说明.md` |
| 6 直连防护 | 按 `应用过滤目标.示例.json` 生成本机 `应用过滤目标.json`：填本机真实路径，SHA256 审核后填写，GUID 新生成；再 `应用直连防护管理.ps1 -Action Install`（管理员） | 同上 |
| 6a 网段阻断 | 以管理员运行 `pwsh -File docs/配置/Claude环境/Anthropic网段直连阻断.ps1 -Action Install`，阻断 `160.79.104.0/23` 的直接出站；再用默认只读模式核对 | `docs/配置/Claude环境/说明/Anthropic网段直连阻断.md` |
| 7 快捷方式 | 桌面建「Claude-英文浏览器」「Claude-五项自检」「Claude-环境一键总检」及各账号 Chrome 快捷方式；复制一份到 Skill `assets/shortcuts/` | Skill `scripts/apply/升级现有Claude快捷方式.ps1`、`references/03-变更与回滚SOP.md` |
| 8 验收 | 一键总检 0 FAIL；Skill 全量体检；用户肉眼核对检测站；把结果写进 `维护日志.md` 和 Skill `references/00-baseline.md` | `docs/配置/Claude环境/Claude环境一键总检.ps1` |

- **分享版的已知问题**：步骤 7 之前，`Check-SuiteIntegrity` 的快捷方式备份项报 WARN；步骤 6 之前，总检的直连防护项报 FAIL；步骤 6a 之前，网段阻断项报 FAIL。都属预期，做完对应步骤后消失。

## 维护工作流程（部署后）

1. **先读现状**：`docs/配置/Claude环境/README.md` → `维护日志.md` 末尾 → 对应 Skill。
2. **改前备份**：改动文件复制到 `%LOCALAPPDATA%\ClaudeNetworkGuard\<中文任务名>-<yyyyMMdd-HHmmss>\`。
3. **改后验证**：一键总检 0 FAIL；动了 Skill 再跑 `Check-SuiteIntegrity.ps1`。
4. **记录**：`维护日志.md` 只保留最近一轮的完整一节，上一轮压成「历史」表一行，不新建记录文件。
5. **清理**：收尾跑 `docs/配置/Claude环境/清理维护残留.ps1`（默认只列清单），列给用户确认后加 `-Yes` 删除。

## 约束

- **先查网关**：`~/.claude/settings.json` 的 `env` 若有 `ANTHROPIC_BASE_URL` 指向第三方网关，Claude 域名规则对 Claude Code 不生效，先向用户说明再继续。
- **订阅链接是高危信息**：部分订阅 URL 参数是 base64 编码的节点密钥；排查中不要打印，若已打印提醒用户不要外发终端记录。
- **不代替用户**：不选节点、不登录账号、不读或输出订阅链接、节点密钥、Cookie、token。
- **默认不启动**：Claude Desktop、Claude Code、浏览器；`Clash/Claude日常五项自检.ps1` 会打开账号 1 专用 Chrome，先告知。
- **目录层级不能变**：总检按「脚本目录往上 3 层 = 项目根」定位。
- **docs 是活动源**：桌面图标运行 `docs/配置/Claude环境/`；Skill 是自包含副本，同名脚本两边路径写法不同，只能逐行改。一键更新只要求 6 个文件两边一致：4 个防护状态 json、`更新ClaudeDesktop.ps1`、`一键更新ClaudeDesktop.ps1`。
- **不导入别人的状态**：防护状态 json、指纹、GUID 必须在本机生成。
- **会话代理**：若会话环境被注入 `HTTPS_PROXY=http://127.0.0.1:43867`，访问国内站点前用 `env -u HTTPS_PROXY -u HTTP_PROXY -u ALL_PROXY` 去掉代理变量，不要为此改 Clash。

## 文档规范

- 结论和操作先写；每行一个事实；成对信息用表格；bullet 用加粗字段名开头。
- 相同事实只在一个位置维护；区分当前状态、历史证据、未验证事项。
