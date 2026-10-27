<!-- README_SYNC: source=working-tree; updated=2026-10-27 -->
<p align="center">
  简体中文 · <a href="./README_EN.md">English</a>
</p>

# Claude 环境卫士

> 给 [Claude 环境分享包](https://github.com/xgz123-wq/claude-env-share) 用户的 Windows 体检工具：换了代理节点之后双击它，约 5 秒查清 Claude 的三个入口是不是还走同一个出口、有没有哪里露出真实位置；不对的项一键修复，修错了能按项撤回。

如果它帮你省掉了换节点后的手动排查，可以点个 [Star](https://github.com/xgz123-wq/ClaudeEnvGuard/stargazers)，方便以后回来，也让有同样需求的人更容易找到。问题和建议请提 [Issue](https://github.com/xgz123-wq/ClaudeEnvGuard/issues)。

[![Stars](https://img.shields.io/github/stars/xgz123-wq/ClaudeEnvGuard?style=flat-square&logo=github)](https://github.com/xgz123-wq/ClaudeEnvGuard/stargazers)
[![Release](https://img.shields.io/github/v/release/xgz123-wq/ClaudeEnvGuard?style=flat-square)](https://github.com/xgz123-wq/ClaudeEnvGuard/releases/latest)
![Windows 10/11](https://img.shields.io/badge/Windows-10%20%2F%2011-0078d4?style=flat-square)
![Go + Wails](https://img.shields.io/badge/Go%20%2B%20Wails-v2-00add8?style=flat-square)
[![License: MIT](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/xgz123-wq/ClaudeEnvGuard?style=flat-square)](https://github.com/xgz123-wq/ClaudeEnvGuard/commits/main)

| 手动排查 | 用环境卫士 |
|---|---|
| 三个入口各开一遍检测网站，对比 IP 和国家 | 三个入口各经自己的端口查 `claude.ai`，一张表列出 Claude 实际看到的出口 |
| 自己记出口在哪、该设什么时区和语言 | 按出口 IP 所在地推算目标时区和语言，对不上直接标出来 |
| 改注册表、环境变量、浏览器参数，改错了不知道怎么改回去 | 先列修复计划，你勾选后执行；每步先备份，能按项还原 |
| 换一次节点就重来一遍 | 打开即查，约 5 秒出完整结果 |

## 它解决什么问题

在国内用 Claude，流量要经代理从海外出去。Claude 有三个入口：**专用浏览器（网页版）、Claude Code（命令行）、Claude Desktop（桌面版）**，各自的代理设置分开管。除了 IP，还有不少地方会暴露真实位置：

| 容易出问题的地方 | 表现 |
|---|---|
| 三个入口出口不一致 | 浏览器从美国出去，Claude Code 却从别的国家出去，同一个账号短时间内「跳国家」 |
| DNS、WebRTC、IPv6 泄漏 | IP 是海外的，但域名解析、浏览器 WebRTC 或 IPv6 绕过了代理 |
| 时区、语言对不上 | 出口在美国，系统时区却是北京时间、浏览器语言是中文 |
| 代理配置丢了 | 更新或重装后，某个入口的代理设置没了，悄悄直连 |

环境卫士把这些做成 30 项检查，分线路、专用浏览器、Claude Code、Desktop、系统五类，每一项写明查什么、能不能自动修。完整清单见 [新版/README.md「检查项」](新版/README.md#检查项)。

## 适合谁

| 情况 | 能不能用 |
|---|---|
| 已按 [Claude 环境分享包](https://github.com/xgz123-wq/claude-env-share) 部署好环境 | ✅ 全部功能可用，这是它的设计前提 |
| 自己用 Clash Verge 配过类似环境，三个入口各有专用端口 | ⚠️ 体检、修复能用：端口和默认的不一样时，照下文「部署清单」的格式手写一份清单；更新、深度总检、英文启动用不了 |
| 还没配环境，想找一个装上就能用 Claude 的工具 | ❌ 它不搭环境、不提供代理。先看[分享包](https://github.com/xgz123-wq/claude-env-share) |
| macOS / Linux | ❌ 只支持 Windows |

## 快速开始

1. 到 [Releases](https://github.com/xgz123-wq/ClaudeEnvGuard/releases/latest) 下载 `ClaudeEnvGuard.exe`（图形界面）和 `ceg.exe`（命令行），放进同一个文件夹，例如 `D:\Tools\ClaudeEnvGuard\`。
2. 双击 `ClaudeEnvGuard.exe`。程序没有代码签名，Windows 可能提示「已保护你的电脑」，点「更多信息 → 仍要运行」。
3. 打开即自动体检。全绿就不用管；有红黄项，点结论区的「一键修复」，看清计划再勾选执行。

- **按分享包部署的不用自己下**：让 AI 按[分享包](https://github.com/xgz123-wq/claude-env-share)的 AGENTS.md 部署时，它会把这两个 exe 下到和分享包同一个目录、建好桌面「Claude 环境卫士」图标；卫士按部署清单找分享包，什么都不用填。
- **不用安装**：两个 exe 是独立程序，不需要 Python 或 Node；图形界面用 Windows 自带的 WebView2，命令行版不需要。
- **不需要管理员**：只有改系统时区、IPv6、Desktop 代理策略这几项会弹一次 UAC，由你点确认。

## 能做什么

| 页面 | 做什么 |
|---|---|
| 体检 | 30 项检查；一键修复、深度总检、打开专用浏览器人工复核、清理登录数据 |
| 更新 | 查 Desktop、CLI、VS Code 插件三端的新版本（约 2 秒），一键更新并显示进度 |
| 记录 | 修复、还原、更新、清理按时间排列，显示「改之前 → 改之后」；修复可以只勾几项撤回 |
| ⚙ 设置 | 三个端口和当前使用的分享包（都只读，来自部署清单）、目标地区、专用浏览器和起始网址、数据存放位置 |

可点的界面原型（模拟数据）在 [原型/](原型/)，下载后双击 HTML 就能打开。

## 和分享包的关系

**单向依赖：卫士依赖分享包，分享包不依赖卫士。** 分享包负责搭环境，卫士负责搭好之后的日常体检。

exe 自己就能完成体检、修复、还原、清理。下面三个功能只是给分享包里的 PowerShell 脚本套了界面，没有分享包时会提示找不到脚本：

| 功能 | 调用的脚本（分享包内的位置） |
|---|---|
| 整个更新页（查版本 + 一键更新） | `docs\配置\Claude环境\Desktop防护\更新三端Claude.ps1` |
| 深度总检 | `docs\配置\Claude环境\Claude环境一键总检.ps1` |
| 检查后启动 Desktop（英文） | `docs\配置\Claude环境\启动Claude英文浏览器.ps1` |

- **部署清单**：分享包的一键总检每次运行都把三个端口（从 Clash 读）、自己的位置和登记的专用浏览器写进 `%LOCALAPPDATA%\ClaudeNetworkGuard\部署清单.json`；卫士只读它，读到的每一项仍会实际核对。端口以清单为准、设置页只读：要改端口先在 Clash 里改，再跑一次一键总检。没有分享包时，可以照分享包里 `部署清单.示例.json` 的格式手写这个文件。
- **脚本找法**：不用手填。依次找部署清单里的脚本根目录（旧版设置里手填过的目录优先，设置页可清除）、`桌面\AI应用开发`、`桌面\Claude环境分享版`，再到桌面和「文档」下的一级子文件夹里找，用第一个找到的；设置页显示当前用的是哪一份。直连防护状态（D3）也用同一套找法。
- **查版本**：由程序自己联网查，但开始前会先确认更新脚本存在，找不到就报「查版本失败」。
- **还需要**：PowerShell 7（`pwsh`），分享包本来就要求安装。

## 安全边界

- **改之前先备份**：每个修复动作执行前，把旧值或旧文件存进 `%LOCALAPPDATA%\ClaudeEnvGuard\备份\`，失败自动撤回；修完 10 秒内可一键撤销，之后在「记录」页按项还原。
- **只改白名单**：需要管理员的动作只允许改 Claude 代理策略、时区、IPv6、指定环境变量和指定防火墙规则。
- **清理先列清单**：清登录数据前列出每个要删的路径，勾「我核对过」才删；Claude 在运行时拒绝清理；删了什么写进记录，不含凭据内容。
- **查不到就说查不到**：读不到、超时一律显示「未知」，不会显示「通过」。
- **会联网访问的地址**：`claude.ai`（看 Claude 眼里的 IP，只经专用端口）、`ip.net.coffee` 和 `ipinfo.io`（查出口所在地和时区）；更新页另查 npm、VS Code 市场和 Claude Desktop 官方下载入口。除了这些查询，不向任何地方发送本机数据。

## 命令行

`ceg.exe` 和图形界面是同一套检查，适合写脚本或交给 AI 助手调用：

```bash
ceg check            # 全量体检
ceg fix --plan       # 只列修复计划，不改任何东西
ceg update --check   # 只查三端版本
```

全部命令和退出码见 [新版/README.md「命令行」](新版/README.md#命令行cegexe)。

## 常见问题

| 问题 | 回答 |
|---|---|
| 全是「失败」或「未知」 | 多半是代理客户端没开，或端口和部署清单（没有清单时是默认的 43865 / 43866 / 43867）不一样；跑一次分享包的一键总检刷新清单后再查 |
| 只支持 Clash Verge 吗 | 端口、出口、DNS 等通用项任何代理客户端都能查；P2「Clash 配置」只认 Clash Verge Rev |
| 体检全绿就一定没问题吗 | 它查的是启动参数和系统设置。WebRTC、浏览器里的 JS 时区等，最终以专用浏览器打开 `ip.net.coffee/claude/` 看一眼为准，体检页有「打开这个浏览器」按钮 |
| 杀毒软件报毒 | 没签名的 Go 程序偶尔被误报；不放心可以按[维护者指南](维护者指南.md)从源码自己打包 |
| 怎么卸载 | 删掉两个 exe 和桌面图标；数据在 `%LOCALAPPDATA%\ClaudeEnvGuard\`，不要了一起删 |

## 项目状态

- **当前版本**：v2.6.1（2026-10-27），每个版本的 exe 都在 [Releases](https://github.com/xgz123-wq/ClaudeEnvGuard/releases)。
- **测试**：Go 单元测试覆盖检查判定、修复、还原、清理和更新查版本，系统调用全部走模拟对象，不碰真机；没有接 CI，由作者打包前在本机运行。
- **维护方式**：作者自用，随分享包一起更新；没有固定的发版计划。

## 文档

| 想看 | 文件 |
|---|---|
| 每项检查、命令行、数据存哪、更新页细节 | [新版/README.md](新版/README.md) |
| 改代码、打包、发版本、回退旧版 | [维护者指南.md](维护者指南.md) |
| 最近改了什么、哪些还没验证 | [维护记录.md](维护记录.md) |
| 已完成的需求、五项自检对比、复盘（只留存） | [历史/](历史/) |

---

觉得有用就点个 [Star](https://github.com/xgz123-wq/ClaudeEnvGuard/stargazers)；想知道新版本，在仓库页点 **Watch → Custom → Releases**；遇到问题欢迎提 [Issue](https://github.com/xgz123-wq/ClaudeEnvGuard/issues) 或 PR。

## 许可证

[MIT](LICENSE)
