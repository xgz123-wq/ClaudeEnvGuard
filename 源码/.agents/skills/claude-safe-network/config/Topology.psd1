# ============================================================================
#  Claude 安全出口链路 —— 拓扑单一真源 (Single Source of Truth)
# ----------------------------------------------------------------------------
#  ★ 这是唯一需要改端口 / 组名的地方。
#    改完必须按 Check-PortConsistency.ps1 输出的「字面量清单」逐行同步
#    （当前 18 个文件 / 56 行含真源端口），完整步骤见 references/03-变更与回滚SOP.md 的 SOP-3。
#
#  本文件是纯数据文件，不允许写 Join-Path / 环境变量等表达式，
#  一律用 ${APPDATA} / ${LOCALAPPDATA} 占位符，由 scripts/Common.ps1 展开。
# ============================================================================
@{
    Version = '2026-09-13'

    Ports = @{
        Mixed        = 43865   # Clash Verge 混合端口（HTTP + SOCKS5）—— Claude Code 走这个
        ChromeSocks  = 43866   # 专用 Chrome 的 SOCKS5 入口 —— 浏览器走这个
        DesktopHttp  = 43867   # Desktop 专用 HTTP 入口 —— Claude Desktop 走这个
        Controller   = 9097    # mihomo external-controller（本机未监听，留档）
    }

    Groups = @{
        Primary = 'ACCOUNT-US'  # 自建全局脚本注入的组，Claude 专用；不依赖订阅自带的组名
    }

    Listeners = @{
        Socks       = 'account-us-socks'   # 43866 的 listener 名
        DesktopHttp = 'claude-desktop-http' # 43867 的 listener 名
        InboundRule = 'IN-NAME,account-us-socks,ACCOUNT-US'
    }

    # 必须走 ACCOUNT-US 的 Claude 生态域名（全局脚本会写 DNS 分层 + 置顶规则）
    ClaudeDomains = @(
        '+.anthropic.com'
        '+.claude.ai'
        '+.claude.com'
        '+.claude.app'
        '+.clau.de'
        '+.claudemcpclient.com'
        '+.claudemcpcontent.com'
        '+.claudeusercontent.com'
    )
    # 遥测域名（sentry / statsig / datadog / intercom）不走 ACCOUNT-US：现场 Script.js 把它们列为旧规则清除（2026-09-23 核对）

    # Claude 归属的 ASN / CIDR（规则里做 IP 兜底，防止域名前置）
    ClaudeAsn    = 399358
    ClaudeCidr4  = '160.79.104.0/21'
    ClaudeCidr6  = '2607:6bc0::/32'

    # 出口 IP 信誉合格线（由 Check-IpReputation.ps1 / Check-ClaudeIp.ps1 执行）
    Thresholds = @{
        IpRiskScorePass  = 90     # iprisk.top 纯净度 ≥ 90/100
        IpRiskScoreWarn  = 70     # 70~89 警告，<70 不合格
        Ping0RiskPass    = 20     # ping0.cc 风控值 < 20%
        Ping0WantIpType  = '家庭宽带 IP'   # ping0.cc 期望的 IP 类型；出现「IDC机房IP」= 住宅属性丢失
        CoffeeTrustPass  = 80     # ip.net.coffee/claude/ 的 Trust Score 合格线
        CoffeeWantAttr   = '家庭住宅IP'    # ip.net.coffee 期望的 IP 属性
        IppureFactorPass = 0      # ippure.com 系数必须为 0%
        IppureHumanWarn  = 85     # 人机流量占比低于此值需关注
    }

    # 无头 Chrome 真实渲染（Invoke-HeadlessDomFetch）的取数窗口。
    # 实测：这两个站都靠前端 JS 注入数值，只给 --timeout 会拿到半成品页，
    # 必须配合 --virtual-time-budget；8000ms 是实测「数据完整 + 用时最短」的点。
    Headless = @{
        TimeoutSec    = 30
        VirtualTimeMs = 8000
        MinDomBytes   = 400    # 低于此长度视为空壳页 / 被风控拦下
        Retry         = 2      # 被挑战页拦下时的总尝试次数（含首次）
        # 持久化无头 Profile：复用 cookie 才像"回访用户"。全新临时目录每次都像陌生访客，
        # 短时间多跑几次就会被 ping0.cc 的 Turnstile 挡下（实测踩过）。
        ProfileDir    = '${LOCALAPPDATA}\claude-safe-network\chrome-profile'
    }

    # 三站 + 一个 Claude 专向检测（Headless=yes 表示已实测可全自动取数）
    ReputationSites = @(
        @{ Name = 'ping0.cc';    Url = 'https://www.ping0.cc/?ip={ip}';        Headless = 'yes'; Note = '风控值 / IP 类型 / 原生 IP / 共享人数' }
        @{ Name = 'ippure.com';  Url = 'https://ippure.com/';                  Headless = 'no';  Note = '人机流量占比 / IP 来源属性（需混淆 JS 生成的 x-t 签名头，只能浏览器看）' }
        @{ Name = 'iprisk.top';  Url = 'https://iprisk.top/ip/{ip}';           Headless = 'yes'; Note = '纯净度评分 / 机房·代理信号 / 黑名单' }
        @{ Name = 'ip.net.coffee'; Url = 'https://ip.net.coffee/claude/';      Headless = 'yes'; Note = 'Claude 专向检测：Trust Score / 属性 / VPN / 滥用记录' }
    )

    # Clash Verge Rev 数据目录（%APPDATA% 下的相对段）
    VergeDir    = '${APPDATA}/io.github.clash-verge-rev.clash-verge-rev'
    VergeCore   = 'C:/Program Files/Clash Verge/verge-mihomo.exe'
    ChromeExe   = 'C:/Program Files/Google/Chrome/Application/chrome.exe'

    # 专用浏览器账号：每个账号一个独立 Chromium User Data 目录（账号隔离的关键）
    Accounts = @(
        @{
            Name        = '账号1'
            UserDataDir = '${LOCALAPPDATA}/ClaudeAccountBrowsers/账号1/UserData'
            Shortcut    = 'Claude-账号1-专用Chrome.lnk'
        }
        @{
            Name        = 'Claude-账号2'
            UserDataDir = '${LOCALAPPDATA}/ClaudeAccountBrowsers/Claude-账号2/UserData'
            Shortcut    = 'Claude-账号2-专用Chrome.lnk'
        }
        @{
            Name        = 'Gemini专用'
            UserDataDir = '${LOCALAPPDATA}/ClaudeAccountBrowsers/Gemini专用/UserData'
            Shortcut    = 'Gemini专用.lnk'
        }
    )

    # 专用 Chrome 的防旁路参数（顺序即最终命令行顺序，{userdata} / {url} 为占位符）
    ChromeArgs = @(
        '--user-data-dir="{userdata}"'
        '--proxy-server="socks5://127.0.0.1:{chromeSocks}"'
        '--host-resolver-rules="MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost"'
        '--lang=en-US'
        '--accept-lang="en-US,en"'
        '--force-webrtc-ip-handling-policy=disable_non_proxied_udp'
        '{url}'
    )

    # 桌面快捷方式（用户资产：本套件只「还原」不「重建」，不会把图标改指 skill 内部）
    #   assets/shortcuts/<Name> 是原版逐字节备份；scripts/apply/Repair-Shortcuts.ps1 以备份为准还原。
    #   BaselineScript 记录迁移前的原始 -File 目标 —— 这是**仅供核对的历史归档路径字符串**：
    #   本套件不解析、不依赖它，docs\配置\ 被删掉也不影响任何脚本运行。
    #   SkillScript 是 skill 内维护的等价脚本，想跑"修正版"时命令行直呼即可，别动桌面图标。
    DesktopShortcuts = @(
        @{
            Name           = 'Claude-环境一键总检.lnk'
            BaselineScript = 'docs\配置\Claude环境\Claude环境一键总检.ps1'
            SkillScript    = 'scripts\Claude环境一键总检.ps1'
            Description    = '只读检查 Clash、Claude 三入口、飞书、DNS 与浏览器防护；通过后打开专用检测浏览器'
        }
        @{
            Name           = 'Claude-五项自检.lnk'
            BaselineScript = 'docs\配置\Claude环境\Clash\Claude日常五项自检.ps1'
            SkillScript    = 'scripts\clash\Claude日常五项自检.ps1'
            Description    = 'Claude 账号环境日常五项自检'
        }
        @{
            Name           = 'Claude-英文浏览器.lnk'
            BaselineScript = 'docs\配置\Claude环境\启动Claude英文浏览器.ps1'
            SkillScript    = 'scripts\desktop\启动Claude英文浏览器.ps1'
            Description    = '内置网页英语；时区沿用当前系统；启动前检查专用代理端口'
        }
    )

    # Claude 专向 / IP 信誉检测页（专用 Chrome 首屏 + 人工核验用）
    HandoffUrls = @(
        'https://ip.net.coffee/claude/'
        'https://www.ping0.cc/?ip={ip}'
        'https://ippure.com/'
    )

    # Cloudflare 边缘 trace 端点：Anthropic 全站挂 Cloudflare，这是最贴近 Claude 视角的落地身份
    TraceUrls = @(
        'https://www.cloudflare.com/cdn-cgi/trace'
        'https://claude.ai/cdn-cgi/trace'
    )

    # Claude Desktop 策略注册表（注意：没有 Anthropic 中间层）
    DesktopPolicyKeys = @(
        'HKLM:\SOFTWARE\Policies\Claude'
        'HKLM:\SOFTWARE\WOW6432Node\Policies\Claude'
    )
}
