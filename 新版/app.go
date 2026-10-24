package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/cleanup"
	"ClaudeEnvGuard/internal/engine"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/geo"
	"ClaudeEnvGuard/internal/logx"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/report"
	"ClaudeEnvGuard/internal/update"
	"ClaudeEnvGuard/internal/version"
)

// App 是暴露给前端的方法集合。所有方法都不会 panic 到前端：出错时返回带说明的结果。
type App struct {
	ctx    context.Context
	eng    *engine.Engine
	mu     sync.Mutex
	cancel context.CancelFunc
	last   model.Report
	upd    *update.Runner
}

// NewApp 创建 App。
func NewApp(eng *engine.Engine) *App {
	s, _ := eng.Settings()
	upd := update.New(eng.Sys, s.SharePackageRoot)
	upd.Proxy = fmt.Sprintf("http://127.0.0.1:%d", s.PortDesktop) // 查版本只经 Desktop 专用端口
	return &App{eng: eng, upd: upd}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// InitState 是界面启动时需要的静态信息。
type InitState struct {
	Version  string             `json:"version"`
	Settings appconfig.Settings `json:"settings"`
	Note     string             `json:"note"`
	Regions  []geo.RegionChoice `json:"regions"`
	IsAdmin  bool               `json:"isAdmin"`
	LogDir   string             `json:"logDir"`
	Defaults appconfig.Settings `json:"defaults"`
	Paths    DataPaths          `json:"paths"`
}

// DataPaths 是设置页「数据放在哪」的各个位置。
type DataPaths struct {
	Settings string `json:"settings"`
	Backups  string `json:"backups"`
	Logs     string `json:"logs"`
	Records  string `json:"records"` // 清理留痕
	Updates  string `json:"updates"` // 三端更新脚本的 更新记录-* 所在目录
}

// Init 返回版本、设置和可选地区。
func (a *App) Init() InitState {
	s, note := a.eng.Settings()
	la := a.eng.Sys.Paths().LocalAppData
	paths := DataPaths{Settings: filepath.Join(appconfig.Dir(la), "设置.json"), Backups: fixes.BackupRoot(la), Logs: logx.Dir(),
		Records: cleanup.LogDir(la), Updates: filepath.Join(la, "ClaudeNetworkGuard")}
	return InitState{Version: version.Version, Settings: s, Note: note, Regions: geo.Choices, IsAdmin: a.eng.Sys.IsAdmin(), LogDir: logx.Dir(), Defaults: appconfig.Defaults(), Paths: paths}
}

// Check 跑一次检查。本地阶段完成后通过事件 "report" 推送一份未完成报告，最终结果作为返回值。
func (a *App) Check(offline bool, browser string) (rep model.Report) {
	defer func() {
		if r := recover(); r != nil {
			rep = model.Report{Version: version.Version, Final: true, Notes: []string{"检查时出现内部错误，请重试。"}}
		}
	}()
	logx.Printf("界面发起检查：离线=%v 浏览器=%q", offline, browser)
	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	rep, err := a.eng.Check(ctx, engine.CheckOptions{Offline: offline, Browser: browser}, func(partial model.Report) {
		runtime.EventsEmit(a.ctx, "report", partial)
	})
	if err != nil {
		return model.Report{Version: version.Version, Final: true, Notes: []string{err.Error()}}
	}
	a.mu.Lock()
	a.last = rep
	a.mu.Unlock()
	return rep
}

// CancelCheck 取消正在进行的检查。
func (a *App) CancelCheck() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

// Plan 生成修复计划。
func (a *App) Plan(alias string) model.FixPlan {
	return a.eng.Plan(fixes.Options{Alias: alias})
}

// Apply 执行选中的修复。
func (a *App) Apply(ids []string, alias string) model.ApplyResult {
	return a.eng.Apply(ids, fixes.Options{Alias: alias})
}

// Backups 列出修复备份。
func (a *App) Backups() []model.BackupInfo {
	b := a.eng.Backups()
	if b == nil {
		return []model.BackupInfo{}
	}
	return b
}

// Restore 还原一次修复。
func (a *App) Restore(id string) model.ApplyResult {
	return a.eng.Restore(id)
}

// RestoreItems 只还原一次修复里勾选的项（动作 id）；其余保持现状。
func (a *App) RestoreItems(id string, items []string) model.ApplyResult {
	if len(items) == 0 {
		return model.ApplyResult{Message: "没有勾选要还原的项。"}
	}
	return a.eng.RestoreItems(id, items)
}

// Records 是「记录」页的全部条目。
func (a *App) Records() []model.LogRecord {
	return a.eng.Records()
}

// CleanPlan 列出清理清单。
func (a *App) CleanPlan(kind string) model.CleanPlan {
	return a.eng.CleanPlan(kind)
}

// Clean 执行清理，只删用户在清单里看到的路径。
func (a *App) Clean(kind string, paths []string) model.CleanResult {
	if len(paths) == 0 {
		return model.CleanResult{Message: "没有选中要清理的项目。"}
	}
	return a.eng.Clean(kind, paths)
}

// SaveSettings 保存设置；返回空串表示成功。
func (a *App) SaveSettings(s appconfig.Settings) string {
	if err := a.eng.SaveSettings(s); err != nil {
		return err.Error()
	}
	return ""
}

// PickDir 弹出选择文件夹对话框（设置页选分享包目录）；取消时返回空串。
func (a *App) PickDir(title string) string {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
	if err != nil {
		return ""
	}
	return dir
}

// SelectBrowser 记住选中的专用浏览器。
func (a *App) SelectBrowser(name string) string {
	s, _ := a.eng.Settings()
	s.SelectedBrowser = name
	return a.SaveSettings(s)
}

// LaunchBrowser 按快捷方式参数打开专用浏览器；url 为空时打开它的默认首页。
func (a *App) LaunchBrowser(name, url string) string {
	if err := a.eng.LaunchBrowser(name, url); err != nil {
		return err.Error()
	}
	return ""
}

// OpenPath 在资源管理器里打开路径。
func (a *App) OpenPath(p string) string {
	if err := a.eng.OpenPath(p); err != nil {
		return err.Error()
	}
	return ""
}

// Log 记录前端事件和错误，便于排查界面问题。
func (a *App) Log(msg string) {
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	logx.Printf("前端：%s", msg)
}

// ReportText 返回最近一次检查的纯文本报告，便于复制给别人排查。
func (a *App) ReportText() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last.Version == "" {
		return ""
	}
	return report.Report(a.last, report.Style{}, true)
}

// ---------------------------------------------------------------- 更新页（internal/update，只调用三端更新脚本、读它写的记录）

// UpdateView 返回更新页当前状态（不跑脚本）。
func (a *App) UpdateView() update.View {
	return a.upd.View()
}

// UpdateCheck 跑一次 -Action Check（只读）。打开卫士、体检完成后界面会自动调一次。
func (a *App) UpdateCheck() update.View {
	logx.Printf("界面发起查三端版本")
	return a.upd.Check(a.ctx)
}

// UpdateStart 开始一键更新；进度通过事件 "update" 每秒推送，结束时把窗口弹回来。
func (a *App) UpdateStart() update.View {
	v, err := a.upd.Start()
	if err != nil {
		logx.Printf("更新：没有开始：%v", err)
		return v
	}
	a.watchUpdate()
	return v
}

// UpdateRetry 是失败后「重试这一步」：重新查一次再开始，已完成的部分脚本会复用。
func (a *App) UpdateRetry() update.View {
	v, err := a.upd.Retry(a.ctx)
	if err != nil {
		logx.Printf("更新：重试没有开始：%v", err)
		return v
	}
	if v.Phase == "running" {
		a.watchUpdate()
	}
	return v
}

// UpdateOpenPlugin 打开 VS Code 的 Claude Code 插件页。
func (a *App) UpdateOpenPlugin() string {
	if err := a.upd.OpenPluginPage(); err != nil {
		return err.Error()
	}
	return ""
}

// watchUpdate 每秒把进度推给界面；用后台协程推而不让界面轮询，窗口最小化时定时器会被浏览器节流。
func (a *App) watchUpdate() {
	go func() {
		for {
			time.Sleep(time.Second)
			v := a.upd.View()
			runtime.EventsEmit(a.ctx, "update", v)
			if v.Phase != "running" {
				runtime.WindowUnminimise(a.ctx)
				runtime.WindowShow(a.ctx)
				return
			}
		}
	}()
}

// beforeClose 在更新进行中关窗时先确认：脚本不会跟着窗口停下，关掉后只是看不到进度。
func (a *App) beforeClose(ctx context.Context) bool {
	if !a.upd.Running() {
		return false
	}
	res, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:    runtime.QuestionDialog,
		Title:   "更新还在进行",
		Message: "关掉窗口不会停下更新：脚本会在后台跑完，只是看不到进度。\n结果在 %LOCALAPPDATA%\\ClaudeNetworkGuard\\更新记录-<时间>\\记录.json。\n\n确定要关闭吗？",
	})
	if err != nil {
		return true
	}
	return res != "Yes" && res != "是"
}
