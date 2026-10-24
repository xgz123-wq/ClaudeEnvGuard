// Package envscripts 把桌面上另外两个图标并进卫士（需求文档阶段 5）：
//
//	深度总检 —— 调 Claude环境一键总检.ps1 -ResultPath，读它写的 JSON，列出 PASS / WARN / FAIL / MANUAL；
//	检查后启动 Desktop（英文） —— 先在后台跑 启动Claude英文浏览器.ps1 -CheckOnly，全部通过才按桌面快捷方式原样启动它。
//
// 和更新页一样只调用脚本、读脚本写的结果，不重写脚本里的判定。脚本找法与 internal/update 相同：
// 桌面\AI应用开发 → 设置里的分享包目录 → 桌面\Claude环境分享版，取第一个存在的。
package envscripts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ClaudeEnvGuard/internal/jsonx"
	"ClaudeEnvGuard/internal/platform"
)

// 脚本在项目目录里的相对位置。
var (
	deepRel     = filepath.Join("docs", "配置", "Claude环境", "Claude环境一键总检.ps1")
	launcherRel = filepath.Join("docs", "配置", "Claude环境", "启动Claude英文浏览器.ps1")
)

// LauncherShortcut 是桌面上英文启动器的快捷方式名。卫士按它原样启动，关窗即退出 Claude 的行为不变。
const LauncherShortcut = "Claude-英文浏览器.lnk"

// Runner 找脚本、跑脚本。
type Runner struct {
	Sys       platform.OS
	Pwsh      string
	Deep      string // 总检脚本
	Launcher  string // 英文启动器脚本
	OutDir    string // %LOCALAPPDATA%\ClaudeEnvGuard\深度总检
	DeepLimit time.Duration
	Now       func() time.Time
}

// New 按本机目录建 Runner。
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
	find := func(rel string) string {
		for _, root := range roots {
			if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
				return filepath.Join(root, rel)
			}
		}
		return filepath.Join(roots[0], rel)
	}
	return &Runner{Sys: sys, Pwsh: pwsh, Deep: find(deepRel), Launcher: find(launcherRel),
		OutDir: filepath.Join(p.LocalAppData, "ClaudeEnvGuard", "深度总检"), DeepLimit: 3 * time.Minute, Now: time.Now}
}

func (r *Runner) args(script string, extra ...string) []string {
	return append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script}, extra...)
}

// ---------------------------------------------------------------- 深度总检

// DeepItem 是总检的一行。
type DeepItem struct {
	Status   string `json:"status"` // PASS / WARN / FAIL / MANUAL
	Item     string `json:"item"`
	Evidence string `json:"evidence"`
}

// DeepResult 是一次深度总检的结果。Error 非空表示没跑成（不是检查项失败）。
type DeepResult struct {
	At      string     `json:"at"`
	Secs    float64    `json:"secs"`
	OK      bool       `json:"ok"` // 没有 FAIL
	Pass    int        `json:"pass"`
	Warn    int        `json:"warn"`
	Fail    int        `json:"fail"`
	Manual  int        `json:"manual"`
	Items   []DeepItem `json:"items"`
	Script  string     `json:"script"`
	Path    string     `json:"path,omitempty"` // 结果文件
	Error   string     `json:"error,omitempty"`
	Summary string     `json:"summary"`
}

type deepFile struct {
	OK      bool `json:"ok"`
	Pass    int  `json:"pass"`
	Warn    int  `json:"warn"`
	Fail    int  `json:"fail"`
	Manual  int  `json:"manual"`
	Results []struct {
		Status   string `json:"Status"`
		Item     string `json:"Item"`
		Evidence string `json:"Evidence"`
	} `json:"results"`
}

// DeepCheck 跑一次总检（只读，约 15 秒）。结果文件每次覆盖。
func (r *Runner) DeepCheck(ctx context.Context) DeepResult {
	start := r.Now()
	res := DeepResult{At: start.Format("15:04"), Script: r.Deep}
	done := func() DeepResult {
		res.Secs = float64(r.Now().Sub(start).Milliseconds()/100) / 10
		return res
	}
	if _, err := os.Stat(r.Deep); err != nil {
		res.Error = "找不到总检脚本：" + r.Deep
		return done()
	}
	if err := os.MkdirAll(r.OutDir, 0o755); err != nil {
		res.Error = "建不了结果目录：" + err.Error()
		return done()
	}
	path := filepath.Join(r.OutDir, "结果.json")
	_ = os.Remove(path)
	cctx, cancel := context.WithTimeout(ctx, r.DeepLimit)
	defer cancel()
	out, code, err := r.Sys.Run(cctx, r.Pwsh, r.args(r.Deep, "-ResultPath", path)...)
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		switch {
		case err != nil:
			res.Error = "启动总检失败：" + err.Error()
		case cctx.Err() != nil:
			res.Error = "总检超时（" + r.DeepLimit.String() + "）"
		case strings.Contains(out, "ResultPath"):
			res.Error = "这份总检脚本还不支持 -ResultPath（分享包没同步到 2026-10-24 之后的版本）"
		default:
			res.Error = fmt.Sprintf("总检没有写出结果（退出码 %d）：%s", code, tail(out, 300))
		}
		return done()
	}
	var f deepFile
	if err := jsonx.Unmarshal(b, &f); err != nil {
		res.Error = "总检结果读不懂：" + err.Error()
		return done()
	}
	res.Path, res.OK, res.Pass, res.Warn, res.Fail, res.Manual = path, f.OK, f.Pass, f.Warn, f.Fail, f.Manual
	for _, it := range f.Results {
		res.Items = append(res.Items, DeepItem{Status: it.Status, Item: it.Item, Evidence: it.Evidence})
	}
	switch {
	case f.Fail > 0:
		res.Summary = fmt.Sprintf("有 %d 项失败：不建议启动 Claude", f.Fail)
	case f.Warn > 0:
		res.Summary = fmt.Sprintf("日常运行检查通过；有 %d 个防护提醒", f.Warn)
	default:
		res.Summary = "程序层检查全部通过；网页全绿后可进入日常使用"
	}
	return done()
}

// ---------------------------------------------------------------- 检查后启动 Desktop（英文）

// LaunchResult 是「检查后启动 Desktop（英文）」的结果。
type LaunchResult struct {
	OK      bool     `json:"ok"`      // 启动前检查全部通过
	Started bool     `json:"started"` // 已经启动英文启动器
	Lines   []string `json:"lines"`   // 检查输出：[PASS] … / [BLOCK] …
	Message string   `json:"message"`
	Script  string   `json:"script"`
}

var reCheckLine = regexp.MustCompile(`^\[(PASS|BLOCK)\]`)

// CheckLauncher 只跑 -CheckOnly：不启动 Claude。
func (r *Runner) CheckLauncher(ctx context.Context) LaunchResult {
	res := LaunchResult{Script: r.Launcher}
	if _, err := os.Stat(r.Launcher); err != nil {
		res.Message = "找不到英文启动器脚本：" + r.Launcher
		return res
	}
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, code, err := r.Sys.Run(cctx, r.Pwsh, r.args(r.Launcher, "-CheckOnly")...)
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); reCheckLine.MatchString(l) {
			res.Lines = append(res.Lines, l)
		}
	}
	switch {
	case err != nil:
		res.Message = "启动前检查没跑起来：" + err.Error()
	case code == 0:
		res.OK = true
		res.Message = "启动前检查全部通过"
	default:
		res.Message = "启动前检查没通过，已拒绝启动"
		for _, l := range res.Lines {
			if strings.HasPrefix(l, "[BLOCK]") {
				res.Message = strings.TrimSpace(strings.TrimPrefix(l, "[BLOCK]"))
			}
		}
	}
	return res
}

// LaunchEnglish 先 -CheckOnly，全部通过才按桌面快捷方式原样启动英文启动器（它会再查一遍、启动 Claude，
// 并留一个窗口守着：关掉那个窗口 = 退出 Claude）。没有快捷方式时直接用 pwsh 启动同一个脚本。
func (r *Runner) LaunchEnglish(ctx context.Context) LaunchResult {
	res := r.CheckLauncher(ctx)
	if !res.OK {
		return res
	}
	lnk := filepath.Join(r.Sys.Paths().Desktop, LauncherShortcut)
	var err error
	if sc, rerr := r.Sys.ReadShortcut(lnk); rerr == nil && sc.Target != "" {
		err = r.Sys.StartDetached(sc.Target, sc.Args)
	} else {
		err = r.Sys.StartDetached(r.Pwsh, `-NoProfile -File "`+r.Launcher+`"`)
	}
	if err != nil {
		res.Message = "检查通过，但启动失败：" + err.Error()
		return res
	}
	res.Started = true
	res.Message = "检查通过，已启动 Claude Desktop（英文）。关掉那个黑色窗口 = 退出 Claude"
	return res
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}
