package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	xwin "golang.org/x/sys/windows"

	"ClaudeEnvGuard/internal/engine"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/logx"
	"ClaudeEnvGuard/internal/platform"
	"ClaudeEnvGuard/internal/version"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	sys := platform.NewWindows()
	args := os.Args[1:]
	// 提权子进程：只执行修复或还原，不显示界面
	if len(args) > 0 && len(args[0]) > 11 && args[0][:11] == "--elevated-" {
		engine.InitLog(sys, "提权")
		logx.Printf("提权进程：%v", args)
	}
	if code, ok := fixes.HandleElevatedArgs(sys, args); ok {
		os.Exit(code)
	}
	engine.InitLog(sys, "界面")
	logx.Printf("界面启动 v%s，管理员=%v", version.Version, sys.IsAdmin())
	if len(args) > 0 {
		title, _ := xwin.UTF16PtrFromString("Claude 环境卫士")
		msg, _ := xwin.UTF16PtrFromString("图形界面不接受命令行参数。\n命令行请使用同目录下的 ceg.exe，例如：ceg check")
		xwin.MessageBox(0, msg, title, 0x40)
		os.Exit(3)
	}
	self, _ := os.Executable()
	app := NewApp(engine.New(sys, version.Version, self))

	err := wails.Run(&options.App{
		Title:            "Claude 环境卫士",
		Width:            1180,
		Height:           860,
		MinWidth:         960,
		MinHeight:        640,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 244, G: 241, B: 234, A: 1},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "ClaudeEnvGuard-7f3c2a9e-single",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if app.ctx != nil {
					runtime.WindowUnminimise(app.ctx)
					runtime.WindowShow(app.ctx)
				}
			},
		},
		Windows: &windows.Options{
			Theme:                windows.SystemDefault,
			WebviewUserDataPath:  "", // 默认在 %APPDATA%\ClaudeEnvGuard.exe
			DisableWindowIcon:    false,
			WebviewIsTransparent: false,
		},
	})
	if err != nil {
		logx.Printf("界面启动失败：%v", err)
		title, _ := xwin.UTF16PtrFromString("Claude 环境卫士")
		msg, _ := xwin.UTF16PtrFromString("界面启动失败：" + err.Error() + "\n\n可能是缺少 WebView2 运行库，可从微软官网安装 \"Microsoft Edge WebView2 Runtime\"。\n命令行版 ceg.exe 不依赖它，仍可使用。")
		xwin.MessageBox(0, msg, title, 0x10)
		os.Exit(1)
	}
}
