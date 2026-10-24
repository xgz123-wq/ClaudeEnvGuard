# 原型（2026-10-24 对齐需求时用的，历史留存）

| 想看 | 打开 |
|---|---|
| 可点的完整原型：体检 / 更新 / 记录 / 设置 | 双击 `Main.dc.html`；右上「换一种情况看看」切场景 |
| 功能地图：每个功能的按钮在哪 | `Map.dc.html`，或 `截图\Map.png` |
| 性能对照：慢在哪、能快多少 | `Speed.dc.html`，或 `截图\Speed.png` |

- **为什么要 `support.js`**：`.dc.html` 是 Claude 画布（Design）的源文件，里面的 `{{…}}` 要靠画布运行时填。`support.js` 就是从画布取回的那份运行时（内含 React），放在同一目录，双击就能正常显示；删掉它就只剩一堆 `{{…}}`。
- **线上原版**：<https://claude.ai/artifact/XeicHJHbKmb5kGahTc9GaM>（私有，要登录）。这里三份 `.dc.html` 和 `canvas.json` 与线上逐字节一致。
- **原型不是成品**：数据是模拟的，出口 IP 已打码；成品按它做出来后又有调整，以 [新版/README.md](../新版/README.md) 为准。
