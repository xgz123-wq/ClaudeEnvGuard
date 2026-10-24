package update

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// 出错分三层（需求文档 4.2）：
//
//	retry —— 网络断、下载不完整、超时：界面给「重试这一步」，脚本会复用已完成的部分；连续失败 2 次升到 ai。
//	user  —— Claude 在运行、插件要手点、UAC 点了「否」：列成待办，做完重新检查。
//	ai    —— 签名不对、防护核验不过、没见过的错：复制给 AI 排查，卫士不绕过安全检查。
const (
	TierRetry = "retry"
	TierUser  = "user"
	TierAI    = "ai"
)

var (
	// 安全类关键词优先：同一句里既有「下载」又有「签名」时按安全问题处理。
	aiRe    = regexp.MustCompile(`签名|指纹|哈希|SHA|核验|Verify|审核|防护|架构|IFEO|策略|不一致|不符|不唯一|拒绝登记|回读`)
	userRe  = regexp.MustCompile(`授权被取消|UAC|claude 进程在运行|Claude 正在运行|正在运行`)
	retryRe = regexp.MustCompile(`(?i)curl 退出码|下载失败|官方入口无响应|官方市场无响应|没有返回跳转|npm view 没有返回|npm 安装 CLI 失败|没有返回 win32-x64|无法连接|积极拒绝|连接|超时|timed? ?out|timeout|SSL|TLS|网络|No such host|name resolution|远程主机|actively refused|EAI_AGAIN|ECONNRESET|ETIMEDOUT`)
)

// classify 按错误原文分层。
func classify(text string) string {
	switch {
	case text == "":
		return TierAI
	case aiRe.MatchString(text):
		return TierAI
	case userRe.MatchString(text):
		return TierUser
	case retryRe.MatchString(text):
		return TierRetry
	}
	return TierAI
}

// Proc 是一个正在运行的 claude.exe。
type Proc struct {
	PID  int    `json:"pid"`
	Path string `json:"path"`
	Kind string `json:"kind"` // 命令行 / VS Code 插件 / Desktop / 其他
}

// procKind 按路径区分是哪一端的 claude.exe。
func procKind(path string) string {
	p := strings.ToLower(strings.ReplaceAll(path, "/", `\`))
	switch {
	case strings.Contains(p, `\.vscode\extensions\anthropic.claude-code`):
		return "VS Code 插件"
	case strings.Contains(p, `\windowsapps\claude_`), strings.Contains(p, `\anthropicclaude\`):
		return "Desktop"
	case strings.Contains(p, `\npm\`), strings.Contains(p, `\.local\bin\`):
		return "命令行"
	}
	return "其他"
}

// Blocker 是要用户先做的一件事。
type Blocker struct {
	Kind   string `json:"kind"` // running / vscode / plugin（旧脚本）/ uac / locked
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Procs  []Proc `json:"procs,omitempty"`
	Link   bool   `json:"link,omitempty"` // 显示「打开插件页」
}

func runningBlocker(procs []Proc) Blocker {
	counts := map[string]int{}
	var order []string
	for _, p := range procs {
		if counts[p.Kind] == 0 {
			order = append(order, p.Kind)
		}
		counts[p.Kind]++
	}
	var parts []string
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%s %d 个", k, counts[k]))
	}
	return Blocker{Kind: "running", Title: "退出 Claude",
		Detail: fmt.Sprintf("有 %d 个 claude.exe 在运行：%s；卫士不会替你强杀", len(procs), strings.Join(parts, "、")),
		Procs:  procs}
}

// vscodeBlocker：插件要装新版，VS Code 得先退出（装插件和迁移防护规则都要求它不在跑）。
func vscodeBlocker(from, to string, n int) Blocker {
	return Blocker{Kind: "vscode", Title: "退出 VS Code",
		Detail: fmt.Sprintf("插件 %s → %s 由卫士用 VS Code 命令行自动装，要先关掉全部 VS Code 窗口（现在开着 %d 个进程）；卫士不会替你强杀", from, to, n)}
}

// pluginBlocker 只给 10-24 之前的旧脚本用：那时插件要在 VS Code 里手点更新。
func pluginBlocker(from, to string) Blocker {
	return Blocker{Kind: "plugin", Title: "在 VS Code 里把插件更新到 " + to,
		Detail: "插件 " + from + " 锁了版本不自动更新，这是唯一要手点的一步；点完退出 VS Code",
		Link:   true}
}

func uacBlocker() Blocker {
	return Blocker{Kind: "uac", Title: "在 UAC 弹窗里点「是」",
		Detail: "上次点了「否」或弹窗被关掉了；没有改任何东西。重新检查后再点一键更新"}
}

func lockedBlocker(msg string) Blocker {
	if msg == "" {
		msg = "另一份三端更新正在运行"
	}
	return Blocker{Kind: "locked", Title: "等另一份更新跑完", Detail: msg}
}

// aiText 是「复制给 AI 排查」的内容：出错步骤、原因原文、记录目录，外加一句别急着改。
func aiText(step, reason, extra, dir, script string) string {
	var b strings.Builder
	b.WriteString("环境卫士「更新」页出错，请帮我排查。先别改任何东西，列出可能原因和每条的验证方式（只读命令）。\n")
	if step != "" {
		b.WriteString("出错步骤：" + step + "\n")
	}
	b.WriteString("原因原文：" + reason + "\n")
	if extra != "" && extra != reason {
		b.WriteString("UAC 内的原因：" + extra + "\n")
	}
	if dir != "" {
		b.WriteString("记录目录：" + dir + "（记录.json、统一维护\\结果.json、各步 .log）\n")
	}
	if script != "" {
		b.WriteString("更新脚本：" + script + "；维护约定见同目录 更新维护说明.md 和 claude-maintenance Skill\n")
	}
	return b.String()
}

// historyItem 是「最近几次」的一行。
type HistoryItem struct {
	At      string  `json:"at"` // 10-24 14:27
	Status  string  `json:"status"`
	Tone    string  `json:"tone"` // pass / info / fail
	What    string  `json:"what"`
	Seconds float64 `json:"seconds"`
	Dir     string  `json:"dir"`
}

func shortVer(v string) string {
	// Desktop 四段版本只显示前两段，和原型一致：3.6556.2.0 → 3.6556
	if parts := strings.Split(v, "."); len(parts) == 4 {
		return parts[0] + "." + parts[1]
	}
	return v
}

// summarize 把一次 Update 记录压成一行。
func summarize(dir string, r *scriptRecord) HistoryItem {
	h := HistoryItem{Status: r.Status, Seconds: round1(r.Timing.Max()), Dir: dir}
	if t := stampOf(dir); !t.IsZero() {
		h.At = t.Format("01-02 15:04")
	}
	changes := func() string {
		var parts []string
		if r.Desktop != nil && r.Desktop.NewVersion != "" {
			parts = append(parts, "Desktop → "+shortVer(r.Desktop.NewVersion))
		}
		cli, plugin := "", ""
		if r.CliInstall != nil {
			cli = r.CliInstall.To
		}
		for _, t := range r.Targets {
			if t.State == "moved" && r.After != nil {
				plugin = r.After.Plugin
			}
		}
		switch {
		case cli != "" && cli == plugin:
			parts = append(parts, "CLI + 插件 → "+cli)
		default:
			if cli != "" {
				parts = append(parts, "CLI → "+cli)
			}
			if plugin != "" {
				parts = append(parts, "插件 → "+plugin)
			}
		}
		return strings.Join(parts, "、")
	}
	switch r.Status {
	case "passed":
		h.Tone, h.What = "pass", changes()
		if h.What == "" {
			h.What = "防护已刷新"
		}
	case "up_to_date":
		h.Tone, h.What = "pass", "已是最新"
		if c := changes(); c != "" {
			h.What = c + "（没弹 UAC）"
		}
	case "plugin_update_needed":
		h.Tone, h.What = "info", "插件落后，停下等手点"
	case "blocked_running":
		h.Tone, h.What = "info", "Claude 在运行，停下"
	case "blocked_vscode":
		h.Tone, h.What = "info", "VS Code 开着，停下"
	case "blocked_locked":
		h.Tone, h.What = "info", "另一份在跑，没执行"
	case "failed":
		h.Tone, h.What = "fail", "失败："+firstClause(r.Error)
	default:
		h.Tone, h.What = "info", "没跑完（进行中或被中断）"
	}
	return h
}

// firstClause 取错误原文的第一句，放进一行里。
func firstClause(s string) string {
	for _, sep := range []string{"；", "。", "，"} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
			break
		}
	}
	if r := []rune(s); len(r) > 28 {
		s = string(r[:28]) + "…"
	}
	return s
}

// failStreak 数最近连续几次是「网络类失败」，用来决定要不要升到交给 AI。
func failStreak(dirs []string) int {
	n := 0
	for _, d := range dirs {
		var r scriptRecord
		if readJSON(filepath.Join(d, "记录.json"), &r) != nil {
			break
		}
		if r.Status != "failed" || classify(r.Error) != TierRetry {
			break
		}
		n++
	}
	return n
}
