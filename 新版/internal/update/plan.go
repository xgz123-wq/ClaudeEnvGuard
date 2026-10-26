package update

import (
	"path/filepath"
	"sort"
	"strings"
)

// Step 是更新流程里的一步。Phase 对应界面上横向 5 步里的前 4 步（第 5 步「完成」没有子步骤）。
type Step struct {
	ID     string  `json:"id"`
	Phase  int     `json:"phase"`
	Name   string  `json:"name"`
	Detail string  `json:"detail,omitempty"`
	UAC    bool    `json:"uac,omitempty"`
	Est    float64 `json:"est"`   // 预计秒数（按历次记录估）
	State  string  `json:"state"` // wait / run / done / fail / skip
	Secs   float64 `json:"secs"`  // 已完成的实际秒数，或正在跑的已用秒数
}

// PhaseNames 是界面上横向 5 步的名字。
var PhaseNames = []string{"检查", "下载与安装", "授权", "安装与核验", "完成"}

// UAC 内各步：进度.log 和 StepSeconds 里的名字 → 步骤 ID。
var maintSteps = map[string]string{
	"插件路径迁移":  "migrate",
	"CLI指纹刷新": "refresh",
	"安装包检查":   "pkgcheck",
	"版本更新":    "install",
	"防护核验":    "verify",
	"日常总检":    "summary",
	"启动条件检查":  "launch",
	"安装包清理":   "cleanup",
}

// 没有历史记录时的预计秒数（取自 10-22 到 10-24 的记录和需求文档实测）。
var defaultEst = map[string]float64{
	"check": 3, "cli": 40, "plugin": 30, "download": 180, "inspect": 8, "hashes": 3, "uac": 7,
	"migrate": 4, "refresh": 10, "pkgcheck": 7, "install": 36, "verify": 3,
	"summary": 5, "summary-full": 18, "launch": 5, "cleanup": 1,
}

// situation 是从一次 Check 结果里读出的「这次要做什么」。
type situation struct {
	Desktop, Cli, Plugin bool // 哪几端落后
	Moved                bool // 插件已在 VS Code 更新，换了目录，防护规则要迁移
	PluginAuto           bool // 插件落后且脚本会用 VS Code 命令行自己装（10-24 起）；装完换目录，同样要迁移
	GuardChanged         bool // Desktop 或插件程序指纹变了（CLI 同路径变化按签名放行，不算）
	Unsafe               []string
	DesktopLatest        string
}

// NeedsUAC 与脚本的判断一致：Desktop 落后、防护指纹变了、插件换目录（含脚本这次自己装插件），之一才弹 UAC。
func (s situation) NeedsUAC() bool { return s.Desktop || s.GuardChanged || s.Moved || s.PluginAuto }

// Any 表示这次一键更新会做点什么。
func (s situation) Any() bool { return s.Desktop || s.Cli || s.Plugin || s.NeedsUAC() }

func readSituation(rec *scriptRecord) situation {
	var s situation
	if rec == nil {
		return s
	}
	for i, p := range rec.Products {
		behind := p.Status == "update_available"
		switch i {
		case 0:
			s.Desktop, s.DesktopLatest = behind, p.Latest
		case 1:
			s.Cli = behind
		case 2:
			s.Plugin = behind
		}
	}
	for _, t := range rec.Targets {
		switch t.State {
		case "moved":
			s.Moved = true
		case "changed_signed":
			if !strings.HasPrefix(t.Name, "Claude Code npm") {
				s.GuardChanged = true
			}
		case "changed_unsigned":
			s.Unsafe = append(s.Unsafe, t.Name+"：文件变了但没有有效的 Anthropic 签名")
		case "missing":
			s.Unsafe = append(s.Unsafe, t.Name+"：受保护的程序不存在")
		}
	}
	return s
}

// buildPlan 按这次要做的事列出步骤；不会跑的步骤不列。
func buildPlan(s situation, est map[string]float64, downloadMB float64, changedOnly bool) []Step {
	e := func(id string) float64 {
		if v, ok := est[id]; ok {
			return v
		}
		return defaultEst[id]
	}
	steps := []Step{{ID: "check", Phase: 0, Name: "查三端版本", Detail: "三个版本源同时查；Claude 在运行就停下"}}
	if s.Cli {
		steps = append(steps, Step{ID: "cli", Phase: 1, Name: "npm 安装 CLI", Detail: "只用 registry.npmjs.org"})
	}
	if s.PluginAuto {
		steps = append(steps, Step{ID: "plugin", Phase: 1, Name: "VS Code 命令行装插件", Detail: "装完核对版本锁定和平台，不对就恢复"})
	}
	if s.Desktop {
		steps = append(steps,
			Step{ID: "download", Phase: 1, Name: "下载 Desktop 官方包", Detail: "约 " + itoa(int(downloadMB+0.5)) + " MB，经 Desktop 专用端口"},
			Step{ID: "inspect", Phase: 1, Name: "审核安装包", Detail: "签名、包家族、版本、架构"})
	}
	steps = append(steps, Step{ID: "hashes", Phase: 1, Name: "登记 4 个程序指纹", Detail: "文件变了必须有 Anthropic 签名"})
	if s.NeedsUAC() {
		steps = append(steps, Step{ID: "uac", Phase: 2, Name: "等你在 UAC 弹窗点「是」", Detail: "整次只弹这一次；没看到弹窗就点任务栏上闪烁的盾牌图标"})
		if s.Moved || s.PluginAuto {
			steps = append(steps, Step{ID: "migrate", Phase: 3, UAC: true, Name: "插件规则迁移到新目录"})
		}
		steps = append(steps, Step{ID: "refresh", Phase: 3, UAC: true, Name: "CLI 指纹刷新"})
		if s.Desktop {
			steps = append(steps,
				Step{ID: "pkgcheck", Phase: 3, UAC: true, Name: "复核安装包"},
				Step{ID: "install", Phase: 3, UAC: true, Name: "安装新版 Desktop"})
		}
		steps = append(steps, Step{ID: "verify", Phase: 3, UAC: true, Name: "防护核验", Detail: "4 个程序、8 条规则"})
		if changedOnly {
			steps = append(steps, Step{ID: "summary", Phase: 3, UAC: true, Name: "只查动过的项", Detail: "Desktop 策略与直连防护；原来全量总检约 18 秒"})
		} else {
			steps = append(steps, Step{ID: "summary", Phase: 3, UAC: true, Name: "日常总检"})
		}
		steps = append(steps,
			Step{ID: "launch", Phase: 3, UAC: true, Name: "启动条件检查"},
			Step{ID: "cleanup", Phase: 3, UAC: true, Name: "清理旧安装包", Detail: "保留最新两版"})
	}
	for i := range steps {
		id := steps[i].ID
		if id == "summary" && !changedOnly {
			id = "summary-full"
		}
		steps[i].Est = e(id)
		steps[i].State = "wait"
	}
	return steps
}

// estimates 按历次 记录.json 估每步秒数：样本够 3 个取最近 5 个的中位数，否则取最近一个（流程刚改过，旧样本不准）。
// 日常总检分全量和只查动过的项两种，各用各的样本。
func estimates(dirs []string) map[string]float64 {
	samples := map[string][]float64{}
	add := func(id string, v float64) {
		if v > 0 {
			samples[id] = append(samples[id], v)
		}
	}
	for _, dir := range dirs {
		var r scriptRecord
		if readJSON(filepath.Join(dir, "记录.json"), &r) != nil {
			continue
		}
		t := r.Timing
		prev := t.Values["查版本"]
		if t.Has("CLI安装") {
			add("cli", t.Values["CLI安装"]-prev)
			prev = t.Values["CLI安装"]
		}
		if t.Has("插件安装") {
			add("plugin", t.Values["插件安装"]-prev)
			prev = t.Values["插件安装"]
		}
		if t.Has("Desktop包就绪") {
			total := t.Values["Desktop包就绪"] - prev
			if r.Desktop != nil && !r.Desktop.Reused {
				add("download", r.Desktop.DownloadSeconds)
				add("inspect", total-r.Desktop.DownloadSeconds)
			} else {
				add("inspect", total)
			}
			prev = t.Values["Desktop包就绪"]
		}
		if t.Has("指纹汇总") {
			add("hashes", t.Values["指纹汇总"]-prev)
		}
		var steps orderedSeconds
		scope := ""
		if r.Maintenance != nil {
			steps = r.Maintenance.StepSeconds
		}
		var m maintResult
		if readJSON(filepath.Join(dir, "统一维护", "结果.json"), &m) == nil {
			scope = m.SummaryScope
			if len(steps.Keys) == 0 {
				steps = m.StepSeconds
			}
		}
		sum := 0.0
		for _, k := range steps.Keys {
			v := steps.Values[k]
			sum += v
			id := maintSteps[k]
			if id == "summary" && scope != "Guard" {
				id = "summary-full"
			}
			if id != "" {
				add(id, v)
			}
		}
		if t.Has("一键更新") && t.Has("指纹汇总") {
			add("uac", t.Values["一键更新"]-t.Values["指纹汇总"]-sum)
		}
	}
	out := map[string]float64{}
	for id, xs := range samples {
		if len(xs) < 3 {
			out[id] = xs[0]
			continue
		}
		if len(xs) > 5 {
			xs = xs[:5]
		}
		ys := append([]float64(nil), xs...)
		sort.Float64s(ys)
		out[id] = ys[len(ys)/2]
	}
	return out
}

func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
