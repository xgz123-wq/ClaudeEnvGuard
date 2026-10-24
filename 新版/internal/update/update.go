// Package update 给桌面「Claude-三端更新」脚本套一层界面：调用 更新三端Claude.ps1，只读它写的记录文件。
// 脚本仍是唯一执行更新的地方，本包不下载、不安装、不改防护；去掉本包，卫士其余功能不受影响。
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ClaudeEnvGuard/internal/logx"
	"ClaudeEnvGuard/internal/platform"
)

// Product 是三端之一的版本。
type Product struct {
	Key       string `json:"key"` // desktop / cli / plugin
	Name      string `json:"name"`
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	Behind    bool   `json:"behind"`
}

// Target 是一个受保护程序的指纹状态：unchanged / changed_signed / changed_unsigned / moved / missing。
type Target struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// View 是更新页的完整状态。界面只按 Phase 选样式、照字段显示，不自己判断。
//
// Phase：never 从没查过 / checking 正在查 / checkfail 查失败 / latest 已是最新 / update 有更新 /
// blocked 要先处理 / unsafe 指纹异常 / running 更新中 / done 完成 / failed 失败（Tier 区分重试或交给 AI）。
type View struct {
	Phase      string        `json:"phase"`
	Title      string        `json:"title"`
	Line       string        `json:"line"`
	Hint       string        `json:"hint"`
	Tier       string        `json:"tier,omitempty"`
	Products   []Product     `json:"products"`
	Targets    []Target      `json:"targets,omitempty"` // 4 个受保护程序的指纹状态（查版本时）
	Steps      []Step        `json:"steps"`
	Blockers   []Blocker     `json:"blockers,omitempty"`
	Download   *Download     `json:"download,omitempty"`
	NeedsUAC   bool          `json:"needsUac"`
	Elapsed    float64       `json:"elapsed"`
	Remaining  float64       `json:"remaining"`
	Percent    float64       `json:"percent"`
	Estimate   float64       `json:"estimate"` // 开始前：预计总秒数
	Error      string        `json:"error,omitempty"`
	FailedStep string        `json:"failedStep,omitempty"`
	RunDir     string        `json:"runDir,omitempty"`
	AIText     string        `json:"aiText,omitempty"`
	CheckedAt  string        `json:"checkedAt,omitempty"`
	CheckSecs  float64       `json:"checkSecs,omitempty"`
	History    []HistoryItem `json:"history"`
	Script     string        `json:"script"`
}

// Runner 管一次查版本或一次更新。界面和命令行各持有一个。
type Runner struct {
	Sys      platform.OS
	Script   string // 更新三端Claude.ps1
	Pwsh     string
	Root     string // %LOCALAPPDATA%\ClaudeNetworkGuard，脚本写记录的地方
	CheckDir string // 卫士自己的查版本记录目录（每次覆盖）
	// ChangedOnly 让 UAC 内的日常总检只查这次动过的项（需求文档第 3 节）。
	ChangedOnly bool
	// UseScript 让查版本改回调用脚本 -Action Check（v2.3.0 起默认用 Go 查，见 gocheck.go）；对照和回退用。
	UseScript bool
	// Proxy 是查版本走的代理，和脚本一样只经 Desktop 专用端口。
	Proxy        string
	Src          Sources
	CheckTimeout time.Duration
	Now          func() time.Time

	mu    sync.Mutex
	view  View
	check *scriptRecord
	sit   situation
	plan  []Step
	run   *runState
}

type runState struct {
	dir    string
	obs    *observer
	start  time.Time
	done   bool
	output string
	err    error
}

// scriptRel 是更新脚本在项目目录里的相对位置。
var scriptRel = filepath.Join("docs", "配置", "Claude环境", "Desktop防护", "更新三端Claude.ps1")

// New 按本机目录建 Runner。shareRoot 是设置里的分享包目录（可为空）。
// 脚本按 桌面\AI应用开发 → 分享包目录 → 桌面\Claude环境分享版 的顺序找第一个存在的；
// 本机的活动源是 AI应用开发，分享包里的副本只给别的电脑用。
func New(sys platform.OS, shareRoot string) *Runner {
	p := sys.Paths()
	pwsh := filepath.Join(p.ProgramFiles, "PowerShell", "7", "pwsh.exe")
	if _, err := os.Stat(pwsh); err != nil {
		pwsh = "pwsh.exe"
	}
	roots := []string{filepath.Join(p.Desktop, "AI应用开发")}
	if shareRoot != "" {
		roots = append(roots, shareRoot)
	}
	roots = append(roots, filepath.Join(p.Desktop, "Claude环境分享版"))
	script := filepath.Join(roots[0], scriptRel)
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, scriptRel)); err == nil {
			script = filepath.Join(root, scriptRel)
			break
		}
	}
	r := &Runner{
		Sys:          sys,
		Script:       script,
		Pwsh:         pwsh,
		Root:         filepath.Join(p.LocalAppData, "ClaudeNetworkGuard"),
		CheckDir:     filepath.Join(p.LocalAppData, "ClaudeEnvGuard", "更新检查"),
		ChangedOnly:  true,
		Proxy:        "http://127.0.0.1:43867",
		Src:          DefaultSources,
		CheckTimeout: 2 * time.Minute,
		Now:          time.Now,
	}
	r.view = View{Phase: "never", Title: "还没查过新版本", Line: "打开卫士、体检完成后会自动查一次", Script: r.Script}
	return r
}

func (r *Runner) args(extra ...string) []string {
	return append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", r.Script}, extra...)
}

// Busy 表示正在查版本或正在更新。
func (r *Runner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.view.Phase == "checking" || r.view.Phase == "running"
}

// Running 表示正在更新（关窗前提醒用）。
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.run != nil && !r.run.done
}

// Check 跑一次 -Action Check（只读）并更新状态。正在更新时直接返回当前状态。
func (r *Runner) Check(ctx context.Context) View {
	r.mu.Lock()
	if r.view.Phase == "checking" || (r.run != nil && !r.run.done) {
		v := r.view
		r.mu.Unlock()
		return v
	}
	r.run = nil
	line := "三个版本源同时查，约 2 秒"
	if r.UseScript {
		line = "调用三端更新脚本查，约 3 秒"
	}
	r.view = View{Phase: "checking", Title: "正在查三端版本…", Line: line, Products: r.view.Products, History: r.view.History, Script: r.Script}
	r.mu.Unlock()

	v := r.doCheck(ctx)
	r.mu.Lock()
	r.view = v
	r.mu.Unlock()
	return v
}

func (r *Runner) doCheck(ctx context.Context) View {
	dirs := runDirs(r.Root)
	v := View{Script: r.Script, History: history(dirs, 5)}
	start := r.Now()
	if _, err := os.Stat(r.Script); err != nil {
		v.Phase, v.Tier = "checkfail", TierAI
		v.Title = "找不到三端更新脚本"
		v.Line = "应在 " + r.Script + "；桌面「Claude-三端更新」图标指向它"
		v.Error = err.Error()
		v.AIText = aiText("查三端版本", "找不到更新脚本："+r.Script, "", "", r.Script)
		return v
	}
	if err := os.MkdirAll(r.CheckDir, 0o755); err != nil {
		return checkFail(v, "建不了查版本记录目录："+err.Error(), r.Script)
	}
	recPath := filepath.Join(r.CheckDir, "检查记录.json")
	_ = os.Remove(recPath) // 防止读到上一次的结果
	cctx, cancel := context.WithTimeout(ctx, r.CheckTimeout)
	defer cancel()
	var rec scriptRecord
	if r.UseScript {
		out, code, err := r.Sys.Run(cctx, r.Pwsh, r.args("-Action", "Check", "-RunDirectory", r.CheckDir)...)
		if rerr := readJSON(recPath, &rec); rerr != nil {
			reason := "脚本没有写出检查记录"
			switch {
			case err != nil:
				reason = "启动脚本失败：" + err.Error()
			case cctx.Err() != nil:
				reason = "查版本超时（" + r.CheckTimeout.String() + "）"
			default:
				reason += fmt.Sprintf("（退出码 %d）：%s", code, tail(out, 300))
			}
			v.CheckSecs = round1(r.Now().Sub(start).Seconds())
			v.CheckedAt = r.Now().Format("15:04")
			logx.Printf("更新：查版本失败：%s", reason)
			return checkFail(v, reason, r.Script)
		}
	} else {
		rec = *r.goCheck(cctx)
		// 和脚本一样留一份 检查记录.json，排查时两种来源看同一个文件
		if b, err := json.MarshalIndent(rec, "", "  "); err == nil {
			_ = os.WriteFile(recPath, b, 0o644)
		}
	}
	v.CheckSecs = round1(r.Now().Sub(start).Seconds())
	v.CheckedAt = r.Now().Format("15:04")
	if rec.Status != "checked" {
		logx.Printf("更新：查版本失败：%s", rec.Error)
		return checkFail(v, rec.Error, r.Script)
	}
	sit := readSituation(&rec)
	sit.PluginAuto = sit.Plugin && r.pluginAuto()
	v.Products = products(&rec)
	for _, t := range rec.Targets {
		v.Targets = append(v.Targets, Target{Name: t.Name, State: t.State})
	}
	est := estimates(dirs)
	if t := rec.Timing.Values["查版本"]; t > 0 {
		est["check"] = t
	}
	plan := buildPlan(sit, est, downloadTotal(r.Root), r.ChangedOnly)
	v.Steps, v.NeedsUAC = plan, sit.NeedsUAC()
	for _, s := range plan {
		v.Estimate += s.Est
	}
	v.Estimate = round1(v.Estimate)
	v.Steps[0].State, v.Steps[0].Secs = "done", round1(rec.Timing.Values["查版本"])

	r.mu.Lock()
	r.check, r.sit, r.plan = &rec, sit, plan
	r.mu.Unlock()
	logx.Printf("更新：查版本 %.1f 秒：%s", v.CheckSecs, versionLine(v.Products))

	switch {
	case len(sit.Unsafe) > 0:
		v.Phase, v.Tier = "unsafe", TierAI
		v.Title = "受保护的程序有异常，先别更新"
		v.Line = strings.Join(sit.Unsafe, "；")
		v.Hint = "卫士不绕过安全检查；AI 只读排查，要改会先问你"
		v.Error = v.Line
		v.AIText = aiText("查三端版本（4 个程序指纹）", v.Line, "", "", r.Script)
	case !sit.Any():
		v.Phase = "latest"
		v.Title = "三端都是最新"
		v.Line = "防护不用动。" + lastUpdateText(dirs)
		v.Steps = v.Steps[:1]
	default:
		var blockers []Blocker
		if procs := r.claudeProcs(); len(procs) > 0 {
			blockers = append(blockers, runningBlocker(procs))
		}
		if sit.Plugin {
			p := rec.Products[2]
			if !sit.PluginAuto {
				blockers = append(blockers, pluginBlocker(p.Installed, p.Latest))
			} else if n := r.vscodeCount(); n > 0 {
				blockers = append(blockers, vscodeBlocker(p.Installed, p.Latest, n))
			}
		}
		if len(blockers) > 0 {
			v.Phase, v.Blockers = "blocked", blockers
			v.Title = fmt.Sprintf("还差 %d 件事要你先做", len(blockers))
			v.Line = "做完点右边，卫士会重新检查并接着走"
			v.Hint = "本次没有下载、没有改任何文件"
			break
		}
		v.Phase = "update"
		v.Title, v.Line = updateTitle(sit, v.Products, v.Estimate)
		if sit.NeedsUAC() {
			v.Hint = "会弹一次 UAC，那就是确认"
		} else {
			v.Hint = "不用弹 UAC"
		}
	}
	return v
}

func checkFail(v View, reason, script string) View {
	v.Phase = "checkfail"
	v.Tier = classify(reason)
	if v.Tier == TierUser {
		v.Tier = TierRetry
	}
	v.Title = "没查到新版本"
	v.Line = firstClause(reason)
	v.Error = reason
	if v.Tier == TierRetry {
		v.Hint = "多半是网络问题；连续失败再交给 AI"
	} else {
		v.Hint = "这类问题卫士不自己绕过；AI 只读排查"
		v.AIText = aiText("查三端版本", reason, "", "", script)
	}
	return v
}

func products(rec *scriptRecord) []Product {
	keys := []string{"desktop", "cli", "plugin"}
	names := []string{"Claude Desktop", "Claude Code CLI", "VS Code 插件"}
	var out []Product
	for i, p := range rec.Products {
		if i >= 3 {
			break
		}
		out = append(out, Product{Key: keys[i], Name: names[i], Installed: p.Installed, Latest: p.Latest, Behind: p.Status == "update_available"})
	}
	return out
}

func versionLine(ps []Product) string {
	var parts []string
	for _, p := range ps {
		s := p.Name + " " + p.Installed
		if p.Behind {
			s += " → " + p.Latest
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "；")
}

func updateTitle(s situation, ps []Product, est float64) (string, string) {
	behind := 0
	var parts []string
	for _, p := range ps {
		if p.Behind && (p.Key != "plugin" || s.PluginAuto) {
			behind++
			parts = append(parts, p.Name+" "+p.Installed+" → "+p.Latest)
		}
	}
	title := fmt.Sprintf("有 %d 端可以更新", behind)
	if behind == 0 {
		title = "插件已更新，防护要跟上"
		parts = append(parts, "插件换了目录，要把防护规则迁过去")
	}
	line := "会更新：" + strings.Join(parts, "、") + "；预计 " + durText(est)
	if s.Desktop {
		line += "（含 Desktop 下载）"
	}
	return title, line
}

func durText(secs float64) string {
	if secs >= 90 {
		return fmt.Sprintf("约 %d 分钟", int(secs/60+0.5))
	}
	return fmt.Sprintf("约 %d 秒", int(secs+0.5))
}

func lastUpdateText(dirs []string) string {
	for _, d := range dirs {
		var r scriptRecord
		if readJSON(filepath.Join(d, "记录.json"), &r) == nil && r.Status == "passed" {
			h := summarize(d, &r)
			return fmt.Sprintf("上次更新 %s，用时 %.1f 秒", h.At, h.Seconds)
		}
	}
	return ""
}

// downloadTotal 按上一个下载过的 Desktop 安装包估大小，没有就按 300 MB。
func downloadTotal(root string) float64 {
	d := downloadProgress(root, 0)
	return d.TotalMB
}

// pluginAuto：脚本会不会自己装插件（10-24 起的脚本用 code --install-extension）；旧脚本仍要用户在 VS Code 手点。
func (r *Runner) pluginAuto() bool {
	b, err := os.ReadFile(r.Script)
	return err == nil && strings.Contains(string(b), "--install-extension")
}

// vscodeCount 数 Code.exe 进程；读不到按 0 算，脚本开始前会再查一次。
func (r *Runner) vscodeCount() int {
	ps, err := r.Sys.Processes("Code.exe")
	if err != nil {
		return 0
	}
	n := 0
	for _, p := range ps {
		if strings.EqualFold(p.Name, "Code.exe") {
			n++
		}
	}
	return n
}

func (r *Runner) claudeProcs() []Proc {
	ps, err := r.Sys.Processes("claude.exe") // 带上进程名才读得到路径，用来区分是哪一端
	if err != nil {
		return nil
	}
	var out []Proc
	for _, p := range ps {
		if strings.EqualFold(p.Name, "claude.exe") {
			out = append(out, Proc{PID: p.PID, Path: p.Exe, Kind: procKind(p.Exe)})
		}
	}
	return out
}

// ErrNotReady 表示还没查过版本，或者当前状态不能开始更新。
var ErrNotReady = errors.New("先查一次版本，确认有可更新的再开始")

// Start 在隐藏窗口里跑 Update（不等它结束）。之后用 View 每秒取进度。
func (r *Runner) Start() (View, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run != nil && !r.run.done {
		return r.view, nil
	}
	if r.check == nil || r.view.Phase != "update" {
		return r.view, ErrNotReady
	}
	now := r.Now()
	dir := filepath.Join(r.Root, runDirPrefix+now.Format(stampLayout))
	for i := 2; ; i++ { // 同一秒内重试时目录名不撞
		if _, err := os.Stat(dir); err != nil {
			break
		}
		dir = filepath.Join(r.Root, fmt.Sprintf("%s%s-%d", runDirPrefix, now.Format(stampLayout), i))
	}
	rs := &runState{dir: dir, obs: newObserver(now), start: now}
	r.run = rs
	plan := append([]Step(nil), r.plan...)
	for i := range plan {
		plan[i].State, plan[i].Secs = "wait", 0
	}
	r.plan = plan
	args := []string{"-RunDirectory", dir}
	if r.ChangedOnly {
		args = append(args, "-ChangedOnly")
	}
	logx.Printf("更新：开始一键更新，记录目录 %s", dir)
	go func() {
		// 不跟窗口的 context 走：UAC 里的维护一旦开始不能半路掐断。
		out, code, err := r.Sys.Run(context.Background(), r.Pwsh, r.args(args...)...)
		r.mu.Lock()
		rs.done, rs.output, rs.err = true, out, err
		r.mu.Unlock()
		logx.Printf("更新：脚本退出，退出码 %d，错误 %v", code, err)
	}()
	r.view = r.progressLocked()
	return r.view, nil
}

// Retry 是失败后「重试这一步」：先重新查一次（已完成的部分脚本会复用，例如 CLI 已装好就不再装、
// 审核过的同版本安装包直接用），确认仍有可更新的再开始；查出要先处理的事就停在那里。
func (r *Runner) Retry(ctx context.Context) (View, error) {
	if v := r.Check(ctx); v.Phase != "update" {
		return v, nil
	}
	return r.Start()
}

// View 返回当前状态；更新进行中时先按记录文件刷新进度。还没查过时带上最近几次记录，界面一打开就有内容。
func (r *Runner) View() View {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run != nil && r.view.Phase == "running" {
		r.view = r.progressLocked()
	}
	if r.view.History == nil && r.view.Phase == "never" {
		r.view.History = history(runDirs(r.Root), 5)
	}
	return r.view
}

func (r *Runner) progressLocked() View {
	rs := r.run
	now := r.Now()
	var rec *scriptRecord
	var tmp scriptRecord
	if readJSON(filepath.Join(rs.dir, "记录.json"), &tmp) == nil {
		rec = &tmp
	}
	lines := readLines(filepath.Join(rs.dir, "统一维护", "进度.log"))
	snap := infer(r.plan, rs.dir, r.Root, rec, lines, rs.obs, now, !rs.done)
	v := View{Script: r.Script, RunDir: rs.dir, Steps: snap.Steps, Download: snap.Download, NeedsUAC: r.sit.NeedsUAC(), History: r.view.History}
	v.Products = products(r.check)
	v.Elapsed = round1(now.Sub(rs.start).Seconds())
	if rs.done {
		return r.finish(v, rec, snap)
	}
	v.Phase = "running"
	v.Remaining = remaining(snap.Steps, snap.Cur, snap.Download)
	if v.Elapsed+v.Remaining > 0 {
		v.Percent = round1(v.Elapsed / (v.Elapsed + v.Remaining) * 100)
	}
	if snap.Cur >= 0 {
		v.Title = snap.Steps[snap.Cur].Name + "…"
	} else {
		v.Title = "收尾…"
	}
	v.Line = fmt.Sprintf("已用 %d 秒 · 预计还要 %d 秒（按最近几次记录估）", int(v.Elapsed), int(v.Remaining+0.5))
	v.Hint = "可以最小化，做完窗口会自己弹回来"
	return v
}

// finish 在脚本退出后按 记录.json 的 Status 给出结论。
func (r *Runner) finish(v View, rec *scriptRecord, snap snapshot) View {
	dirs := runDirs(r.Root)
	v.History = history(dirs, 5)
	if rec != nil && rec.Timing.Max() > 0 {
		v.Elapsed = round1(rec.Timing.Max())
	}
	steps := v.Steps
	markRest := func(state string) {
		for i := range steps {
			if steps[i].State == "wait" || steps[i].State == "run" {
				steps[i].State = state
			}
		}
	}
	status := ""
	if rec != nil {
		status = rec.Status
	}
	switch status {
	case "passed":
		markRest("skip")
		v.Phase, v.Percent = "done", 100
		v.Title = fmt.Sprintf("更新完成，用时 %.1f 秒", v.Elapsed)
		v.Line = "三端回读等于最新"
		if m := rec.Maintenance; m != nil {
			v.Line += fmt.Sprintf("，防护核验 %d/4 个程序、%d/8 条规则通过", m.HashCount, m.FiltersCount)
		}
		v.Hint = "记录在 " + filepath.Base(r.run.dir) + `\记录.json`
		v.Products = afterProducts(v.Products, rec)
	case "up_to_date":
		markRest("skip")
		v.Percent = 100
		if rec.CliInstall != nil {
			v.Phase = "done"
			v.Title = fmt.Sprintf("CLI 已更新到 %s，用时 %.1f 秒", rec.CliInstall.To, v.Elapsed)
			v.Line = "防护不用动：CLI 同路径更新按 Anthropic 签名放行，没弹 UAC"
			v.Products = afterProducts(v.Products, rec)
		} else {
			v.Phase = "latest"
			v.Title = "三端都是最新"
			v.Line = "防护不用动"
		}
	case "blocked_running":
		markRest("wait")
		var procs []Proc
		for _, p := range rec.RunningProcesses {
			procs = append(procs, Proc{PID: p.ProcessID, Path: p.ExecutablePath, Kind: procKind(p.ExecutablePath)})
		}
		v = blocked(v, runningBlocker(procs))
	case "blocked_vscode":
		markRest("wait")
		p := rec.Products[2]
		v = blocked(v, vscodeBlocker(p.Installed, p.Latest, len(rec.RunningProcesses)))
	case "plugin_update_needed":
		markRest("wait")
		p := rec.Products[2]
		v = blocked(v, pluginBlocker(p.Installed, p.Latest))
	case "blocked_locked":
		markRest("wait")
		v = blocked(v, lockedBlocker(rec.Error))
	default:
		v = r.failed(v, rec, snap, dirs)
	}
	v.Steps = steps
	if v.Phase != "running" {
		logx.Printf("更新：结束 %s（%s）：%s", status, v.Phase, v.Title)
	}
	return v
}

func blocked(v View, b Blocker) View {
	v.Phase, v.Blockers = "blocked", []Blocker{b}
	v.Title = "还差 1 件事要你先做"
	v.Line = "做完点右边，卫士会重新检查并接着走"
	v.Hint = "本次没有下载、没有改任何文件"
	v.Percent, v.Remaining = 0, 0
	return v
}

func (r *Runner) failed(v View, rec *scriptRecord, snap snapshot, dirs []string) View {
	reason, extra := "", ""
	switch {
	case rec == nil && r.run.err != nil:
		reason = "启动脚本失败：" + r.run.err.Error()
	case rec == nil:
		reason = "脚本没有写出记录就退出了：" + tail(r.run.output, 300)
	case rec.Status == "failed":
		reason = rec.Error
	default:
		reason = "脚本没跑完就退出了（状态 " + rec.Status + "）：" + tail(r.run.output, 300)
	}
	var m maintResult
	if readJSON(filepath.Join(r.run.dir, "统一维护", "结果.json"), &m) == nil && m.Error != "" {
		extra = m.Error
	}
	step := ""
	if snap.Cur >= 0 && snap.Cur < len(v.Steps) {
		step = v.Steps[snap.Cur].Name
		v.Steps[snap.Cur].State = "fail"
		for i := snap.Cur + 1; i < len(v.Steps); i++ {
			v.Steps[i].State = "skip"
		}
	}
	tier := classify(extra + " " + reason)
	if extra == "" {
		tier = classify(reason)
	}
	v.Error, v.FailedStep = reason, step
	if tier == TierUser {
		b := uacBlocker()
		if !strings.Contains(extra+reason, "授权") && !strings.Contains(extra+reason, "UAC") {
			b = runningBlocker(nil)
			b.Detail = "UAC 里发现 Claude 在运行，维护已停下；卫士不会替你强杀"
		}
		return blocked(v, b)
	}
	if tier == TierRetry && failStreak(dirs) >= 2 {
		tier = TierAI
	}
	v.Phase, v.Tier = "failed", tier
	if tier == TierRetry {
		v.Title = "这一步没成功，可以直接重试"
		v.Line = firstClause(reason) + "；已完成的部分不会重做"
		v.Hint = "连续失败 2 次再交给 AI"
	} else {
		v.Title = "安全检查没过，已停下"
		if aiRe.MatchString(extra+reason) == false {
			v.Title = "更新没跑完，已停下"
		}
		v.Line = firstClause(firstNonEmpty(extra, reason)) + "；防护、日志、安装包都已保留"
		v.Hint = "这类问题卫士不自己绕过；AI 只读排查，要改会先问你"
		v.AIText = aiText(step, reason, extra, r.run.dir, r.Script)
	}
	return v
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// afterProducts 用回读版本更新三张产品卡。
func afterProducts(ps []Product, rec *scriptRecord) []Product {
	out := append([]Product(nil), ps...)
	for i := range out {
		if !out[i].Behind {
			continue
		}
		switch out[i].Key {
		case "desktop":
			if rec.After != nil && rec.After.Desktop != "" {
				out[i].Installed = rec.After.Desktop
			}
		case "cli":
			if rec.CliInstall != nil {
				out[i].Installed = rec.CliInstall.To
			}
		case "plugin":
			if rec.After != nil && rec.After.Plugin != "" {
				out[i].Installed = rec.After.Plugin
			}
		}
	}
	return out
}

// History 返回最近 n 次更新记录。
func (r *Runner) History(n int) []HistoryItem { return history(runDirs(r.Root), n) }

func history(dirs []string, n int) []HistoryItem {
	out := []HistoryItem{}
	for _, d := range dirs {
		if len(out) >= n {
			break
		}
		var rec scriptRecord
		if readJSON(filepath.Join(d, "记录.json"), &rec) != nil {
			continue
		}
		out = append(out, summarize(d, &rec))
	}
	return out
}

// OpenPluginPage 打开 VS Code 的 Claude Code 插件页（只允许这一个地址）。
func (r *Runner) OpenPluginPage() error {
	explorer := filepath.Join(r.Sys.Getenv("SystemRoot"), "explorer.exe")
	if r.Sys.Getenv("SystemRoot") == "" {
		explorer = `C:\Windows\explorer.exe`
	}
	return r.Sys.StartDetached(explorer, `"vscode:extension/anthropic.claude-code"`)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}
