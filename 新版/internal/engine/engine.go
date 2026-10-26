// Package engine 把收集、判定、修复、清理串成 GUI 和命令行共用的几个入口。
package engine

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/checks"
	"ClaudeEnvGuard/internal/chrome"
	"ClaudeEnvGuard/internal/cleanup"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/logx"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
	"ClaudeEnvGuard/internal/records"
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

// New 创建引擎，并初始化日志。who 用于区分日志来源（界面 / 命令行）。
func New(sys platform.OS, version, selfExe string) *Engine {
	return &Engine{Sys: sys, Net: facts.DefaultNet(), Version: version, SelfExe: selfExe}
}

// InitLog 打开日志（GUI 和命令行在启动时调用一次）。
func InitLog(sys platform.OS, who string) {
	logx.Init(appconfig.Dir(sys.Paths().LocalAppData), who)
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
	logCheck(rep, f)
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
	logx.Printf("修复开始：%s", strings.Join(ids, ","))
	res := fixes.Apply(e.Sys, plan, ids, e.SelfExe)
	logOutcomes("修复", res)
	return res
}

func logOutcomes(what string, res model.ApplyResult) {
	for _, o := range res.Outcomes {
		logx.Printf("%s %s：%s", what, map[bool]string{true: "成功", false: "失败"}[o.OK], o.Message)
	}
	logx.Printf("%s结束：%s", what, res.Message)
}

func logCheck(rep model.Report, f *facts.Facts) {
	s := rep.Summary
	logx.Printf("检查完成：%s；用时 %dms；通过 %d 警告 %d 失败 %d 未知 %d；离线=%v；浏览器=%s",
		s.Headline, rep.DurationMs, s.Pass, s.Warn, s.Fail, s.Unknown, rep.Offline, rep.SelectedBrowser)
	if line := timingLine(f.Timings); line != "" {
		logx.Printf("本地收集用时：%s", line)
	}
	for _, ex := range rep.Exits {
		logx.Printf("出口 %s:%d 监听=%v IP=%s 国家=%s 可达=%s %s", ex.Entry, ex.Port, ex.Listening, ex.IP, ex.CountryCode, ex.Reach, ex.Err)
	}
	for _, r := range rep.Results {
		if r.Status == model.Fail || r.Status == model.Warn || r.Status == model.Unknown {
			logx.Printf("  %s %s %s：%s", r.Status, r.ID, r.Title, r.Detail)
		}
	}
	for name, msg := range f.Errors {
		logx.Printf("收集器异常 %s：%s", name, msg)
	}
}

// timingLine 把各收集器耗时按名字排成一行，例如「browsers 20ms；cli 1ms」。
func timingLine(ts map[string]facts.CollectorTiming) string {
	names := make([]string, 0, len(ts))
	for n := range ts {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, n+" "+ts[n].String())
	}
	return strings.Join(parts, "；")
}

// Backups 列出可还原的修复。
func (e *Engine) Backups() []model.BackupInfo {
	return fixes.ListBackups(e.Sys.Paths().LocalAppData)
}

// Restore 整次还原某次修复。
func (e *Engine) Restore(id string) model.ApplyResult {
	return e.RestoreItems(id, nil)
}

// RestoreItems 只还原某次修复里的指定动作；items 为空 = 整次。
func (e *Engine) RestoreItems(id string, items []string) model.ApplyResult {
	logx.Printf("还原开始：%s 项=%v", id, items)
	res := fixes.RestoreItems(e.Sys, id, items, e.SelfExe)
	logOutcomes("还原", res)
	return res
}

// Records 是「记录」页的全部条目：修复、还原、更新、清理。
func (e *Engine) Records() []model.LogRecord {
	la := e.Sys.Paths().LocalAppData
	return records.List(la, filepath.Join(la, "ClaudeNetworkGuard"))
}

// localFacts 清理前需要最新的进程和浏览器状态；没检查过就现场读一次本地事实。
func (e *Engine) localFacts() *facts.Facts {
	s, _ := e.Settings()
	return facts.CollectLocal(context.Background(), e.Sys, s)
}

// LaunchBrowser 按专用浏览器快捷方式里的完整参数启动它；url 非空时替换首页。
// 不直接拼 --user-data-dir 启动，避免漏掉代理、DNS、WebRTC 防护参数。
func (e *Engine) LaunchBrowser(name, url string) error {
	f := e.localFacts()
	for _, b := range f.Browsers {
		if b.Name != name {
			continue
		}
		if len(b.Shortcuts) == 0 {
			return errors.New("这个专用浏览器没有桌面快捷方式，先在检查结果里一键修复")
		}
		sc := b.Shortcuts[0]
		args := sc.Args
		if url != "" {
			fl := chrome.Parse(sc.Args)
			args = chrome.Build(sc.Args, chrome.Want{UserDataDir: b.UserDataDir, SocksPort: e.portFromArgs(fl, f), Lang: fl.Get(chrome.FlagLang),
				AcceptLang: fl.Get(chrome.FlagAcceptLang)})
			args = stripURLs(args) + " " + url
		}
		return e.Sys.StartDetached(sc.Target, args)
	}
	return errors.New("找不到专用浏览器「" + name + "」")
}

func (e *Engine) portFromArgs(fl chrome.Flags, f *facts.Facts) int {
	v := fl.Get(chrome.FlagProxyServer)
	if i := strings.LastIndex(v, ":"); i >= 0 {
		if n, err := strconv.Atoi(v[i+1:]); err == nil {
			return n
		}
	}
	return f.Settings.PortBrowser
}

func stripURLs(args string) string {
	var keep []string
	for _, t := range chrome.Tokenize(args) {
		if strings.HasPrefix(t, "-") {
			keep = append(keep, t)
		}
	}
	return strings.Join(keep, " ")
}

// OpenPath 在资源管理器里打开路径。
func (e *Engine) OpenPath(p string) error { return e.Sys.OpenPath(p) }

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
	res := cleanup.Execute(e.Sys, plan, f)
	logx.Printf("清理 %s：%s 删除=%v 失败=%v", kind, res.Message, res.Removed, res.Errors)
	return res
}
