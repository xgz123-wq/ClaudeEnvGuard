// Package engine 把收集、判定、修复、清理串成 GUI 和命令行共用的几个入口。
package engine

import (
	"context"
	"sync"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/checks"
	"ClaudeEnvGuard/internal/cleanup"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

// NetworkBudget 是联网阶段的总时长上限。
const NetworkBudget = 14 * time.Second

// Engine 持有最近一次检查的现场，修复和清理都基于它。
type Engine struct {
	Sys     platform.OS
	Net     facts.Net
	Version string
	SelfExe string

	mu      sync.Mutex
	last    *facts.Facts
	results []model.Result
	target  model.Target
	running bool
}

// New 创建引擎。
func New(sys platform.OS, version, selfExe string) *Engine {
	return &Engine{Sys: sys, Net: facts.DefaultNet(), Version: version, SelfExe: selfExe}
}

// Settings 读设置；note 非空表示设置文件有问题已回退默认值。
func (e *Engine) Settings() (appconfig.Settings, string) {
	return appconfig.Load(e.Sys.Paths().LocalAppData)
}

// SaveSettings 保存设置。
func (e *Engine) SaveSettings(s appconfig.Settings) error {
	return appconfig.Save(e.Sys.Paths().LocalAppData, s)
}

// CheckOptions 是一次检查的参数。
type CheckOptions struct {
	Offline bool
	Browser string // 非空时临时切换到这个专用浏览器（并记住）
}

// ErrBusy 表示上一次检查还没结束。
type ErrBusy struct{}

func (ErrBusy) Error() string { return "上一次检查还没结束" }

// Check 跑一次完整检查。progress 会在本地阶段完成后收到一份未完成的报告（联网项标为检测中）。
func (e *Engine) Check(ctx context.Context, opt CheckOptions, progress func(model.Report)) (model.Report, error) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return model.Report{}, ErrBusy{}
	}
	e.running = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()

	start := time.Now()
	s, note := e.Settings()
	if opt.Browser != "" && opt.Browser != s.SelectedBrowser {
		s.SelectedBrowser = opt.Browser
		_ = e.SaveSettings(s)
	}
	f := facts.CollectLocal(ctx, e.Sys, s)
	f.Offline = opt.Offline
	var notes []string
	if note != "" {
		notes = append(notes, note)
	}
	if opt.Browser != "" && f.SelectedName() != opt.Browser {
		notes = append(notes, "没找到名为「"+opt.Browser+"」的专用浏览器，已改为检查「"+f.SelectedName()+"」。")
	}
	rep := e.build(f, start, notes)
	if progress != nil && !opt.Offline {
		progress(rep)
	}
	if !opt.Offline && ctx.Err() == nil {
		facts.CollectNetwork(ctx, e.Sys, f, e.Net, NetworkBudget)
		if ctx.Err() != nil {
			notes = append(notes, "检查被取消，联网项结果不完整。")
		}
		rep = e.build(f, start, notes)
	}
	rep.Final = true
	e.mu.Lock()
	e.last, e.results, e.target = f, rep.Results, rep.Target
	e.mu.Unlock()
	return rep, nil
}

func (e *Engine) build(f *facts.Facts, start time.Time, notes []string) model.Report {
	t := checks.ResolveTarget(f)
	results := checks.Evaluate(f, t)
	rep := model.Report{Version: e.Version, StartedAt: start, DurationMs: time.Since(start).Milliseconds(), Offline: f.Offline,
		Final: f.NetworkDone || f.Offline, SelectedBrowser: f.SelectedName(), Target: t, Results: results,
		Summary: checks.Summarize(results), Notes: notes}
	for _, b := range f.Browsers {
		bi := model.BrowserInfo{Name: b.Name, UserDataDir: b.UserDataDir, Standard: b.Standard}
		if len(b.Shortcuts) > 0 {
			bi.Shortcut = b.Shortcuts[0].Path
		}
		if !b.LastUsed.IsZero() {
			bi.LastUsed = b.LastUsed.Format("2006-01-02 15:04")
		}
		rep.Browsers = append(rep.Browsers, bi)
	}
	for _, en := range []model.Entry{model.EntryBrowser, model.EntryCLI, model.EntryDesktop} {
		if ex := f.Exits[en]; ex != nil {
			rep.Exits = append(rep.Exits, *ex)
		} else {
			rep.Exits = append(rep.Exits, model.Exit{Entry: en})
		}
	}
	return rep
}

// HasCheck 表示是否已有可用于修复的检查结果。
func (e *Engine) HasCheck() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.last != nil
}

// Plan 基于最近一次检查生成修复计划。
func (e *Engine) Plan(opt fixes.Options) model.FixPlan {
	e.mu.Lock()
	f, rs, t := e.last, e.results, e.target
	e.mu.Unlock()
	if f == nil {
		return model.FixPlan{Notes: []string{"请先检查一次。"}}
	}
	return fixes.Plan(f, t, rs, opt)
}

// Apply 执行选中的修复。
func (e *Engine) Apply(ids []string, opt fixes.Options) model.ApplyResult {
	plan := e.Plan(opt)
	return fixes.Apply(e.Sys, plan, ids, e.SelfExe)
}

// Backups 列出可还原的修复。
func (e *Engine) Backups() []model.BackupInfo {
	return fixes.ListBackups(e.Sys.Paths().LocalAppData)
}

// Restore 还原某次修复。
func (e *Engine) Restore(id string) model.ApplyResult {
	return fixes.Restore(e.Sys, id, e.SelfExe)
}

// localFacts 清理前需要最新的进程和浏览器状态；没检查过就现场读一次本地事实。
func (e *Engine) localFacts() *facts.Facts {
	s, _ := e.Settings()
	return facts.CollectLocal(context.Background(), e.Sys, s)
}

// CleanPlan 列出清理清单。
func (e *Engine) CleanPlan(kind string) model.CleanPlan {
	return cleanup.Plan(e.Sys, kind, e.localFacts())
}

// Clean 执行清理（调用方负责先展示清单并取得确认）。confirmed 非空时只删用户看过的那些路径。
func (e *Engine) Clean(kind string, confirmed []string) model.CleanResult {
	f := e.localFacts()
	plan := cleanup.Plan(e.Sys, kind, f)
	if len(confirmed) > 0 {
		ok := map[string]bool{}
		for _, p := range confirmed {
			ok[p] = true
		}
		var keep []model.CleanItem
		for _, it := range plan.Items {
			if ok[it.Path] {
				keep = append(keep, it)
			}
		}
		plan.Items = keep
	}
	return cleanup.Execute(e.Sys, plan, f)
}
