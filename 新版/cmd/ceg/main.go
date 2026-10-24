// ceg 是 Claude 环境卫士的命令行版，和图形界面共用同一套检查、修复、清理逻辑。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"ClaudeEnvGuard/internal/cleanup"
	"ClaudeEnvGuard/internal/engine"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/logx"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
	"ClaudeEnvGuard/internal/report"
	"ClaudeEnvGuard/internal/update"
	"ClaudeEnvGuard/internal/version"
)

const usage = `Claude 环境卫士（命令行版） v%s

用法：
  ceg check   [--browser 名字] [--offline] [--json] [--verbose]
      全量检查。--offline 跳过联网项；--verbose 通过的项也显示原因。
  ceg fix     --plan [--json] [--alias 名字]
      只列出修复计划，不改任何东西。
  ceg fix     --apply --items default|全部ID用逗号分隔 [--alias 名字]
      执行修复（先备份）。default = 计划里默认勾选的项。需要管理员的项会弹一次 UAC。
  ceg restore [--list] [--id 备份编号 [--items 动作id,...]]
      还原某次修复；不带 --id 时还原最近一次；--items 只还原其中几项（动作 id 见 --list）。
  ceg records [--json]
      只读：列出「记录」页的修复、还原、更新、清理。
  ceg clean   code|desktop|browser [--browser 名字] --plan|--yes [--json]
      清理登录数据。先用 --plan 看清单，确认后再用 --yes 执行。
  ceg update  --check [--json]
      只读：调用桌面「Claude-三端更新」脚本的 -Action Check，列出三端版本、4 个程序指纹和这次会做什么。
  ceg update  --apply
      一键更新（同桌面「Claude-三端更新」）：先退出全部 Claude；要动防护时弹一次 UAC。
  ceg version

退出码：0 全部通过；1 有警告或未知；2 有失败；3 工具自身出错。
update：0 已是最新或更新完成；1 有可更新的或要先处理；2 失败或安全检查没过；3 工具自身出错。
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	sys := platform.NewWindows()
	if len(args) > 0 && strings.HasPrefix(args[0], "--elevated-") {
		engine.InitLog(sys, "提权")
	} else {
		engine.InitLog(sys, "命令行")
	}
	logx.Printf("命令：%s", strings.Join(args, " "))
	if code, ok := fixes.HandleElevatedArgs(sys, args); ok {
		return code
	}
	setupConsole()
	st := report.Style{Color: colorEnabled()}
	self, _ := os.Executable()
	eng := engine.New(sys, version.Version, self)
	if len(args) == 0 {
		fmt.Fprintf(stdout, usage, version.Version)
		return 0
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "check":
		return cmdCheck(eng, rest, stdout, stderr, st)
	case "fix":
		return cmdFix(eng, rest, stdout, stderr, st)
	case "restore":
		return cmdRestore(eng, rest, stdout, stderr, st)
	case "records":
		return cmdRecords(eng, rest, stdout, stderr)
	case "clean":
		return cmdClean(eng, rest, stdout, stderr, st)
	case "update":
		return cmdUpdate(sys, eng, rest, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.Version)
		return 0
	case "help", "--help", "-h", "/?":
		fmt.Fprintf(stdout, usage, version.Version)
		return 0
	}
	fmt.Fprintf(stderr, "不认识的命令：%s\n\n", cmd)
	fmt.Fprintf(stderr, usage, version.Version)
	return 3
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func exitCode(s model.Status) int {
	switch s {
	case model.Fail:
		return 2
	case model.Warn, model.Unknown:
		return 1
	}
	return 0
}

func checkCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func cmdCheck(eng *engine.Engine, args []string, stdout, stderr io.Writer, st report.Style) int {
	fs := newFlags("check", stderr)
	browser := fs.String("browser", "", "要检查的专用浏览器名字")
	offline := fs.Bool("offline", false, "跳过联网检测")
	asJSON := fs.Bool("json", false, "输出 JSON")
	verbose := fs.Bool("verbose", false, "通过的项也显示原因")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	ctx, cancel := checkCtx()
	defer cancel()
	if !*asJSON && !*offline {
		fmt.Fprintln(stderr, "正在检查（联网项最多约 15 秒）…")
	}
	rep, err := eng.Check(ctx, engine.CheckOptions{Offline: *offline, Browser: *browser}, nil)
	if err != nil {
		fmt.Fprintln(stderr, "检查失败：", err)
		return 3
	}
	if *asJSON {
		printJSON(stdout, rep)
	} else {
		fmt.Fprint(stdout, report.Report(rep, st, *verbose))
	}
	return exitCode(rep.Summary.Overall)
}

func cmdFix(eng *engine.Engine, args []string, stdout, stderr io.Writer, st report.Style) int {
	fs := newFlags("fix", stderr)
	plan := fs.Bool("plan", false, "只列计划")
	apply := fs.Bool("apply", false, "执行修复")
	items := fs.String("items", "", "要执行的修复 ID，逗号分隔；default 表示默认勾选的项")
	alias := fs.String("alias", "", "新建专用浏览器时的名字")
	browser := fs.String("browser", "", "针对哪个专用浏览器")
	offline := fs.Bool("offline", false, "跳过联网检测（随 IP 的项将无法生成）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	if *plan == *apply {
		fmt.Fprintln(stderr, "请指定 --plan（只看计划）或 --apply（执行），二选一。")
		return 3
	}
	if *apply && *items == "" {
		fmt.Fprintln(stderr, "执行修复需要 --items：先用 ceg fix --plan 看计划，再用 --items default 或指定 ID。")
		return 3
	}
	ctx, cancel := checkCtx()
	defer cancel()
	if !*asJSON {
		fmt.Fprintln(stderr, "先检查现场…")
	}
	if _, err := eng.Check(ctx, engine.CheckOptions{Offline: *offline, Browser: *browser}, nil); err != nil {
		fmt.Fprintln(stderr, "检查失败：", err)
		return 3
	}
	opt := fixes.Options{Alias: *alias}
	p := eng.Plan(opt)
	if *plan {
		if *asJSON {
			printJSON(stdout, p)
		} else {
			fmt.Fprint(stdout, report.Plan(p, st))
		}
		return 0
	}
	var ids []string
	if *items == "default" {
		for _, a := range p.Actions {
			if a.DefaultOn {
				ids = append(ids, a.ID)
			}
		}
	} else {
		known := map[string]bool{}
		for _, a := range p.Actions {
			known[a.ID] = true
		}
		for _, id := range strings.Split(*items, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if !known[id] {
				fmt.Fprintf(stderr, "计划里没有 %s（可能已经不需要修复），跳过。\n", id)
				continue
			}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		fmt.Fprintln(stdout, "没有要执行的修复项。")
		return 0
	}
	res := eng.Apply(ids, opt)
	if *asJSON {
		printJSON(stdout, res)
	} else {
		fmt.Fprint(stdout, report.Apply(res, st))
	}
	if !res.OK {
		return 2
	}
	return 0
}

func cmdRestore(eng *engine.Engine, args []string, stdout, stderr io.Writer, st report.Style) int {
	fs := newFlags("restore", stderr)
	list := fs.Bool("list", false, "列出备份")
	id := fs.String("id", "", "备份编号")
	itemsArg := fs.String("items", "", "只还原这些动作，逗号分隔（动作 id 见 --list）；不写 = 整次还原")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	var items []string
	for _, it := range strings.Split(*itemsArg, ",") {
		if it = strings.TrimSpace(it); it != "" {
			items = append(items, it)
		}
	}
	if len(items) > 0 && *id == "" {
		fmt.Fprintln(stderr, "按项还原要同时写 --id 备份编号")
		return 3
	}
	backups := eng.Backups()
	if *list {
		if *asJSON {
			printJSON(stdout, backups)
			return 0
		}
		if len(backups) == 0 {
			fmt.Fprintln(stdout, "还没有修复备份。")
		}
		for _, b := range backups {
			mark := ""
			if b.Restored {
				mark = "（已还原）"
			}
			fmt.Fprintf(stdout, "%s  %s%s\n", b.ID, b.CreatedAt, mark)
			for _, it := range b.Items {
				state := "可还原"
				switch {
				case !it.OK:
					state = "没改成"
				case it.RestoredAt != "":
					state = "已还原 " + it.RestoredAt
				}
				fmt.Fprintf(stdout, "    %-22s %s（%s）\n", it.ID, it.Title, state)
			}
		}
		return 0
	}
	target := *id
	if target == "" {
		for _, b := range backups {
			if !b.Restored {
				target = b.ID
				break
			}
		}
	}
	if target == "" {
		fmt.Fprintln(stdout, "没有可还原的修复。")
		return 0
	}
	res := eng.RestoreItems(target, items)
	if *asJSON {
		printJSON(stdout, res)
	} else {
		fmt.Fprintf(stdout, "还原 %s：\n%s", target, report.Apply(res, st))
	}
	if !res.OK {
		return 2
	}
	return 0
}

// cmdRecords 列出「记录」页的条目（只读）。
func cmdRecords(eng *engine.Engine, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("records", stderr)
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	rs := eng.Records()
	if *asJSON {
		printJSON(stdout, rs)
		return 0
	}
	if len(rs) == 0 {
		fmt.Fprintln(stdout, "还没有记录。")
	}
	kinds := map[string]string{"fix": "修复", "restore": "还原", "update": "更新", "clean": "清理"}
	for _, r := range rs {
		fmt.Fprintf(stdout, "%s  %s  %s（%s）\n", r.At, kinds[r.Kind], r.Title, r.State)
		for _, d := range r.Diffs {
			fmt.Fprintf(stdout, "    %s：%s → %s\n", d.What, d.From, d.To)
		}
	}
	return 0
}

func cmdClean(eng *engine.Engine, args []string, stdout, stderr io.Writer, st report.Style) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "请指定清理对象：code / desktop / browser")
		return 3
	}
	kind := args[0]
	if kind != cleanup.KindCode && kind != cleanup.KindDesktop && kind != cleanup.KindBrowser {
		fmt.Fprintln(stderr, "清理对象只能是 code / desktop / browser")
		return 3
	}
	fs := newFlags("clean", stderr)
	plan := fs.Bool("plan", false, "只列清单")
	yes := fs.Bool("yes", false, "确认执行")
	browser := fs.String("browser", "", "专用浏览器名字（clean browser 时）")
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 3
	}
	if *plan == *yes {
		fmt.Fprintln(stderr, "请指定 --plan（只看清单）或 --yes（确认执行），二选一。")
		return 3
	}
	if *browser != "" {
		s, _ := eng.Settings()
		s.SelectedBrowser = *browser
		_ = eng.SaveSettings(s)
	}
	if *plan {
		p := eng.CleanPlan(kind)
		if *asJSON {
			printJSON(stdout, p)
		} else {
			fmt.Fprint(stdout, report.Clean(p, st))
			if p.Blocked == "" && len(p.Items) > 0 {
				fmt.Fprintf(stdout, "确认无误后执行：ceg clean %s --yes\n", kind)
			}
		}
		return 0
	}
	res := eng.Clean(kind, nil)
	if *asJSON {
		printJSON(stdout, res)
	} else {
		for _, r := range res.Removed {
			fmt.Fprintln(stdout, "已删除：", r)
		}
		for _, e := range res.Errors {
			fmt.Fprintln(stdout, "失败：", e)
		}
		fmt.Fprintln(stdout, res.Message)
	}
	if !res.OK {
		return 2
	}
	return 0
}

func cmdUpdate(sys platform.OS, eng *engine.Engine, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("update", stderr)
	check := fs.Bool("check", false, "只查版本（只读）")
	apply := fs.Bool("apply", false, "一键更新")
	asJSON := fs.Bool("json", false, "输出 JSON（只用于 --check）")
	if err := fs.Parse(args); err != nil {
		return 3
	}
	if *check == *apply {
		fmt.Fprintln(stderr, "请指定 --check（只读查版本）或 --apply（一键更新），二选一。")
		return 3
	}
	s, _ := eng.Settings()
	r := update.New(sys, s.SharePackageRoot)
	ctx, cancel := checkCtx()
	defer cancel()
	if !*asJSON {
		fmt.Fprintln(stderr, "正在查三端版本（三个版本源同时查，约 3 秒）…")
	}
	v := r.Check(ctx)
	if *check {
		if *asJSON {
			printJSON(stdout, v)
		} else {
			printUpdateView(stdout, v)
		}
		return updateExit(v)
	}
	printUpdateView(stdout, v)
	if v.Phase != "update" {
		return updateExit(v)
	}
	fmt.Fprintln(stdout, "\n开始一键更新（脚本在隐藏窗口里跑；要动防护时会弹一次 UAC，那就是确认）…")
	v, err := r.Start()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 3
	}
	fmt.Fprintln(stdout, "记录目录：", v.RunDir)
	// 每秒读一次记录文件；步骤变了或每 10 秒打一行。Ctrl+C 只停止显示，不会掐断脚本（UAC 里的维护不能半路停）。
	last, tick := "", 0
	for v.Phase == "running" {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stdout, "\n已停止显示；更新仍在后台继续，结果看记录目录里的 记录.json。")
			return 1
		case <-time.After(time.Second):
		}
		v = r.View()
		tick++
		if v.Phase == "running" && (v.Title != last || tick%10 == 0) {
			last = v.Title
			line := fmt.Sprintf("  [%3.0f%%] %s  %s", v.Percent, v.Title, v.Line)
			if v.Download != nil {
				line += fmt.Sprintf("（已下载 %.0f / 约 %.0f MB，%.1f MB/s）", v.Download.MB, v.Download.TotalMB, v.Download.Speed)
			}
			fmt.Fprintln(stdout, line)
		}
	}
	fmt.Fprintln(stdout)
	printUpdateView(stdout, v)
	return updateExit(v)
}

func updateExit(v update.View) int {
	switch v.Phase {
	case "latest", "done":
		return 0
	case "update", "blocked":
		return 1
	}
	return 2
}

func printUpdateView(w io.Writer, v update.View) {
	if len(v.Products) > 0 {
		head := "三端版本"
		if v.CheckSecs > 0 {
			head += fmt.Sprintf("（查版本 %.1f 秒）", v.CheckSecs)
		}
		fmt.Fprintln(w, head+"：")
		for _, p := range v.Products {
			mark := "最新"
			if p.Behind {
				mark = "可更新"
			}
			fmt.Fprintf(w, "  %-16s 已装 %-12s 最新 %-12s %s\n", p.Name, p.Installed, p.Latest, mark)
		}
	}
	if len(v.Targets) > 0 {
		fmt.Fprintln(w, "受保护的 4 个程序：")
		for _, t := range v.Targets {
			fmt.Fprintf(w, "  %-24s %s\n", t.Name, t.State)
		}
	}
	fmt.Fprintf(w, "结论：%s", v.Title)
	if v.Line != "" {
		fmt.Fprintf(w, " —— %s", v.Line)
	}
	fmt.Fprintln(w)
	for _, b := range v.Blockers {
		fmt.Fprintf(w, "  要先做：%s（%s）\n", b.Title, b.Detail)
		for _, p := range b.Procs {
			fmt.Fprintf(w, "    PID %d  %s  %s\n", p.PID, p.Kind, p.Path)
		}
	}
	if v.Phase == "update" {
		fmt.Fprintln(w, "这次会走：")
		for _, s := range v.Steps[1:] {
			where := ""
			if s.UAC {
				where = "（UAC 内）"
			}
			fmt.Fprintf(w, "  %-4s %s%s  约 %.0f 秒\n", update.PhaseNames[s.Phase], s.Name, where, s.Est)
		}
		fmt.Fprintln(w, "执行：ceg update --apply（先退出全部 Claude）")
	}
	if v.Hint != "" {
		fmt.Fprintln(w, "提示：", v.Hint)
	}
	if v.Phase == "done" || v.Phase == "failed" {
		for _, s := range v.Steps {
			fmt.Fprintf(w, "  %-5s %s  %.1f 秒\n", stepMark(s.State), s.Name, s.Secs)
		}
	}
	if v.Error != "" && v.Error != v.Line {
		fmt.Fprintln(w, "原因原文：", v.Error)
	}
	if v.AIText != "" {
		fmt.Fprintln(w, "\n可复制给 AI 排查：\n"+v.AIText)
	}
	if v.RunDir != "" && v.Phase != "running" {
		fmt.Fprintln(w, "记录目录：", v.RunDir)
	}
}

func stepMark(state string) string {
	return map[string]string{"done": "完成", "fail": "失败", "skip": "跳过", "wait": "没执行", "run": "进行中"}[state]
}

// setupConsole 让旧版控制台也能正确显示中文和颜色。
func setupConsole() {
	_ = windows.SetConsoleOutputCP(65001)
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) == nil {
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &mode) == nil
}
