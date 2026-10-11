# Clash DNS 分层说明

> 整理日期：2026-09-23
> 用途：说明网络分工与维护边界

## 网络分工

- **普通应用**：国内流量按规则直连，国外流量按订阅规则分流。
- **Claude 域名**：使用独立解析规则，连接沿既有 `ACCOUNT-US` 通道。
- **普通 DNS**：沿用当前合并配置的通用解析器。
- **判断原则**：国内网站解析到国内地址，本身不代表 DNS 泄漏。

## IPv6 三层关闭

| 层 | 操作位置 | 现状（2026-09-23 核对） |
|---|---|---|
| 网卡 | 控制面板 → 网络和 Internet → 网络连接 → 网卡属性 → 取消勾选「Internet 协议版本 6 (TCP/IPv6)」 | WLAN、VMnet1、VMnet8 已关；`Mihomo`（TUN 虚拟网卡）保持开启，属正常 |
| Clash 全局 | Clash Verge 设置里的 IPv6 开关 | `config.yaml`、`clash-verge.yaml` 为 `ipv6: false` |
| Clash DNS 覆写 | DNS 覆写里的 IPv6 开关 | `dns_config.yaml` 为 `ipv6: false` |

- **命令等价操作**（管理员）：`Disable-NetAdapterBinding -Name 'WLAN' -ComponentID ms_tcpip6`；查看用 `Get-NetAdapterBinding -ComponentID ms_tcpip6`。
- **作用范围**：按网卡生效，同一张网卡换 Wi-Fi 不用重做；新增网卡（USB 网卡、新无线网卡）要重新取消勾选。
- **自动检查**：五项自检和 Skill `Check-WindowsFallback.ps1` 会检查物理网卡 IPv6 绑定，未关闭时报出。
- **现状结论**：本机没有公网 IPv6 地址，也没有 IPv6 默认路由。

## 维护与验收

- **现场优先**：以当前生效脚本、运行配置及实际连接为准。
- **禁止盲同步**：不得用本目录或技能内的旧脚本直接覆盖现场 `Script.js`。
- **变更顺序**：先比对差异，再备份，仅修改已确认的问题。
- **核验内容**：运行模式、监听、规则命中及真实出口。
- **网页边界**：普通浏览器检测结果不能替代 Claude 专用浏览器验收。

## Claude 域名定期复核

**结论**：每周以及 Desktop、CLI、浏览器、插件更新后，对照 [Anthropic Desktop 网络要求](https://code.claude.com/docs/en/desktop#network-access-requirements)和 [Claude Code 网络要求](https://code.claude.com/docs/en/network-config#network-access-requirements)复核域名；订阅、节点、Clash 规则变化或登录、下载、桥接异常时也复核。官方列表是来源，实际连接和 Mihomo 命中是现场证据。

| 入口 | 新域名如何进入 `ACCOUNT-US` | 复核重点 |
|---|---|---|
| CLI `43865` | 共用入口按置顶 Claude 域名规则分流 | 新域名、命中规则、DNS policy、最终策略组与出口 |
| 专用 Chrome `43866` | 进入专用 SOCKS 的连接由置顶 `IN-NAME` 规则分流 | 实际浏览器连接是否进入 `43866`，以及网页出口、DNS、WebRTC |
| Desktop `43867` | 进入专用 HTTP listener 的连接指定 `ACCOUNT-US` | Desktop 与子进程的真实连接是否进入 `43867` |

**分工**：官方清单比对由脚本 `Clash/检查Claude官方域名.ps1` 完成，只有脚本报出未归类新域名时才需要模型判断。

| 步骤 | 谁做 | 说明 |
|---|---|---|
| 抓官方两份文档、提取域名、按后缀归类、核对路由脚本与验证脚本的清单一致 | 脚本（只读，只 GET 官方 Markdown） | 退出码 `0` 全部已归类，保持安静；`1` 有未归类新域名；`2` 抓取或解析失败、本地清单不一致 |
| 判断新域名是 Claude 专属还是共享第三方 | 模型 | 看官方用途说明；共享的写入脚本内 `$sharedHosts` 并写明原因，专属的走变更流程加规则 |
| 更新路由与验证脚本、应用到 Clash | 用户手动完成 | 模型不更新、不重启 Clash 或 Claude，也不热重载；只在旁边审查 |
| 核对现场规则、运行配置、验证脚本有没有遗漏或漂移 | 模型（只读） | 结论与建议写给用户 |
| 真实浏览器网页验收、Desktop 内置浏览器验收 | 用户 | 未观察到的访问只记为规则覆盖，不记为实测通过 |

**归类规则**：被 `Clash全局账号通道.js` 里 Claude 域名后缀覆盖的记为已覆盖，如 `platform.claude.com`、`downloads.claude.ai`、`bridge.claudeusercontent.com`；在 `$sharedHosts` 里的记为共享，如 npm、GitHub、Google Storage、Datadog、字体与脚本 CDN；其余都是未归类。共享域名不全局绑定 `ACCOUNT-US`。

**运行**：`pwsh -NoProfile -File docs\配置\Claude环境\Clash\检查Claude官方域名.ps1`，输出 JSON；`-OfflineDir <目录>` 可用已下载的 `network-config.md`、`desktop.md` 离线测试。Skill 副本 `scripts/clash/检查Claude官方域名.ps1` 与它逐字节相同。

**新增域名后**：由用户更新；模型只读核对 `Script.js`、Claude 专用 DNS policy、通道验证脚本、Skill 副本和运行配置是否都带上新规则、顺序是否在宽泛规则之前，有真实连接证据时再看脱敏的 Mihomo 目标域名、入站、规则、组和出口。

**规则来源**：Anthropic Desktop 网络要求列出 `claude.app` 及动态子域，因此本分享版的规则模板包含 `DOMAIN-SUFFIX,claude.app` 与 `+.claude.app`；部署后的实际访问和出口要在使用者机器上验证。CLI 官方清单未把它列为独立的 CLI 必需域名。

## 文件与阅读入口

| 材料 | 作用 |
|---|---|
| [Claude 配置总览](../README.md) | 统一入口，先读 |
| `%APPDATA%\io.github.clash-verge-rev.clash-verge-rev\profiles\Script.js` | 需现场核对的 Clash 全局脚本 |
| `../Clash/Clash全局账号通道.js` | 项目副本；2026-09-23 与现场 `Script.js`、Skill 副本三处哈希一致，维护前仍先比较 |
