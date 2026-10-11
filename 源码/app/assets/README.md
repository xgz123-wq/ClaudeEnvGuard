# 图标说明

`icon.png` 为 256x256 应用图标（深蓝圆角底 + 橙色盾牌 + 白色对勾），
由项目维护者用 PIL 生成，属本开源项目资产，可自由替换。

打包时如需设置为 exe 图标，在 `packaging/build.py` 中给 PyInstaller
追加参数 `--icon app/assets/icon.png` 即可。
