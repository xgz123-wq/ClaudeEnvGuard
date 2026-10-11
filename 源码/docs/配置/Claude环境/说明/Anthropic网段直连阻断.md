# Anthropic 网段直连阻断

## 结论与操作

`160.79.104.0/23` 的直连出站由 Windows 防火墙阻断；访问该网段时，应用仍可先连接本机 Clash 代理。现有入口为 Claude Code HTTP `43865`、专用 Chrome SOCKS `43866`、Claude Desktop HTTP `43867`。三条入口均应经 `ACCOUNT-US` 出口。

| 操作 | 命令 |
|---|---|
| 只读检查 | `pwsh -File docs/配置/Claude环境/Anthropic网段直连阻断.ps1` |
| 首次安装或复核 | 以管理员身份运行 `pwsh -File docs/配置/Claude环境/Anthropic网段直连阻断.ps1 -Action Install` |
| 日常总检 | `pwsh -File docs/配置/Claude环境/Claude环境一键总检.ps1`，查看「Anthropic 网段直连阻断」一项 |

**规则标识**：`Claude-Anthropic-Direct-Block-160.79.104.0-23`；覆盖所有程序、端口和 Windows 网络配置文件。安装脚本遇到同名但配置不符的规则会停止，不会覆盖现有规则。

## 覆盖边界

[Anthropic 官方 IP 文档](https://platform.claude.com/docs/en/api/ip-addresses)把 `160.79.104.0/23` 列为 API／Console 接收连接的 IPv4 网段。该列表不能证明 Claude 网页及相关服务只使用这些地址；本规则只管这个 IPv4 网段，不替代 Clash 域名路由、DNS 防旁路和 Claude 程序的 WFP 直连防护，也不保证账号不会触发平台风控。

**验证含义**：总检会核对规则已持久生效、方向和网段准确、没有限制到个别程序或端口；代理出口与三入口一致性由总检的其他项目核对。真实网页、登录和 Clash 关闭后的断线行为仍按[维护日志](../维护日志.md)的未验收项处理。
