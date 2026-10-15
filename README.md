# Claude 环境卫士

按**出口 IP** 体检 Claude 三个入口（专用浏览器、Claude Code、Claude Desktop）的使用环境，带一键修复、整次还原和登录清理。Windows 专用，Go + Wails 编写。

- **怎么用、查什么、数据存哪**：见 [新版/README.md](新版/README.md)。
- **最近做了什么、哪些没验证**：见 [维护记录.md](维护记录.md)。
- **当初的需求原文**：见 [重构需求.md](重构需求.md)。

## 目录

| 位置 | 内容 | 在 Git 里 |
|---|---|---|
| `新版\` | 源码、测试、`打包.ps1` | 是 |
| `ClaudeEnvGuard.exe`、`ceg.exe` | 部署的程序（桌面图标指向前者） | 否，放在 GitHub Release |
| `卸载.bat` | 只删程序、图标和卸载项，保留源码和备份 | 是 |
| `维护记录.md`、`重构需求.md` | 维护记录、需求原文 | 是 |

## 拿回某个版本

每次部署打一个标签，标签里有那一版的全部源码和文档；v2 起的标签在 GitHub Release 里附带两个 exe。

| 标签 | 是什么 | 程序怎么拿 |
|---|---|---|
| `v1-原型` | Kimi 做的 Python + PySide6 版，在 `源码\` 目录 | 没有现成 exe；要按该版本 `源码\README.md` 装 Python 3.12 和 PySide6 后重新打包 |
| `v2.0.0` | Go 重构版 | GitHub Release 页直接下载（2026-10-15 从标签源码补打包） |
| `v2.0.1` 及以后 | 窗口按屏幕缩小，之后的版本 | GitHub Release 页直接下载 |

| 想要 | 做法 |
|---|---|
| 只看看旧版代码 | GitHub 仓库页 → Tags → 选标签 → Download ZIP |
| 在本机另放一份旧版，不影响现在的 | `git -C D:\Tools\ClaudeEnvGuard worktree add D:\Tools\环境卫士-旧版 <标签>`，用完 `git -C D:\Tools\ClaudeEnvGuard worktree remove D:\Tools\环境卫士-旧版` |
| 从源码打包某个 v2 版本 | 先把 `main` 上的 `新版\打包.ps1` 复制进上面那份旧版目录覆盖（`v2.0.1` 及更早的打包脚本在新克隆的目录里会因缺 `frontend\dist` 失败），再运行 `pwsh -File 新版\打包.ps1`，exe 在 `新版\build\bin\` |

- **不要**在 `D:\Tools\ClaudeEnvGuard` 本身执行 `git checkout <旧标签>`：工作目录会整个变成旧版，桌面图标指向的 exe 不受影响，但源码会被替换，容易忘了切回来。

## 给别人用

| 对方要什么 | 做法 |
|---|---|
| 只用程序 | 把 Release 里的 `ClaudeEnvGuard.exe` 和 `ceg.exe` 发给他，放同一个目录双击即可 |
| 看或改源码 | GitHub 仓库 Settings → Collaborators 加他的账号（仓库保持私有） |

- **仓库不能直接改公开**：Git 历史里有本机真实出口 IP（维护记录和测试数据）。要公开，先改写全部历史去掉它，再改可见性。

## 开发规范

### 版本与分支

- **分支**：只用 `main`，直接在上面提交；只有大改、可能整体放弃时才临时开分支，合回 `main` 后删掉。
- **版本号**：`主.次.修`。修 bug 加修订号，加功能加次版本号；同时改 `新版\internal\version\version.go` 和 `新版\wails.json` 的 `productVersion`。
- **提交信息**：一行简短中文，术语保留英文，例如「窗口按屏幕工作区缩小」。

### 改代码到上线

1. 改代码，补对应的单元测试（系统调用走 `internal/platform` 的 `Fake`，不碰真机）。
2. `pwsh -File 新版\打包.ps1`：跑全部测试并构建两个 exe；测试不过不会打包。
3. 关掉正在运行的环境卫士窗口，`pwsh -File 新版\打包.ps1 -Deploy` 部署到本目录。
4. 双击桌面图标实测改动点；涉及修复、还原、清理的，由本人在界面里操作确认。
5. 提交 → 打标签 `git tag -a vX.Y.Z -m "一句话说明"` → `git push` 和 `git push --tags`。
6. 发 Release：`gh release create vX.Y.Z ClaudeEnvGuard.exe ceg.exe --title vX.Y.Z --notes "一句话说明"`（在本目录执行）。
7. `维护记录.md` 的「历史」表加一行；大改写成完整一节，上一节压成一行。

### 边界

- **AI 在本机只跑只读命令**：`ceg check`、`ceg fix --plan`、`ceg clean <类型> --plan`；`fix --apply`、`restore`、`clean --yes` 要本人当次同意。
- **不进 Git 的东西**：exe、`build\bin\`、`node_modules`、`dist`、`wailsjs`、日志、`新版\frontend\dev\*.json`（含真实 IP 的检查结果）。目前写在本机 `.git\info\exclude`，新克隆的仓库不带这份规则。
- **工具链**：Go 1.27（`%LOCALAPPDATA%\Programs\go`）、Wails v2.14（`%USERPROFILE%\go\bin`）、Node 24；不用 conda 的 Python。
- **文档**：同一件事只写在一处，其他地方链接过去；用法写 `新版\README.md`，规范写本文件，经过和结果写 `维护记录.md`。
