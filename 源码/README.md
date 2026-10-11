# Claude 环境卫士（开源复刻版）v1.0

参考经典「Claude 环境卫士 v2.0」界面与功能复刻的开源 GUI：**检查 · 配置 · 恢复** Claude 运行环境，代码全部可见，无后台行为。

> 本仓库分支 `gui-v1.0-release` 为 GUI 版本，主分支保留原 PowerShell 脚本包。

## 界面

深色卡片式布局，与参考截图一致：

- **网络出口信息**：公网 IP / 国家地区 / 城市 / ISP / ASN / 代理出口
- **目标地区配置**：目标地区下拉 + 禁用 IPv6
- **环境检查项（9 项）**：系统时区、系统/浏览器语言、DNS 泄露检测、
  ANTHROPIC_BASE_URL、IPv6、WebRTC (Chrome/Edge)、系统代理、Claude 连通性
- **一键配置**：按目标地区自动配置时区/语言/代理/IPv6/WebRTC 策略
- **一键清理**：Claude Code 凭据 / Claude 桌面端 / Claude 网页版（先备份再删）

## 运行

```bash
pip install -r requirements.txt
python main.py
```

Windows 下打包成单文件 exe：

```bat
packaging\build-windows.bat
```

## 目录结构

```
main.py                  入口
app/
  core/                  检测 / 配置 / 清理核心逻辑（与 UI 解耦）
    system.py            系统信息读取（时区/语言/DNS/代理/IPv6/WebRTC/连通性/出口IP）
    checks.py            9 项环境检查的组合与判定
    actions.py           一键配置动作（调用 PowerShell，自动请求提权）
    cleanup.py           三项清理（备份优先）
  scripts/
    configure-us.ps1     一键配置美国（时区/语言/代理/IPv6/WebRTC/清 BASE_URL/刷新DNS）
    configure-cn.ps1     一键配置中国（还原为国内直连环境）
  ui/                    PySide6 界面
    theme.py             深色主题 QSS
    cards.py / check_row.py / workers.py / main_window.py
  resources/docs/        「环境检查说明」「软件操作说明」（界面内可打开）
packaging/               PyInstaller 打包脚本
```

## 设计原则

1. **只读检查**：「检查环境」不修改任何系统设置。
2. **备份优先**：所有清理操作先备份到 `%LOCALAPPDATA%\ClaudeEnvGuard\backups\`。
3. **需要提权才提权**：一键配置通过 UAC 请求管理员权限，脚本开源可见。
4. **降级友好**：非 Windows 环境或读取失败时给出「需注意」而不是报错崩溃。

## 免责声明

本项目为个人学习用途的开源复刻，与 Anthropic 及任何同名软件无关。
使用一键配置前请阅读 `app/resources/docs/软件操作说明.md`。
