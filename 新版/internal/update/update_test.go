package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/platform"
	"ClaudeEnvGuard/internal/scriptroot"
)

// 测试不跑真脚本：Fake.RunFunc 按参数把脚本会写的记录文件写到磁盘上，Runner 只读这些文件。

const (
	prodLatest = `[
 {"Product":"Claude Desktop","Installed":"3.6556.2.0","Latest":"3.6556.2.0","Status":"up_to_date"},
 {"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.310","Status":"up_to_date"},
 {"Product":"VS Code Claude Code 插件","Installed":"2.1.310","Latest":"2.1.310","Status":"up_to_date"}]`
	prodCli = `[
 {"Product":"Claude Desktop","Installed":"3.6556.2.0","Latest":"3.6556.2.0","Status":"up_to_date"},
 {"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.311","Status":"update_available"},
 {"Product":"VS Code Claude Code 插件","Installed":"2.1.310","Latest":"2.1.310","Status":"up_to_date"}]`
	prodDesktop = `[
 {"Product":"Claude Desktop","Installed":"3.6556.2.0","Latest":"3.6601.0.0","Status":"update_available"},
 {"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.311","Status":"update_available"},
 {"Product":"VS Code Claude Code 插件","Installed":"2.1.310","Latest":"2.1.310","Status":"up_to_date"}]`
	prodPlugin = `[
 {"Product":"Claude Desktop","Installed":"3.6556.2.0","Latest":"3.6556.2.0","Status":"up_to_date"},
 {"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.311","Status":"update_available"},
 {"Product":"VS Code Claude Code 插件","Installed":"2.1.310","Latest":"2.1.311","Status":"update_available"}]`
	targetsOK = `[
 {"Name":"Claude Desktop 主程序及内置浏览器","Path":"C:\\a\\claude.exe","State":"unchanged"},
 {"Name":"Claude Code npm 启动目标","Path":"C:\\b\\claude.exe","State":"unchanged"},
 {"Name":"Claude Code npm 原生包","Path":"C:\\c\\claude.exe","State":"unchanged"},
 {"Name":"Claude Code VS Code 插件","Path":"C:\\d\\claude.exe","State":"unchanged"}]`
	targetsUnsigned = `[
 {"Name":"Claude Desktop 主程序及内置浏览器","Path":"C:\\a\\claude.exe","State":"changed_unsigned"},
 {"Name":"Claude Code npm 启动目标","Path":"C:\\b\\claude.exe","State":"unchanged"},
 {"Name":"Claude Code npm 原生包","Path":"C:\\c\\claude.exe","State":"unchanged"},
 {"Name":"Claude Code VS Code 插件","Path":"C:\\d\\claude.exe","State":"unchanged"}]`
)

func checkRecord(products, targets string) string {
	return fmt.Sprintf(`{"Action":"Check","Status":"checked","Products":%s,"Targets":%s,"Timing":{"查版本":2.1},"Error":null}`, products, targets)
}

// fakeScript 模拟 更新三端Claude.ps1：check 是 -Action Check 写的 检查记录.json；update 在 -RunDirectory 里写 Update 的文件。
type fakeScript struct {
	check    string
	checkErr error
	update   func(dir string)
	exit     int
	gate     chan struct{} // 不为 nil 时，Update 写完文件后等它关闭才退出
}

func (f *fakeScript) run(name string, args ...string) (string, int, error) {
	action, dir := "Update", ""
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-Action":
			action = args[i+1]
		case "-RunDirectory":
			dir = args[i+1]
		}
	}
	if dir == "" {
		return "没有 -RunDirectory", 1, nil
	}
	_ = os.MkdirAll(dir, 0o755)
	if action == "Check" {
		if f.checkErr != nil {
			return "", -1, f.checkErr
		}
		if f.check != "" {
			_ = os.WriteFile(filepath.Join(dir, "检查记录.json"), []byte(f.check), 0o644)
		}
		return "[16:00:20] 查三端版本", 0, nil
	}
	if f.update != nil {
		f.update(dir)
	}
	if f.gate != nil {
		<-f.gate
	}
	return "[16:00:20] 更新输出", f.exit, nil
}

type harness struct {
	t      *testing.T
	sys    *platform.Fake
	r      *Runner
	script *fakeScript
}

func newHarness(t *testing.T, s *fakeScript) *harness {
	t.Helper()
	sys := platform.NewFake(t.TempDir())
	r := New(sys, "")
	if err := os.MkdirAll(filepath.Dir(r.Script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.Script, []byte("# 假脚本"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(r.Root, 0o755)
	r.Pwsh = "pwsh.exe"
	r.UseScript = true // 这组测试按脚本写的记录驱动；Go 查版本另见 gocheck_test.go
	sys.RunFunc = s.run
	return &harness{t: t, sys: sys, r: r, script: s}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pastRun 在记录根目录下造一次历史更新。
func (h *harness) pastRun(stamp, record string) string {
	dir := filepath.Join(h.r.Root, runDirPrefix+stamp)
	writeFile(h.t, filepath.Join(dir, "记录.json"), record)
	return dir
}

// apply 查一次版本后开始更新，等脚本退出后返回最终状态。
func (h *harness) apply() View {
	h.t.Helper()
	if v := h.r.Check(context.Background()); v.Phase != "update" {
		h.t.Fatalf("开始前应为 update，实际 %s：%s %s", v.Phase, v.Title, v.Line)
	}
	if _, err := h.r.Start(); err != nil {
		h.t.Fatal(err)
	}
	return h.wait()
}

func (h *harness) wait() View {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v := h.r.View(); v.Phase != "running" {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatal("脚本 5 秒内没有结束")
	return View{}
}

func stepIDs(steps []Step) string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, ",")
}

// ---------------------------------------------------------------- 查版本（Status=checked）

func TestCheckLatest(t *testing.T) {
	h := newHarness(t, &fakeScript{check: checkRecord(prodLatest, targetsOK)})
	h.pastRun("20261024-094215", `{"Status":"passed","Timing":{"查版本":6.8,"完成":99.8}}`)
	v := h.r.Check(context.Background())
	if v.Phase != "latest" || v.Title != "三端都是最新" {
		t.Fatalf("应为已是最新，实际 %s %q", v.Phase, v.Title)
	}
	if len(v.Steps) != 1 || v.Steps[0].State != "done" {
		t.Fatalf("已是最新时只走第 1 步：%+v", v.Steps)
	}
	if !strings.Contains(v.Line, "10-24 09:42") || !strings.Contains(v.Line, "99.8") {
		t.Errorf("应写上次更新时间和用时：%q", v.Line)
	}
	if len(v.Products) != 3 || v.Products[2].Name != "VS Code 插件" || v.Products[0].Behind {
		t.Errorf("产品卡不对：%+v", v.Products)
	}
	log := strings.Join(h.sys.RunLog, "\n")
	if !strings.Contains(log, "-Action Check -RunDirectory "+h.r.CheckDir) || !strings.Contains(log, "-NonInteractive") {
		t.Errorf("查版本应传 -Action Check 和卫士的记录目录：%s", log)
	}
}

func TestCheckCliOnlyNoUAC(t *testing.T) {
	h := newHarness(t, &fakeScript{check: checkRecord(prodCli, targetsOK)})
	v := h.r.Check(context.Background())
	if v.Phase != "update" || v.NeedsUAC {
		t.Fatalf("只有 CLI 落后：应为 update 且不弹 UAC，实际 %s uac=%v", v.Phase, v.NeedsUAC)
	}
	if got := stepIDs(v.Steps); got != "check,cli,hashes" {
		t.Errorf("步骤：%s", got)
	}
	if v.Hint != "不用弹 UAC" || !strings.Contains(v.Line, "2.1.310 → 2.1.311") {
		t.Errorf("状态条：%q / %q", v.Line, v.Hint)
	}
}

func TestCheckDesktopPlanAndEstimate(t *testing.T) {
	h := newHarness(t, &fakeScript{check: checkRecord(prodDesktop, targetsOK)})
	// 两次历史：下载 150 秒、总检只查动过的项 4 秒
	for i, stamp := range []string{"20261020-100000", "20261021-100000"} {
		dir := h.pastRun(stamp, fmt.Sprintf(`{"Status":"passed","Desktop":{"Reused":false,"DownloadSeconds":%d},
			"Timing":{"查版本":3,"Desktop包就绪":%d,"指纹汇总":%d,"一键更新":%d,"完成":%d},
			"Maintenance":{"StepSeconds":{"CLI指纹刷新":9,"安装包检查":7,"版本更新":30,"防护核验":3,"日常总检":4,"启动条件检查":5,"安装包清理":1}}}`,
			150+i*10, 163+i*10, 166+i*10, 232+i*10, 232+i*10))
		writeFile(t, filepath.Join(dir, "统一维护", "结果.json"), `{"Status":"passed","SummaryScope":"Guard","StepSeconds":{}}`)
	}
	v := h.r.Check(context.Background())
	if v.Phase != "update" || !v.NeedsUAC {
		t.Fatalf("Desktop 落后要弹 UAC：%s %v", v.Phase, v.NeedsUAC)
	}
	want := "check,cli,download,inspect,hashes,uac,refresh,pkgcheck,install,verify,summary,launch,cleanup"
	if got := stepIDs(v.Steps); got != want {
		t.Errorf("步骤：\n got %s\nwant %s", got, want)
	}
	for _, s := range v.Steps {
		switch s.ID {
		case "download":
			if s.Est != 160 { // 样本不足 3 个取最近一次
				t.Errorf("下载预计应按最近一次 160 秒，实际 %v", s.Est)
			}
		case "summary":
			if s.Est != 4 || s.Name != "只查动过的项" {
				t.Errorf("总检应只查动过的项、按历史 4 秒：%+v", s)
			}
		case "uac":
			if s.Phase != 2 || s.UAC {
				t.Errorf("等 UAC 是第 3 步（授权），本身不在 UAC 内：%+v", s)
			}
		}
	}
	if v.Estimate < 200 || !strings.Contains(v.Line, "含 Desktop 下载") || v.Hint != "会弹一次 UAC，那就是确认" {
		t.Errorf("预计 %v 秒；%q；%q", v.Estimate, v.Line, v.Hint)
	}
}

func TestCheckBlockedPluginAndRunning(t *testing.T) {
	h := newHarness(t, &fakeScript{check: checkRecord(prodPlugin, targetsOK)})
	h.sys.Procs = []platform.Process{
		{PID: 11, Name: "claude.exe", Exe: `C:\Users\x\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\bin\claude.exe`},
		{PID: 12, Name: "claude.exe", Exe: `c:\Users\x\.vscode\extensions\anthropic.claude-code-2.1.310-win32-x64\resources\native-binary\claude.exe`},
		{PID: 13, Name: "Code.exe"},
	}
	v := h.r.Check(context.Background())
	if v.Phase != "blocked" || len(v.Blockers) != 2 {
		t.Fatalf("应列出 2 件要先做的事：%s %+v", v.Phase, v.Blockers)
	}
	run, plug := v.Blockers[0], v.Blockers[1]
	if run.Kind != "running" || len(run.Procs) != 2 || run.Procs[0].Kind != "命令行" || run.Procs[1].Kind != "VS Code 插件" {
		t.Errorf("在跑的 claude.exe 要按路径分端：%+v", run)
	}
	if plug.Kind != "plugin" || !plug.Link || !strings.Contains(plug.Title, "2.1.311") {
		t.Errorf("插件待办：%+v", plug)
	}
	if v.Title != "还差 2 件事要你先做" || v.Hint != "本次没有下载、没有改任何文件" {
		t.Errorf("状态条：%q %q", v.Title, v.Hint)
	}
	if _, err := h.r.Start(); err != ErrNotReady {
		t.Errorf("要先处理时不能开始更新：%v", err)
	}
}

func TestCheckUnsafe(t *testing.T) {
	h := newHarness(t, &fakeScript{check: checkRecord(prodDesktop, targetsUnsigned)})
	v := h.r.Check(context.Background())
	if v.Phase != "unsafe" || v.Tier != TierAI {
		t.Fatalf("指纹变了又没签名：应交给 AI，实际 %s %s", v.Phase, v.Tier)
	}
	if !strings.Contains(v.AIText, "先别改") || !strings.Contains(v.AIText, "没有有效的 Anthropic 签名") {
		t.Errorf("复制给 AI 的内容：%s", v.AIText)
	}
}

func TestCheckFailures(t *testing.T) {
	cases := []struct {
		name  string
		s     *fakeScript
		tier  string
		title string
	}{
		{"网络", &fakeScript{check: `{"Action":"Check","Status":"failed","Error":"官方入口无响应；检查 43867 通道。"}`}, TierRetry, "没查到新版本"},
		{"没写记录", &fakeScript{}, TierAI, "没查到新版本"},
		{"架构", &fakeScript{check: `{"Action":"Check","Status":"failed","Error":"已装包架构 arm64 与系统架构 x64 不一致，停止。"}`}, TierAI, "没查到新版本"},
		{"启动失败", &fakeScript{checkErr: fmt.Errorf("exec: pwsh.exe: not found")}, TierAI, "没查到新版本"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.s)
			v := h.r.Check(context.Background())
			if v.Phase != "checkfail" || v.Tier != c.tier || v.Title != c.title {
				t.Fatalf("got %s/%s %q（%s）", v.Phase, v.Tier, v.Title, v.Error)
			}
			if c.tier == TierAI && v.AIText == "" {
				t.Error("交给 AI 时要有可复制的内容")
			}
		})
	}
}

func TestCheckMissingScript(t *testing.T) {
	h := newHarness(t, &fakeScript{})
	_ = os.Remove(h.r.Script)
	v := h.r.Check(context.Background())
	if v.Phase != "checkfail" || v.Tier != TierAI || len(h.sys.RunLog) != 0 {
		t.Fatalf("找不到脚本时不应启动 pwsh：%s %s %v", v.Phase, v.Tier, h.sys.RunLog)
	}
}

// 找法见 internal/scriptroot：设置 → 部署清单 → 桌面\AI应用开发 → 桌面\Claude环境分享版；这里只验更新页用的是同一套。
func TestNewPrefersActiveProject(t *testing.T) {
	sys := platform.NewFake(t.TempDir())
	share := filepath.Join(sys.P.Desktop, "Claude环境分享版")
	writeFile(t, filepath.Join(scriptroot.DocDir(share), "README.md"), "#")
	writeFile(t, filepath.Join(share, scriptRel), "#")
	if r := New(sys, ""); r.Script != filepath.Join(share, scriptRel) {
		t.Errorf("只有分享包时用分享包：%s", r.Script)
	}
	active := filepath.Join(sys.P.Desktop, "AI应用开发")
	writeFile(t, filepath.Join(scriptroot.DocDir(active), "README.md"), "#")
	writeFile(t, filepath.Join(active, scriptRel), "#")
	if r := New(sys, ""); r.Script != filepath.Join(active, scriptRel) {
		t.Errorf("两份都在、没手填时用 AI应用开发（活动源）：%s", r.Script)
	}
	if r := New(sys, share); r.Script != filepath.Join(share, scriptRel) {
		t.Errorf("设置里手填的分享包最优先：%s", r.Script)
	}
}

// ---------------------------------------------------------------- 一键更新：6 种结局

func TestApplyPassed(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodDesktop, targetsOK), update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Action":"Update","Status":"passed","Products":`+prodDesktop+`,
			"CliInstall":{"From":"2.1.310","To":"2.1.311"},"Desktop":{"NewVersion":"3.6601.0.0","Reused":false,"DownloadSeconds":120},
			"Maintenance":{"Status":"passed","HashCount":4,"FiltersCount":8,"StepSeconds":{"CLI指纹刷新":9,"安装包检查":7,"版本更新":30,"防护核验":3,"日常总检":4,"启动条件检查":5,"安装包清理":1}},
			"After":{"Desktop":"3.6601.0.0","Cli":"2.1.311","Plugin":"2.1.310","PluginProtected":true},
			"Timing":{"查版本":2,"CLI安装":40,"Desktop包就绪":170,"指纹汇总":173,"一键更新":240,"完成":240.3},"Command":"& x"}`)
		writeFile(t, filepath.Join(dir, "统一维护", "进度.log"), "开始 CLI指纹刷新\n完成 CLI指纹刷新（9 秒）\n")
	}}
	h := newHarness(t, s)
	v := h.apply()
	if v.Phase != "done" || v.Percent != 100 || v.Elapsed != 240.3 {
		t.Fatalf("应完成：%s %v%% %v 秒 %q", v.Phase, v.Percent, v.Elapsed, v.Error)
	}
	if !strings.Contains(v.Line, "4/4") || !strings.Contains(v.Line, "8/8") {
		t.Errorf("要写防护核验结果：%q", v.Line)
	}
	if v.Products[0].Installed != "3.6601.0.0" || v.Products[1].Installed != "2.1.311" {
		t.Errorf("产品卡要换成回读版本：%+v", v.Products)
	}
	for _, st := range v.Steps {
		if st.State != "done" && st.State != "skip" {
			t.Errorf("完成后不应还有 %s：%s", st.State, st.ID)
		}
	}
	log := strings.Join(h.sys.RunLog, "\n")
	if !strings.Contains(log, "-RunDirectory "+filepath.Join(h.r.Root, runDirPrefix)) || !strings.Contains(log, "-ChangedOnly") {
		t.Errorf("Update 要传卫士的记录目录和 -ChangedOnly：%s", log)
	}
	if strings.Contains(log, "-Action Update") {
		t.Errorf("Update 不传 -Action（脚本默认就是 Update）：%s", log)
	}
	if len(v.History) == 0 || v.History[0].Tone != "pass" || !strings.Contains(v.History[0].What, "Desktop → 3.6601") {
		t.Errorf("最近几次要有这一次：%+v", v.History)
	}
}

func TestApplyUpToDateCliOnly(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodCli, targetsOK), update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"up_to_date","Products":`+prodCli+`,"CliInstall":{"From":"2.1.310","To":"2.1.311"},"Timing":{"查版本":2,"CLI安装":41,"指纹汇总":44}}`)
	}}
	h := newHarness(t, s)
	v := h.apply()
	if v.Phase != "done" || !strings.Contains(v.Title, "2.1.311") || !strings.Contains(v.Line, "没弹 UAC") {
		t.Fatalf("CLI 同路径更新：%s %q %q", v.Phase, v.Title, v.Line)
	}
}

func TestApplyUpToDateNothing(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodCli, targetsOK), update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"up_to_date","Products":`+prodLatest+`,"Timing":{"查版本":2,"指纹汇总":4}}`)
	}}
	v := newHarness(t, s).apply()
	if v.Phase != "latest" {
		t.Fatalf("脚本说已是最新（例如别处已更新）：%s", v.Phase)
	}
}

func TestApplyBlockedRunning(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodCli, targetsOK), exit: 1, update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"blocked_running","Error":"有 claude 进程在运行","RunningProcesses":[{"ProcessId":7,"ExecutablePath":"C:\\Program Files\\WindowsApps\\Claude_3.6556.2.0_x64__pzs8sxrjxfjjc\\app\\claude.exe"}],"Timing":{"查版本":2}}`)
	}}
	v := newHarness(t, s).apply()
	if v.Phase != "blocked" || len(v.Blockers) != 1 || v.Blockers[0].Kind != "running" || v.Blockers[0].Procs[0].Kind != "Desktop" {
		t.Fatalf("Claude 在运行：%s %+v", v.Phase, v.Blockers)
	}
}

func TestApplyPluginNeeded(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodCli, targetsOK), exit: 1, update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"plugin_update_needed","Products":`+prodPlugin+`,"Timing":{"查版本":2}}`)
	}}
	v := newHarness(t, s).apply()
	if v.Phase != "blocked" || v.Blockers[0].Kind != "plugin" || !v.Blockers[0].Link {
		t.Fatalf("插件要手点：%s %+v", v.Phase, v.Blockers)
	}
}

func TestApplyLocked(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodCli, targetsOK), exit: 1, update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"blocked_locked","Error":"另一份三端更新正在运行"}`)
	}}
	v := newHarness(t, s).apply()
	if v.Phase != "blocked" || v.Blockers[0].Kind != "locked" {
		t.Fatalf("单实例锁：%s %+v", v.Phase, v.Blockers)
	}
}

// ---------------------------------------------------------------- 失败分三层

func failedRun(t *testing.T, errText, maintErr string) func(string) {
	return func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), fmt.Sprintf(`{"Status":"failed","Error":%q,"Timing":{"查版本":2,"CLI安装":40,"指纹汇总":43},"Command":"& x"}`, errText))
		if maintErr != "" {
			writeFile(t, filepath.Join(dir, "统一维护", "结果.json"), fmt.Sprintf(`{"Status":"failed","Error":%q}`, maintErr))
		}
	}
}

func TestFailRetryTier(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodDesktop, targetsOK), exit: 1,
		update: failedRun(t, "Desktop 安装包下载失败，curl 退出码 28；未完成文件已删除，重跑会整包重下。", "")}
	h := newHarness(t, s)
	v := h.apply()
	if v.Phase != "failed" || v.Tier != TierRetry || v.AIText != "" {
		t.Fatalf("下载断了应让界面重试：%s %s", v.Phase, v.Tier)
	}
	if v.FailedStep == "" || !strings.Contains(v.Line, "已完成的部分不会重做") {
		t.Errorf("要写停在哪一步：%q %q", v.FailedStep, v.Line)
	}
	// 再失败一次：连续 2 次网络失败升到交给 AI
	v = h.apply()
	if v.Phase != "failed" || v.Tier != TierAI || v.AIText == "" {
		t.Fatalf("连续失败 2 次应交给 AI：%s %s", v.Phase, v.Tier)
	}
}

func TestFailUserTier(t *testing.T) {
	cases := []struct{ name, maint, kind string }{
		{"UAC 点了否", "管理员授权被取消或无法启动；未重试提权。The operation was canceled by the user.", "uac"},
		{"UAC 里发现 Claude 在跑", "Claude 正在运行；请退出后再执行。本入口不会强行关闭或启动应用。", "running"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &fakeScript{check: checkRecord(prodDesktop, targetsOK), exit: 1,
				update: failedRun(t, `一键更新 失败，退出码 1；日志：C:\x\一键更新.log`, c.maint)}
			v := newHarness(t, s).apply()
			if v.Phase != "blocked" || v.Blockers[0].Kind != c.kind {
				t.Fatalf("应列成待办 %s：%s %+v", c.kind, v.Phase, v.Blockers)
			}
		})
	}
}

func TestFailAITier(t *testing.T) {
	cases := []struct{ name, err, maint, title string }{
		{"签名", "新文件缺少有效的 Anthropic 签名，拒绝登记：Claude Desktop 主程序及内置浏览器", "", "安全检查没过，已停下"},
		{"防护核验", `一键更新 失败，退出码 1；日志：C:\x\一键更新.log`, "防护核验 失败；详情：C:\\x\\防护核验.log", "安全检查没过，已停下"},
		{"没见过的错", "Cannot index into a null array.", "", "更新没跑完，已停下"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &fakeScript{check: checkRecord(prodDesktop, targetsOK), exit: 1, update: failedRun(t, c.err, c.maint)}
			h := newHarness(t, s)
			v := h.apply()
			if v.Phase != "failed" || v.Tier != TierAI || v.Title != c.title {
				t.Fatalf("got %s/%s %q", v.Phase, v.Tier, v.Title)
			}
			if !strings.Contains(v.AIText, v.RunDir) || !strings.Contains(v.AIText, "先别改") || !strings.Contains(v.AIText, c.err) {
				t.Errorf("复制给 AI 要有记录目录、原因原文和「先别改」：\n%s", v.AIText)
			}
			if c.maint != "" && !strings.Contains(v.AIText, c.maint) {
				t.Errorf("要带上 UAC 内的原因：%s", v.AIText)
			}
		})
	}
}

func TestFailNoRecord(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodCli, targetsOK), exit: 1}
	v := newHarness(t, s).apply()
	if v.Phase != "failed" || v.Tier != TierAI || !strings.Contains(v.Error, "没有写出记录") {
		t.Fatalf("脚本没写记录就退出：%s %s %q", v.Phase, v.Tier, v.Error)
	}
}

func TestRetryRechecks(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodDesktop, targetsOK), exit: 1,
		update: failedRun(t, "Desktop 安装包下载失败，curl 退出码 28。", "")}
	h := newHarness(t, s)
	if v := h.apply(); v.Tier != TierRetry {
		t.Fatalf("前提：网络失败 %s", v.Tier)
	}
	// 重试前 CLI 已装好：重新查后计划里不再有 npm 安装
	s.check = checkRecord(strings.Replace(prodDesktop, `"2.1.311","Status":"update_available"`, `"2.1.311","Status":"up_to_date"`, 1), targetsOK)
	s.update, s.exit = func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"passed","Timing":{"查版本":2,"完成":200}}`)
	}, 0
	if _, err := h.r.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stepIDs(h.r.View().Steps), "cli") {
		t.Errorf("CLI 已装好，重试不应再列 npm 安装：%s", stepIDs(h.r.View().Steps))
	}
	if v := h.wait(); v.Phase != "done" {
		t.Fatalf("重试后应完成：%s", v.Phase)
	}
}

func TestStartTwiceAndBusy(t *testing.T) {
	gate := make(chan struct{})
	s := &fakeScript{check: checkRecord(prodCli, targetsOK), gate: gate, update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"running","Timing":{"查版本":2}}`)
	}}
	h := newHarness(t, s)
	h.r.Check(context.Background())
	v1, err := h.r.Start()
	if err != nil || v1.Phase != "running" || !h.r.Running() || !h.r.Busy() {
		t.Fatalf("开始后应在更新中：%v %s", err, v1.Phase)
	}
	if v := h.r.Check(context.Background()); v.Phase != "running" {
		t.Errorf("更新中再点检查不应打断：%s", v.Phase)
	}
	if _, err := h.r.Start(); err != nil {
		t.Errorf("更新中再点开始应直接返回当前状态：%v", err)
	}
	runs := 0
	for _, l := range h.sys.RunLog {
		if !strings.Contains(l, "-Action Check") {
			runs++
		}
	}
	if runs != 1 {
		t.Errorf("只能起一份 Update，实际 %d 份", runs)
	}
	close(gate)
	h.wait()
}

// ---------------------------------------------------------------- 进度推断

func TestInferProgressInsideUAC(t *testing.T) {
	sit := situation{Desktop: true, Cli: true}
	plan := buildPlan(sit, map[string]float64{}, 300, true)
	dir := t.TempDir()
	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.Local)
	obs := newObserver(now)
	rec := &scriptRecord{Desktop: &desktopPackage{DownloadSeconds: 100}, Command: "& x"}
	if err := rec.Timing.UnmarshalJSON([]byte(`{"查版本":2,"CLI安装":40,"Desktop包就绪":150,"指纹汇总":153}`)); err != nil {
		t.Fatal(err)
	}
	lines := []string{"开始 CLI指纹刷新", "完成 CLI指纹刷新（9 秒）", "开始 安装包检查", "完成 安装包检查（7 秒）", "开始 版本更新"}
	// 第一次看到：等 UAC 已过（进度.log 有内容），当前是「安装新版 Desktop」
	snap := infer(plan, dir, dir, rec, lines, obs, now, true)
	cur := snap.Steps[snap.Cur]
	if cur.ID != "install" || cur.State != "run" {
		t.Fatalf("当前步骤应为 install，实际 %s %s", cur.ID, cur.State)
	}
	byID := map[string]Step{}
	for _, s := range snap.Steps {
		byID[s.ID] = s
	}
	if byID["cli"].Secs != 38 || byID["download"].Secs != 100 || byID["inspect"].Secs != 10 || byID["refresh"].Secs != 9 {
		t.Errorf("已完成步骤秒数：cli %v download %v inspect %v refresh %v", byID["cli"].Secs, byID["download"].Secs, byID["inspect"].Secs, byID["refresh"].Secs)
	}
	if byID["uac"].State != "done" || byID["verify"].State != "wait" {
		t.Errorf("uac %s verify %s", byID["uac"].State, byID["verify"].State)
	}
	// 12 秒后再看：秒表在走，剩余时间按预计减已用
	snap = infer(plan, dir, dir, rec, lines, obs, now.Add(12*time.Second), true)
	cur = snap.Steps[snap.Cur]
	if cur.Secs != 12 {
		t.Errorf("没动静的步骤也要有秒表：%v", cur.Secs)
	}
	left := remaining(snap.Steps, snap.Cur, snap.Download)
	want := (defaultEst["install"] - 12) + defaultEst["verify"] + defaultEst["summary"] + defaultEst["launch"] + defaultEst["cleanup"]
	if left != round1(want) {
		t.Errorf("剩余 %v，应为 %v", left, want)
	}
}

func TestInferSkipsUnrunUACStep(t *testing.T) {
	// Desktop 版本相同不重装：安装包检查完成后直接到防护核验，中间的「安装新版 Desktop」算跳过
	plan := buildPlan(situation{Desktop: true}, map[string]float64{}, 300, true)
	now := time.Now()
	rec := &scriptRecord{Command: "& x"}
	_ = rec.Timing.UnmarshalJSON([]byte(`{"查版本":2,"Desktop包就绪":150,"指纹汇总":153}`))
	lines := []string{"开始 CLI指纹刷新", "完成 CLI指纹刷新（9 秒）", "开始 安装包检查", "完成 安装包检查（7 秒）", "开始 防护核验"}
	snap := infer(plan, t.TempDir(), t.TempDir(), rec, lines, newObserver(now), now, true)
	if snap.Steps[snap.Cur].ID != "verify" {
		t.Fatalf("当前应为 verify：%s", snap.Steps[snap.Cur].ID)
	}
	for _, s := range snap.Steps {
		if s.ID == "install" && s.State != "skip" {
			t.Errorf("install 应为跳过：%s", s.State)
		}
	}
}

func TestDownloadProgress(t *testing.T) {
	root := t.TempDir()
	dl := filepath.Join(root, "Desktop安装包下载")
	writeFile(t, filepath.Join(dl, "Claude-3.6556.2.0.msix"), strings.Repeat("x", 4<<20))
	writeFile(t, filepath.Join(dl, "Claude-3.6601.0.0-下载中.msix"), strings.Repeat("x", 1<<20))
	d := downloadProgress(root, 2)
	if d.TotalMB != 4 || d.MB != 1 || d.Speed != 0.5 {
		t.Errorf("下载进度：%+v", d)
	}
	if left := remaining([]Step{{ID: "download", State: "run", Est: 100, Secs: 10}}, 0, &Download{MB: 100, TotalMB: 300, Speed: 2}); left != 100 {
		t.Errorf("下载跑了 5 秒以上按实际速度估：%v", left)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"Desktop 安装包下载失败，curl 退出码 28": TierRetry,
		"npm 安装 CLI 失败，退出码 1；日志：x":    TierRetry,
		"npm view 没有返回版本号：":           TierRetry,
		"安装包审核 失败，退出码 1；日志：x":         TierAI,
		"更新后 Desktop 版本回读为 a，预期 b":    TierAI,
		"管理员授权被取消或无法启动；未重试提权。":        TierUser,
		"Claude 正在运行；请退出后再执行。":        TierUser,
		"":                          TierAI,
		"Exception calling \"Foo\"": TierAI,
		"下载失败，但签名也不对":               TierAI, // 安全类优先
	}
	for text, want := range cases {
		if got := classify(text); got != want {
			t.Errorf("%q → %s，应为 %s", text, got, want)
		}
	}
}

func TestHistorySummaries(t *testing.T) {
	root := t.TempDir()
	write := func(stamp, body string) {
		writeFile(t, filepath.Join(root, runDirPrefix+stamp, "记录.json"), body)
	}
	write("20261023-054028", `{"Status":"passed","Desktop":{"NewVersion":"3.6556.2.0"},"Timing":{"完成":293.8}}`)
	write("20261024-093949", `{"Status":"plugin_update_needed","Timing":{"查版本":19.3}}`)
	write("20261024-094215", `{"Status":"passed","CliInstall":{"To":"2.1.310"},"Targets":[{"State":"moved"}],"After":{"Plugin":"2.1.310"},"Timing":{"完成":99.8}}`)
	write("20261024-142740", `{"Status":"up_to_date","Timing":{"查版本":6.5,"指纹汇总":7.9}}`)
	write("20261024-150000", `{"Status":"failed","Error":"Desktop 安装包下载失败，curl 退出码 28；未完成文件已删除"}`)
	_ = os.MkdirAll(filepath.Join(root, runDirPrefix+"20261024-160000"), 0o755) // 没有记录的目录跳过
	hs := history(runDirs(root), 5)
	if len(hs) != 5 {
		t.Fatalf("应有 5 条：%+v", hs)
	}
	want := []struct{ at, tone, what string }{
		{"10-24 15:00", "fail", "失败：Desktop 安装包下载失败"},
		{"10-24 14:27", "pass", "已是最新"},
		{"10-24 09:42", "pass", "CLI + 插件 → 2.1.310"},
		{"10-24 09:39", "info", "插件落后，停下等手点"},
		{"10-23 05:40", "pass", "Desktop → 3.6556"},
	}
	for i, w := range want {
		if hs[i].At != w.at || hs[i].Tone != w.tone || !strings.HasPrefix(hs[i].What, w.what) {
			t.Errorf("第 %d 条：%+v，应为 %+v", i, hs[i], w)
		}
	}
	if hs[2].Seconds != 99.8 || hs[1].Seconds != 7.9 {
		t.Errorf("用时：%v %v", hs[2].Seconds, hs[1].Seconds)
	}
}

func TestEstimatesMedian(t *testing.T) {
	root := t.TempDir()
	for i, npm := range []float64{30, 50, 40, 90} {
		writeFile(t, filepath.Join(root, fmt.Sprintf("%s2026102%d-100000", runDirPrefix, i), "记录.json"),
			fmt.Sprintf(`{"Status":"passed","Timing":{"查版本":5,"CLI安装":%v,"指纹汇总":%v}}`, 5+npm, 8+npm))
	}
	est := estimates(runDirs(root))
	if est["cli"] != 50 { // 最近 4 个：90、40、50、30 → 中位数取第 3 小
		t.Errorf("npm 预计应取中位数 50，实际 %v", est["cli"])
	}
	if est["hashes"] != 3 {
		t.Errorf("指纹汇总：%v", est["hashes"])
	}
}

func TestViewLoadsHistoryBeforeCheck(t *testing.T) {
	h := newHarness(t, &fakeScript{})
	h.pastRun("20261024-142740", `{"Status":"up_to_date","Timing":{"指纹汇总":7.9}}`)
	v := h.r.View()
	if v.Phase != "never" || len(v.History) != 1 {
		t.Fatalf("没查过时也要显示最近几次：%s %+v", v.Phase, v.History)
	}
}
