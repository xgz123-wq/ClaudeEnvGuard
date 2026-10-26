<!-- README_SYNC: source=working-tree; updated=2026-10-26 -->
<p align="center">
  <a href="./README.md">简体中文</a> · English
</p>

# Claude Env Guard

> A Windows health check for users of the [Claude environment share pack](https://github.com/xgz123-wq/claude-env-share). After switching proxy nodes, double-click it: in about 5 seconds it tells you whether all three Claude entry points still leave through the same exit and whether anything leaks your real location. Fix problems in one click, and roll back individual fixes if one was wrong.

If it saves you the manual re-check after every node switch, consider giving it a [Star](https://github.com/xgz123-wq/ClaudeEnvGuard/stargazers) so you can find it again and others with the same setup can discover it. Questions and ideas are welcome as [Issues](https://github.com/xgz123-wq/ClaudeEnvGuard/issues).

[![Stars](https://img.shields.io/github/stars/xgz123-wq/ClaudeEnvGuard?style=flat-square&logo=github)](https://github.com/xgz123-wq/ClaudeEnvGuard/stargazers)
[![Release](https://img.shields.io/github/v/release/xgz123-wq/ClaudeEnvGuard?style=flat-square)](https://github.com/xgz123-wq/ClaudeEnvGuard/releases/latest)
![Windows 10/11](https://img.shields.io/badge/Windows-10%20%2F%2011-0078d4?style=flat-square)
![Go + Wails](https://img.shields.io/badge/Go%20%2B%20Wails-v2-00add8?style=flat-square)
[![License: MIT](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![Last Commit](https://img.shields.io/github/last-commit/xgz123-wq/ClaudeEnvGuard?style=flat-square)](https://github.com/xgz123-wq/ClaudeEnvGuard/commits/main)

> **Note:** the app UI, CLI output and most docs are in Chinese.

| Checking by hand | With Env Guard |
|---|---|
| Open an IP-check site from each entry point and compare IPs and countries | Each entry point queries `claude.ai` through its own port; one table shows the exit Claude actually sees |
| Remember where the exit is and which time zone and language to match | Target time zone and language are derived from the exit IP's location; mismatches are flagged |
| Edit the registry, env vars and browser flags, with no easy way back | A fix plan is listed first and runs only what you tick; every step is backed up and can be undone item by item |
| Repeat all of it after every node switch | Checks run on launch; full results in about 5 seconds |

## The problem

Using Claude from mainland China means routing traffic out through a proxy. Claude has three entry points — **a dedicated browser (web app), Claude Code (CLI) and Claude Desktop** — and each has its own proxy settings. Besides the IP itself, several other things can reveal where you really are:

| Where it goes wrong | What it looks like |
|---|---|
| Entry points use different exits | The browser exits in the US while Claude Code exits elsewhere, so one account "jumps countries" |
| DNS, WebRTC or IPv6 leaks | The IP is overseas, but DNS lookups, browser WebRTC or IPv6 bypass the proxy |
| Time zone and language mismatch | The exit is in the US, but the system clock is on Beijing time and the browser language is Chinese |
| Proxy settings get lost | After an update or reinstall one entry point silently goes direct |

Env Guard turns these into 30 checks across five groups — route, dedicated browser, Claude Code, Desktop and system — each stating what it checks and whether it can be fixed automatically. Full list: [新版/README.md (检查项)](新版/README.md#检查项).

## Who it is for

| Your situation | Will it work? |
|---|---|
| You deployed the [share pack](https://github.com/xgz123-wq/claude-env-share) | ✅ Every feature works; this is the setup it was designed for |
| You built a similar Clash Verge setup with a dedicated port per entry point | ⚠️ Checks and fixes work after you set your ports in ⚙ Settings; update, deep check and English launch do not |
| Nothing is set up yet and you want a one-click way to reach Claude | ❌ It does not build the environment or provide a proxy. Start with the [share pack](https://github.com/xgz123-wq/claude-env-share) |
| macOS / Linux | ❌ Windows only |

## Quick start

1. Download `ClaudeEnvGuard.exe` (GUI) and `ceg.exe` (CLI) from [Releases](https://github.com/xgz123-wq/ClaudeEnvGuard/releases/latest) into the same folder, e.g. `D:\Tools\ClaudeEnvGuard\`.
2. Run `ClaudeEnvGuard.exe`. The binaries are not code-signed, so SmartScreen may warn you; choose "More info → Run anyway".
3. Checks start automatically. All green means nothing to do; otherwise click "一键修复" (Fix), review the plan, tick what you want and run it.

- **No install**: both exes are standalone — no Python or Node. The GUI uses the WebView2 runtime that ships with Windows; the CLI does not need it.
- **No admin by default**: only time zone, IPv6 and the Desktop proxy policy need elevation, and you confirm a single UAC prompt.

## What it does

| Page | What you get |
|---|---|
| Check (体检) | 30 checks; one-click fix, deep check, open the dedicated browser for a manual look, clear sign-in data |
| Update (更新) | Looks up new versions of Desktop, CLI and the VS Code extension (about 2 s) and updates them with progress |
| Records (记录) | Fixes, restores, updates and clean-ups in time order with "before → after"; undo only the fix items you tick |
| ⚙ Settings | Ports, target region, dedicated browser and start page, share-pack folder, data locations |

A clickable prototype with mock data lives in [原型/](原型/); download it and open the HTML file.

## Relationship with the share pack

**One-way dependency: Env Guard needs the share pack; the share pack does not need Env Guard.** The share pack builds the environment; Env Guard checks it day to day.

The exe handles checks, fixes, restores and clean-ups on its own. The three features below are a GUI around PowerShell scripts from the share pack, and report a missing script without it:

| Feature | Script it calls (path inside the share pack) |
|---|---|
| The whole Update page (version check + update) | `docs\配置\Claude环境\Desktop防护\更新三端Claude.ps1` |
| Deep check | `docs\配置\Claude环境\Claude环境一键总检.ps1` |
| Pre-checked Desktop launch in English | `docs\配置\Claude环境\启动Claude英文浏览器.ps1` |

- **Where it looks**: `Desktop\AI应用开发`, then the share-pack folder set in ⚙ Settings, then `Desktop\Claude环境分享版`; the first match wins. If the share pack lives elsewhere, set its root folder in Settings.
- **Version check**: done by the program itself over the network, but it first confirms the update script exists and fails if it does not.
- **Also needed**: PowerShell 7 (`pwsh`), which the share pack already requires.

## Safety boundaries

- **Backup before change**: every fix action saves the old value or file to `%LOCALAPPDATA%\ClaudeEnvGuard\备份\` first and rolls back on failure; undo within 10 seconds, or restore item by item later from Records.
- **Allow-listed changes only**: elevated actions may only touch the Claude proxy policy, time zone, IPv6, specific environment variables and specific firewall rules.
- **Clean-ups list first**: every path to delete is shown and you must tick "我核对过" (I checked) before anything is removed; it refuses while Claude is running; what was deleted is logged, never the credentials themselves.
- **Unknown means unknown**: anything unreadable or timed out shows as "Unknown", never "Pass".
- **Network endpoints**: `claude.ai` (the IP Claude sees, only through the dedicated ports), `ip.net.coffee` and `ipinfo.io` (exit location and time zone); the Update page also queries npm, the VS Code Marketplace and Claude Desktop's official download endpoint. No other local data is sent anywhere.

## CLI

`ceg.exe` runs the same checks as the GUI, which makes it easy to script or hand to an AI assistant:

```bash
ceg check            # 全量体检
ceg fix --plan       # 只列修复计划，不改任何东西
ceg update --check   # 只查三端版本
```

All commands and exit codes: [新版/README.md (命令行)](新版/README.md#命令行cegexe).

## FAQ

| Question | Answer |
|---|---|
| Everything is "Fail" or "Unknown" | Usually the proxy client is not running, or your ports differ from the defaults 43865 / 43866 / 43867; change them in ⚙ Settings and it re-checks |
| Is it Clash Verge only? | Generic items such as ports, exits and DNS work with any proxy client; only P2 (Clash config) reads Clash Verge Rev |
| All green means I am safe? | It checks launch flags and system settings. For WebRTC and in-page JS time zone, the final word is opening `ip.net.coffee/claude/` in the dedicated browser; the Check page has a button for that |
| My antivirus flags it | Unsigned Go binaries occasionally trigger false positives; if in doubt, build from source with the [maintainer guide](维护者指南.md) |
| How do I uninstall? | Delete both exes and the desktop shortcut; data lives in `%LOCALAPPDATA%\ClaudeEnvGuard\` if you want it gone too |

## Project status

- **Current version**: v2.5.0 (2026-10-24); every version's exes are on [Releases](https://github.com/xgz123-wq/ClaudeEnvGuard/releases).
- **Tests**: Go unit tests cover check logic, fixes, restores, clean-ups and the version check, with all system calls behind a fake; there is no CI — the author runs them locally before each build.
- **Maintenance**: built for the author's own use and updated alongside the share pack; no fixed release schedule.

## Docs

| Topic | File (Chinese) |
|---|---|
| Every check, CLI, data locations, Update page details | [新版/README.md](新版/README.md) |
| Changing code, building, releasing, rolling back | [维护者指南.md](维护者指南.md) |
| Recent changes and what is still unverified | [维护记录.md](维护记录.md) |
| Original design requirements | [重构需求.md](重构需求.md), [更新并入需求.md](更新并入需求.md) |

---

Found it useful? A [Star](https://github.com/xgz123-wq/ClaudeEnvGuard/stargazers) helps. To hear about new versions, use **Watch → Custom → Releases** on the repo page. Problems, [Issues](https://github.com/xgz123-wq/ClaudeEnvGuard/issues) and PRs are welcome.

## License

[MIT](LICENSE)
