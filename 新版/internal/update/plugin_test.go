package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ClaudeEnvGuard/internal/platform"
)

// 10-24 起的脚本插件落后时用 VS Code 命令行自己装（脚本里有 --install-extension）；旧脚本仍要用户手点，见 update_test.go。

const prodPluginOnly = `[
 {"Product":"Claude Desktop","Installed":"3.6556.2.0","Latest":"3.6556.2.0","Status":"up_to_date"},
 {"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.310","Status":"up_to_date"},
 {"Product":"VS Code Claude Code 插件","Installed":"2.1.310","Latest":"2.1.311","Status":"update_available"}]`

func newAutoHarness(t *testing.T, s *fakeScript) *harness {
	t.Helper()
	h := newHarness(t, s)
	if err := os.WriteFile(h.r.Script, []byte("# 假脚本\n& $codeCli --install-extension ('anthropic.claude-code@' + $pluginLatest)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func stepByID(steps []Step, id string) *Step {
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i]
		}
	}
	return nil
}

func TestCheckPluginAutoPlan(t *testing.T) {
	h := newAutoHarness(t, &fakeScript{check: checkRecord(prodPlugin, targetsOK)})
	v := h.r.Check(context.Background())
	if v.Phase != "update" || len(v.Blockers) != 0 {
		t.Fatalf("插件落后不再是待办，应直接可以更新：%s %+v", v.Phase, v.Blockers)
	}
	if got := stepIDs(v.Steps); got != "check,cli,plugin,hashes,uac,migrate,refresh,verify,summary,launch,cleanup" {
		t.Errorf("步骤：%s", got)
	}
	if p := stepByID(v.Steps, "plugin"); p == nil || p.Phase != 1 || p.Est != 30 {
		t.Errorf("装插件归在第 2 步「下载与安装」：%+v", p)
	}
	if !v.NeedsUAC || v.Hint != "会弹一次 UAC，那就是确认" {
		t.Errorf("插件换目录要迁移防护，要弹 UAC：%v %q", v.NeedsUAC, v.Hint)
	}
	if v.Title != "有 2 端可以更新" || !strings.Contains(v.Line, "VS Code 插件 2.1.310 → 2.1.311") {
		t.Errorf("状态条要算上插件：%q %q", v.Title, v.Line)
	}
	if PhaseNames[1] != "下载与安装" {
		t.Errorf("第 2 步名字：%s", PhaseNames[1])
	}
}

func TestCheckPluginAutoVSCodeRunning(t *testing.T) {
	h := newAutoHarness(t, &fakeScript{check: checkRecord(prodPluginOnly, targetsOK)})
	h.sys.Procs = []platform.Process{{PID: 21, Name: "Code.exe"}, {PID: 22, Name: "Code.exe"}}
	v := h.r.Check(context.Background())
	if v.Phase != "blocked" || len(v.Blockers) != 1 {
		t.Fatalf("VS Code 开着应只列一件事：%s %+v", v.Phase, v.Blockers)
	}
	b := v.Blockers[0]
	if b.Kind != "vscode" || b.Link || b.Title != "退出 VS Code" || !strings.Contains(b.Detail, "2 个进程") || !strings.Contains(b.Detail, "不会替你强杀") {
		t.Errorf("待办是退出 VS Code，不再是手点更新：%+v", b)
	}
	if _, err := h.r.Start(); err != ErrNotReady {
		t.Errorf("VS Code 开着不能开始：%v", err)
	}
	// 退出 VS Code 后重新检查就能走。
	h.sys.Procs = nil
	if v := h.r.Check(context.Background()); v.Phase != "update" || v.Title != "有 1 端可以更新" {
		t.Errorf("退出后应能更新：%s %q", v.Phase, v.Title)
	}
}

func TestApplyPluginInstalled(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodPluginOnly, targetsOK), update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Action":"Update","Status":"passed","Products":[
 {"Product":"Claude Desktop","Installed":"3.6556.2.0","Latest":"3.6556.2.0","Status":"up_to_date"},
 {"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.310","Status":"up_to_date"},
 {"Product":"VS Code Claude Code 插件","Installed":"2.1.311","Latest":"2.1.311","Status":"up_to_date"}],
			"PluginInstall":{"From":"2.1.310","To":"2.1.311","Log":"x\\插件安装.log","Backup":"x\\extensions-装插件前.json","Problem":null},
			"Targets":[{"Name":"Claude Code VS Code 插件","Path":"C:\\d\\claude.exe","State":"moved"}],
			"Maintenance":{"Status":"passed","HashCount":4,"FiltersCount":8,"StepSeconds":{"插件路径迁移":4,"CLI指纹刷新":9,"防护核验":3,"日常总检":4,"启动条件检查":5,"安装包清理":1}},
			"After":{"Desktop":"3.6556.2.0","Cli":"2.1.310","Plugin":"2.1.311","PluginProtected":true},
			"Timing":{"查版本":2,"插件安装":27,"指纹汇总":30,"一键更新":62,"完成":62.4}}`)
	}}
	h := newAutoHarness(t, s)
	v := h.apply()
	if v.Phase != "done" {
		t.Fatalf("应完成：%s %q", v.Phase, v.Error)
	}
	if p := stepByID(v.Steps, "plugin"); p == nil || p.State != "done" || p.Secs != 25 {
		t.Errorf("装插件一步应完成、用时 25 秒：%+v", p)
	}
	if m := stepByID(v.Steps, "migrate"); m == nil || m.State != "done" {
		t.Errorf("UAC 内迁移应完成：%+v", m)
	}
	if v.Products[2].Installed != "2.1.311" {
		t.Errorf("插件卡换成回读版本：%+v", v.Products[2])
	}
	if len(v.History) == 0 || !strings.Contains(v.History[0].What, "插件 → 2.1.311") {
		t.Errorf("最近几次要写插件：%+v", v.History)
	}
	// 下次估时用这次装插件的秒数。
	if est := estimates([]string{v.RunDir}); est["plugin"] != 25 {
		t.Errorf("装插件估时：%v", est)
	}
}

func TestApplyBlockedVSCode(t *testing.T) {
	s := &fakeScript{check: checkRecord(prodPluginOnly, targetsOK), exit: 1, update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"blocked_vscode","Products":`+prodPluginOnly+`,
			"Error":"插件 2.1.310 → 2.1.311 要先退出 VS Code（全部窗口）再重跑。本次没有下载、没有改任何文件。",
			"RunningProcesses":[{"ProcessId":31,"ExecutablePath":"C:\\Users\\x\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe"}],"Timing":{"查版本":2}}`)
	}}
	v := newAutoHarness(t, s).apply()
	if v.Phase != "blocked" || len(v.Blockers) != 1 || v.Blockers[0].Kind != "vscode" || !strings.Contains(v.Blockers[0].Detail, "1 个进程") {
		t.Fatalf("脚本发现 VS Code 开着：%s %+v", v.Phase, v.Blockers)
	}
	if len(v.History) == 0 || v.History[0].What != "VS Code 开着，停下" {
		t.Errorf("最近几次：%+v", v.History)
	}
}

func TestApplyPluginRestored(t *testing.T) {
	msg := `插件安装后核对不通过（pinned 不是 true（VS Code 会自动更新插件）），已用装之前的 extensions.json 恢复；日志：C:\x\插件安装.log`
	s := &fakeScript{check: checkRecord(prodPluginOnly, targetsOK), exit: 1, update: func(dir string) {
		writeFile(t, filepath.Join(dir, "记录.json"), `{"Status":"failed","Products":`+prodPluginOnly+`,
			"PluginInstall":{"From":"2.1.310","To":"2.1.311","Problem":"pinned 不是 true（VS Code 会自动更新插件）"},
			"Error":`+jsonString(msg)+`,"Timing":{"查版本":2}}`)
	}}
	v := newAutoHarness(t, s).apply()
	if v.Phase != "failed" || v.Tier != TierAI {
		t.Fatalf("核对不通过是安全问题，交给 AI 只读排查：%s/%s %q", v.Phase, v.Tier, v.Title)
	}
	if p := stepByID(v.Steps, "plugin"); p == nil || p.State != "fail" {
		t.Errorf("停在装插件这一步：%+v", v.Steps)
	}
	if !strings.Contains(v.AIText, "已用装之前的 extensions.json 恢复") {
		t.Errorf("复制给 AI 要有原因原文：%s", v.AIText)
	}
	if v.Products[2].Installed != "2.1.310" {
		t.Errorf("恢复后插件仍是旧版：%+v", v.Products[2])
	}
}

func jsonString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
