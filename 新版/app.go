package main

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/engine"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/geo"
	"ClaudeEnvGuard/internal/logx"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/report"
	"ClaudeEnvGuard/internal/version"
)

// App 是暴露给前端的方法集合。所有方法都不会 panic 到前端：出错时返回带说明的结果。
type App struct {
	ctx    context.Context
	eng    *engine.Engine
	mu     sync.Mutex
	cancel context.CancelFunc
	last   model.Report
}

// NewApp 创建 App。
func NewApp(eng *engine.Engine) *App {
	return &App{eng: eng}
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
	Defaults appconfig.Settings `json:"defaults"`
}

// Init 返回版本、设置和可选地区。
func (a *App) Init() InitState {
	s, note := a.eng.Settings()
	return InitState{Version: version.Version, Settings: s, Note: note, Regions: geo.Choices, IsAdmin: a.eng.Sys.IsAdmin(), Defaults: appconfig.Defaults()}
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
