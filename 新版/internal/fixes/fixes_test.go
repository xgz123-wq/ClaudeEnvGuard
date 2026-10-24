package fixes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/checks"
	"ClaudeEnvGuard/internal/chrome"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

func newFake(t *testing.T) *platform.Fake {
	t.Helper()
	fk := platform.NewFake(t.TempDir())
	fk.Env["SystemRoot"] = `C:\Windows`
	return fk
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

const origSettings = "{\n  \"model\": \"opus\",\n  \"env\": {\n    \"FOO\": \"1\",\n    \"ANTHROPIC_BASE_URL\": \"https://relay.example.com\"\n  },\n  \"permissions\": {\n    \"allow\": [\n      \"Bash(ls)\"\n    ]\n  },\n  \"big\": 12345678901234567890\n}\n"

func TestApplyAndRestoreRoundTrip(t *testing.T) {
	fk := newFake(t)
	home := fk.P.Home
	settings := filepath.Join(home, ".claude", "settings.json")
	write(t, settings, origSettings)
	lnk := filepath.Join(fk.P.Desktop, "KK-Claude-专用Chrome.lnk")
	if err := fk.WriteShortcut(platform.Shortcut{Path: lnk, Target: `C:\chrome.exe`, Args: "--user-data-dir=x"}); err != nil {
		t.Fatal(err)
	}
	origLnk := readFile(t, lnk)
	fk.SetReg(platform.HKCU, facts.KeyUserEnv, "ANTHROPIC_AUTH_TOKEN", platform.RegValue{Type: "sz", Str: "sk-secret"})
	fk.SetReg(platform.HKLM, facts.KeyTZ, "TimeZoneKeyName", platform.RegValue{Type: "sz", Str: "China Standard Time"})
	var runs []string
	fk.RunFunc = func(name string, args ...string) (string, int, error) {
		runs = append(runs, filepath.Base(name)+" "+strings.Join(args, " "))
		return "", 0, nil
	}
	fk.Admin = true // 本测试只测普通步骤的备份还原
	plan := model.FixPlan{Actions: []model.FixAction{
		{ID: "a1", Title: "代理", Steps: []model.Step{jsonEnvStep(settings, map[string]string{"HTTPS_PROXY": "http://127.0.0.1:43865"}, []string{"ANTHROPIC_BASE_URL"})}},
		{ID: "a2", Title: "快捷方式", Steps: []model.Step{step("shortcut_write", "path", lnk, "target", `C:\chrome.exe`, "args", "--user-data-dir=x --lang=en-US")}},
		{ID: "a3", Title: "变量", Steps: []model.Step{step("reg_delete", "root", "HKCU", "key", facts.KeyUserEnv, "name", "ANTHROPIC_AUTH_TOKEN")}},
		{ID: "a4", Title: "时区", Steps: []model.Step{step("tz_set", "id", "Eastern Standard Time")}},
		{ID: "a5", Title: "新目录", Steps: []model.Step{step("mkdir", "path", filepath.Join(fk.P.LocalAppData, "ClaudeAccountBrowsers", "新号", "UserData"))}},
	}}
	res := Apply(fk, plan, []string{"a1", "a2", "a3", "a4", "a5"}, "self.exe")
	if !res.OK || res.BackupID == "" {
		t.Fatalf("执行失败：%+v", res)
	}
	got := readFile(t, settings)
	for _, want := range []string{`"FOO": "1"`, `"HTTPS_PROXY": "http://127.0.0.1:43865"`, `"Bash(ls)"`, `12345678901234567890`} {
		if !strings.Contains(got, want) {
			t.Errorf("修改后应包含 %s：\n%s", want, got)
		}
	}
	if strings.Contains(got, "ANTHROPIC_BASE_URL") {
		t.Errorf("BASE_URL 应被删除")
	}
	if strings.Index(got, `"model"`) > strings.Index(got, `"env"`) {
		t.Errorf("键顺序被打乱：\n%s", got)
	}
	sc, _ := fk.ReadShortcut(lnk)
	if !strings.Contains(sc.Args, "--lang=en-US") {
		t.Errorf("快捷方式没改：%+v", sc)
	}
	if v, _ := fk.RegRead(platform.HKCU, facts.KeyUserEnv, "ANTHROPIC_AUTH_TOKEN"); v.Exists {
		t.Errorf("用户变量应被删除")
	}
	if len(runs) != 1 || runs[0] != "tzutil.exe /s Eastern Standard Time" {
		t.Errorf("时区命令不对：%v", runs)
	}
	if !facts.Exists(filepath.Join(fk.P.LocalAppData, "ClaudeAccountBrowsers", "新号", "UserData")) {
		t.Errorf("目录没建")
	}
	list := ListBackups(fk.P.LocalAppData)
	if len(list) != 1 || list[0].Restored || len(list[0].Actions) != 5 {
		t.Fatalf("备份列表不对：%+v", list)
	}

	rr := Restore(fk, res.BackupID, "self.exe")
	if !rr.OK {
		t.Fatalf("还原失败：%+v", rr)
	}
	if readFile(t, settings) != origSettings {
		t.Errorf("settings.json 没有逐字节还原：\n%s", readFile(t, settings))
	}
	if readFile(t, lnk) != origLnk {
		t.Errorf("快捷方式没还原")
	}
	if v, _ := fk.RegRead(platform.HKCU, facts.KeyUserEnv, "ANTHROPIC_AUTH_TOKEN"); !v.Exists || v.Str != "sk-secret" {
		t.Errorf("用户变量没还原：%+v", v)
	}
	if runs[len(runs)-1] != "tzutil.exe /s China Standard Time" {
		t.Errorf("时区没还原：%v", runs)
	}
	if facts.Exists(filepath.Join(fk.P.LocalAppData, "ClaudeAccountBrowsers", "新号")) {
		t.Errorf("空的新目录应在还原时删除")
	}
	if l := ListBackups(fk.P.LocalAppData); !l[0].Restored {
		t.Errorf("还原后应标记已还原")
	}
	// 再还原一次：没有可还原的改动，不报错
	if rr2 := Restore(fk, res.BackupID, "self.exe"); !rr2.OK {
		t.Errorf("重复还原应无害：%+v", rr2)
	}
}

func TestNewSettingsFileCreatedAndRemovedOnRestore(t *testing.T) {
	fk := newFake(t)
	settings := filepath.Join(fk.P.Home, ".claude", "settings.json")
	plan := model.FixPlan{Actions: []model.FixAction{{ID: "a", Steps: []model.Step{jsonEnvStep(settings, map[string]string{"HTTP_PROXY": "http://127.0.0.1:1"}, nil)}}}}
	res := Apply(fk, plan, []string{"a"}, "")
	if !res.OK || !strings.Contains(readFile(t, settings), "HTTP_PROXY") {
		t.Fatalf("应新建 settings.json：%+v", res)
	}
	Restore(fk, res.BackupID, "")
	if facts.Exists(settings) {
		t.Errorf("原本不存在的文件还原后应删除")
	}
}

func TestCorruptJSONNotOverwritten(t *testing.T) {
	fk := newFake(t)
	settings := filepath.Join(fk.P.Home, ".claude", "settings.json")
	write(t, settings, `{"env": {"A": 1,}`)
	plan := model.FixPlan{Actions: []model.FixAction{{ID: "a", Title: "代理", Steps: []model.Step{jsonEnvStep(settings, map[string]string{"HTTP_PROXY": "x"}, nil)}}}}
	res := Apply(fk, plan, []string{"a"}, "")
	if res.OK || !strings.Contains(res.Outcomes[0].Message, "格式有误") {
		t.Errorf("损坏的 JSON 应拒绝修改：%+v", res)
	}
	if readFile(t, settings) != `{"env": {"A": 1,}` {
		t.Errorf("损坏文件被改动了")
	}
}

func TestActionIsAtomic(t *testing.T) {
	fk := newFake(t)
	settings := filepath.Join(fk.P.Home, ".claude", "settings.json")
	write(t, settings, `{"env":{}}`)
	fk.DeniedKeys[strings.ToLower(`HKCU\`+facts.KeyUserEnv)] = true
	plan := model.FixPlan{Actions: []model.FixAction{{ID: "a", Title: "两步", Steps: []model.Step{
		jsonEnvStep(settings, map[string]string{"HTTP_PROXY": "x"}, nil),
		step("reg_set", "root", "HKCU", "key", facts.KeyUserEnv, "name", "X", "type", "sz", "value", "1"),
	}}}}
	res := Apply(fk, plan, []string{"a"}, "")
	if res.OK || !strings.Contains(res.Outcomes[0].Message, "已撤回") {
		t.Fatalf("第二步失败应撤回整个动作：%+v", res)
	}
	if readFile(t, settings) != `{"env":{}}` {
		t.Errorf("第一步没被撤回：%s", readFile(t, settings))
	}
}

func TestElevatedApplyAndRestore(t *testing.T) {
	fk := newFake(t)
	fk.ElevateFunc = func(exe string, args []string) (int, error) {
		code, ok := HandleElevatedArgs(fk, args)
		if !ok {
			t.Fatalf("提权参数没被识别：%v", args)
		}
		return code, nil
	}
	f := &facts.Facts{Settings: appconfig.Defaults()}
	a := planDesktopPolicy(f)
	res := Apply(fk, model.FixPlan{Actions: []model.FixAction{*a}}, []string{a.ID}, "self.exe")
	if !res.OK {
		t.Fatalf("提权执行失败：%+v", res)
	}
	if v, _ := fk.RegRead(platform.HKLM, facts.KeyPolicy, "egressProxyUrl"); v.Str != "http://127.0.0.1:43867" {
		t.Errorf("策略没写入：%+v", v)
	}
	if v, _ := fk.RegRead(platform.HKLM, facts.KeyPolicy, "disableAutoUpdates"); v.DWord != 1 {
		t.Errorf("自动更新开关没写入：%+v", v)
	}
	rr := Restore(fk, res.BackupID, "self.exe")
	if !rr.OK {
		t.Fatalf("提权还原失败：%+v", rr)
	}
	if v, _ := fk.RegRead(platform.HKLM, facts.KeyPolicy, "egressProxyUrl"); v.Exists {
		t.Errorf("原本没有的策略还原后应删除")
	}
}

func TestElevationCancelled(t *testing.T) {
	fk := newFake(t)
	settings := filepath.Join(fk.P.Home, ".claude", "settings.json")
	f := &facts.Facts{Settings: appconfig.Defaults()}
	plan := model.FixPlan{Actions: []model.FixAction{
		*planDesktopPolicy(f),
		{ID: "u", Title: "普通", Steps: []model.Step{jsonEnvStep(settings, map[string]string{"HTTP_PROXY": "x"}, nil)}},
	}}
	res := Apply(fk, plan, []string{checks.FixDesktopPolicy, "u"}, "self.exe")
	if res.OK {
		t.Fatalf("取消 UAC 应报告失败")
	}
	var adminMsg string
	for _, o := range res.Outcomes {
		if o.ActionID == checks.FixDesktopPolicy {
			adminMsg = o.Message
		}
	}
	if !strings.Contains(adminMsg, "UAC") {
		t.Errorf("应说明 UAC 被取消：%v", res.Outcomes)
	}
	if !strings.Contains(readFile(t, settings), "HTTP_PROXY") {
		t.Errorf("普通项不应受 UAC 取消影响")
	}
}

func TestElevatedRejectsTamperedRequest(t *testing.T) {
	fk := newFake(t)
	dir := filepath.Join(BackupRoot(fk.P.LocalAppData), "x")
	evil := []model.FixAction{{ID: "evil", Title: "篡改", Steps: []model.Step{
		step("reg_set", "root", "HKLM", "key", `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, "name", "x", "type", "sz", "value", `C:\evil.exe`)}}}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, requestAdmin), evil); err != nil {
		t.Fatal(err)
	}
	ElevatedApply(fk, dir)
	if v, _ := fk.RegRead(platform.HKLM, `SOFTWARE\Microsoft\Windows\CurrentVersion\Run`, "x"); v.Exists {
		t.Fatalf("篡改的请求被执行了")
	}
	var outs []model.StepOutcome
	_ = readJSON(filepath.Join(dir, resultAdmin), &outs)
	if len(outs) != 1 || outs[0].OK || !strings.Contains(outs[0].Message, "拒绝") {
		t.Errorf("应拒绝执行：%+v", outs)
	}
}

func TestValidateStep(t *testing.T) {
	ok := []model.Step{
		step("reg_set", "root", "HKLM", "key", facts.KeyPolicy, "name", "egressProxyUrl", "type", "sz", "value", "http://127.0.0.1:43867"),
		step("reg_set", "root", "HKLM", "key", facts.KeyTZAuto, "name", "Start", "type", "dword", "value", "4"),
		step("reg_delete", "root", "HKLM", "key", facts.KeyMachineEnv, "name", "ANTHROPIC_BASE_URL"),
		step("tz_set", "id", "Tokyo Standard Time"),
		step("firewall_add", "name", facts.FirewallRuleID),
	}
	bad := []model.Step{
		step("reg_set", "root", "HKLM", "key", facts.KeyPolicy, "name", "egressProxyUrl", "type", "sz", "value", "http://evil.com:80"),
		step("reg_set", "root", "HKLM", "key", facts.KeyTZAuto, "name", "Start", "type", "dword", "value", "2"),
		step("reg_delete", "root", "HKLM", "key", facts.KeyMachineEnv, "name", "Path"),
		step("tz_set", "id", `x" & calc`),
		step("firewall_add", "name", "other"),
		step("json_env", "path", `C:\x.json`),
	}
	for _, s := range ok {
		if err := validateStep(s); err != nil {
			t.Errorf("应允许 %v：%v", s, err)
		}
	}
	for _, s := range bad {
		if err := validateStep(s); err == nil {
			t.Errorf("应拒绝 %v", s)
		}
	}
}

func TestElevatedArgsOnlyFromBackupDir(t *testing.T) {
	fk := newFake(t)
	if code, ok := HandleElevatedArgs(fk, []string{"--elevated-apply", `C:\Temp\evil`}); !ok || code != 4 {
		t.Errorf("备份目录以外的请求应拒绝：%d %v", code, ok)
	}
	if _, ok := HandleElevatedArgs(fk, []string{"check"}); ok {
		t.Errorf("普通参数不应被当作提权调用")
	}
}

func TestRestoreRejectsBadID(t *testing.T) {
	fk := newFake(t)
	for _, id := range []string{"", "..", `..\..\x`, "a/b"} {
		if r := Restore(fk, id, ""); r.OK {
			t.Errorf("非法编号 %q 应拒绝", id)
		}
	}
}

// ---------------------------------------------------------------- 计划生成

func planFacts(t *testing.T, fk *platform.Fake) *facts.Facts {
	chromeExe := filepath.Join(fk.P.ProgramFiles, "Google", "Chrome", "Application", "chrome.exe")
	write(t, chromeExe, "x")
	args := `--user-data-dir="X" --proxy-server="http://127.0.0.1:7890" --lang=zh-CN --disable-quic`
	b := facts.Browser{Name: "KK-Claude", UserDataDir: filepath.Join(fk.P.LocalAppData, "ClaudeAccountBrowsers", "KK-Claude", "UserData"), Standard: true,
		Shortcuts: []facts.ShortcutInfo{{Shortcut: platform.Shortcut{Path: filepath.Join(fk.P.Desktop, "KK-Claude-专用Chrome.lnk"), Target: `C:\old\chrome.exe`, Args: args}, Flags: chrome.Parse(args)}}}
	f := &facts.Facts{Settings: appconfig.Defaults(), Paths: fk.P, ChromeExe: chromeExe, Browsers: []facts.Browser{b},
		CLI: facts.CLIFacts{Settings: facts.SettingsFile{Env: map[string]string{"NO_PROXY": "example.com", "ANTHROPIC_BASE_URL": "https://r"}},
			LocalSettings: facts.SettingsFile{Env: map[string]string{"HTTPS_PROXY": "http://127.0.0.1:7890"}},
			UserEnv:       map[string]string{}, MachineEnv: map[string]string{"ANTHROPIC_BASE_URL": "https://r"}},
		System: facts.SystemFacts{WindowsTZ: "China Standard Time", AutoTZ: func() *bool { b := true; return &b }()},
		Now:    time.Now()}
	f.Selected = &f.Browsers[0]
	return f
}

func TestPlanGeneration(t *testing.T) {
	fk := newFake(t)
	f := planFacts(t, fk)
	tg := model.Target{Source: "auto", CountryCode: "JP", Country: "日本", IANA: "Asia/Tokyo", WindowsTZ: "Tokyo Standard Time", Lang: "ja-JP", AcceptLang: "ja-JP,ja,en-US,en", Locale: "ja-JP"}
	rs := []model.Result{
		{ID: "B3", Status: model.Fail, FixID: checks.FixBrowserShortcut},
		{ID: "B6", Status: model.Fail, FixID: checks.FixBrowserShortcut},
		{ID: "C2", Status: model.Warn, FixID: checks.FixCLIProxy},
		{ID: "C3", Status: model.Fail, FixID: checks.FixCLIThirdParty},
		{ID: "D2", Status: model.Fail, FixID: checks.FixDesktopPolicy},
		{ID: "S1", Status: model.Warn, FixID: checks.FixTimezone},
		{ID: "S2", Status: model.Warn, FixID: checks.FixRegion},
		{ID: "S4", Status: model.Warn, FixID: checks.FixIPv6},
		{ID: "P3", Status: model.Pass, FixID: "ignored"},
		{ID: "B8", Status: model.Unknown, Pending: true, FixID: checks.FixBrowserShortcut},
	}
	p := Plan(f, tg, rs, Options{})
	got := map[string]model.FixAction{}
	for _, a := range p.Actions {
		got[a.ID] = a
	}
	sc := got[checks.FixBrowserShortcut]
	if len(sc.CheckIDs) != 2 || len(sc.Steps) != 1 {
		t.Fatalf("快捷方式动作不对：%+v", sc)
	}
	args := sc.Steps[0].Params["args"]
	for _, want := range []string{`--proxy-server="socks5://127.0.0.1:43866"`, "--lang=ja-JP", `--accept-lang="ja-JP,ja,en-US,en"`, "--disable-quic", "disable_non_proxied_udp", "ip.net.coffee"} {
		if !strings.Contains(args, want) {
			t.Errorf("新参数缺少 %s：%s", want, args)
		}
	}
	if sc.Steps[0].Params["target"] != f.ChromeExe {
		t.Errorf("Chrome 路径不存在时应改指本机 Chrome")
	}
	cp := got[checks.FixCLIProxy]
	if len(cp.Steps) != 2 || !strings.Contains(cp.Steps[0].Params["set"], "localhost") || cp.Steps[1].Params["delete"] != "HTTPS_PROXY" {
		t.Errorf("CLI 代理计划不对：%+v", cp.Steps)
	}
	tp := got[checks.FixCLIThirdParty]
	if !tp.NeedsAdmin || !tp.DefaultOn {
		t.Errorf("系统级 BASE_URL 需要管理员且默认勾选：%+v", tp)
	}
	tz := got[checks.FixTimezone]
	if !tz.NeedsAdmin || len(tz.Steps) != 2 || tz.Steps[1].Params["id"] != "Tokyo Standard Time" {
		t.Errorf("自动时区开着时应一并关闭：%+v", tz)
	}
	if got[checks.FixRegion].DefaultOn || got[checks.FixIPv6].DefaultOn {
		t.Errorf("区域格式和警告级 IPv6 不应默认勾选")
	}
	if !got[checks.FixDesktopPolicy].NeedsAdmin {
		t.Errorf("Desktop 策略需要管理员")
	}
	if _, ok := got["ignored"]; ok {
		t.Errorf("通过的项不应生成动作")
	}
}

func TestPlanBrowserCreate(t *testing.T) {
	fk := newFake(t)
	f := planFacts(t, fk)
	f.Browsers, f.Selected = nil, nil
	tg := model.Target{Source: "none"}
	rs := []model.Result{{ID: "B1", Status: model.Fail, FixID: checks.FixBrowserCreate}}
	p := Plan(f, tg, rs, Options{Alias: "账号2"})
	if len(p.Actions) != 1 || len(p.Actions[0].Steps) != 2 {
		t.Fatalf("新建计划不对：%+v", p)
	}
	st := p.Actions[0].Steps[1]
	if filepath.Base(st.Params["path"]) != "Claude-账号2-专用Chrome.lnk" || !strings.Contains(st.Params["args"], "--lang=en-US") {
		t.Errorf("快捷方式名或默认语言不对：%+v", st.Params)
	}
	p = Plan(f, tg, rs, Options{Alias: "bad name!"})
	if len(p.Actions) != 0 || len(p.Notes) == 0 {
		t.Errorf("非法名字应拒绝：%+v", p)
	}
	f.ChromeExe = ""
	p = Plan(f, tg, rs, Options{})
	if len(p.Actions) != 0 || !strings.Contains(strings.Join(p.Notes, ""), "Chrome") {
		t.Errorf("没装 Chrome 应提示：%+v", p)
	}
	// 默认名字被占用时自动加序号
	f.ChromeExe = filepath.Join(fk.P.ProgramFiles, "Google", "Chrome", "Application", "chrome.exe")
	if err := os.MkdirAll(filepath.Join(fk.P.LocalAppData, "ClaudeAccountBrowsers", "账号1"), 0o755); err != nil {
		t.Fatal(err)
	}
	p = Plan(f, tg, rs, Options{})
	if !strings.Contains(p.Actions[0].Title, "账号12") {
		t.Errorf("默认名字冲突应加序号：%s", p.Actions[0].Title)
	}
}

// 按项还原：只还原勾选的项，其余不变；再还原剩下的，整次标记已还原。
func TestRestoreItemsPartialThenRest(t *testing.T) {
	fk := newFake(t)
	fk.Admin = true
	settings := filepath.Join(fk.P.Home, ".claude", "settings.json")
	write(t, settings, origSettings)
	fk.SetReg(platform.HKCU, facts.KeyUserEnv, "ANTHROPIC_AUTH_TOKEN", platform.RegValue{Type: "sz", Str: "sk-secret"})
	fk.SetReg(platform.HKLM, facts.KeyTZ, "TimeZoneKeyName", platform.RegValue{Type: "sz", Str: "China Standard Time"})
	var runs []string
	fk.RunFunc = func(name string, args ...string) (string, int, error) {
		runs = append(runs, filepath.Base(name)+" "+strings.Join(args, " "))
		return "", 0, nil
	}
	plan := model.FixPlan{Actions: []model.FixAction{
		{ID: "a1", Title: "代理", Changes: []model.Change{{What: "HTTPS_PROXY", From: "未设置", To: "http://127.0.0.1:43865"}},
			Steps: []model.Step{jsonEnvStep(settings, map[string]string{"HTTPS_PROXY": "http://127.0.0.1:43865"}, nil)}},
		{ID: "a3", Title: "变量", Steps: []model.Step{step("reg_delete", "root", "HKCU", "key", facts.KeyUserEnv, "name", "ANTHROPIC_AUTH_TOKEN")}},
		{ID: "a4", Title: "时区", Changes: []model.Change{{What: "系统时区", From: "China Standard Time", To: "Eastern Standard Time"}},
			Steps: []model.Step{step("tz_set", "id", "Eastern Standard Time")}},
	}}
	res := Apply(fk, plan, []string{"a1", "a3", "a4"}, "self.exe")
	if !res.OK {
		t.Fatalf("执行失败：%+v", res)
	}
	modified := readFile(t, settings)
	b := ListBackups(fk.P.LocalAppData)[0]
	if len(b.Items) != 3 || !b.Items[0].OK || b.Items[0].Changes[0].What != "HTTPS_PROXY" || b.Items[0].RestoredAt != "" {
		t.Fatalf("备份应带逐项清单：%+v", b.Items)
	}

	// 只还原时区
	rr := RestoreItems(fk, res.BackupID, []string{"a4"}, "self.exe")
	if !rr.OK || !strings.Contains(rr.Message, "已还原 1 项") {
		t.Fatalf("按项还原失败：%+v", rr)
	}
	if runs[len(runs)-1] != "tzutil.exe /s China Standard Time" {
		t.Errorf("时区没还原：%v", runs)
	}
	if readFile(t, settings) != modified {
		t.Errorf("没勾的 settings.json 不应被还原")
	}
	if v, _ := fk.RegRead(platform.HKCU, facts.KeyUserEnv, "ANTHROPIC_AUTH_TOKEN"); v.Exists {
		t.Errorf("没勾的用户变量不应被还原")
	}
	b = ListBackups(fk.P.LocalAppData)[0]
	if b.Restored || b.Items[2].RestoredAt == "" || b.Items[0].RestoredAt != "" || b.Items[1].RestoredAt != "" {
		t.Fatalf("只有时区应标记已还原：%+v", b)
	}
	if len(b.Restores) != 1 || len(b.Restores[0].Items) != 1 || b.Restores[0].Items[0] != "a4" {
		t.Errorf("应记下这次按项还原：%+v", b.Restores)
	}

	// 再还原剩下的：时区不再动
	nRuns := len(runs)
	rr = RestoreItems(fk, res.BackupID, nil, "self.exe")
	if !rr.OK || rr.Message != "已全部还原。" {
		t.Fatalf("还原剩下的失败：%+v", rr)
	}
	if len(runs) != nRuns {
		t.Errorf("已还原的时区不应再执行：%v", runs[nRuns:])
	}
	if readFile(t, settings) != origSettings {
		t.Errorf("settings.json 没有逐字节还原")
	}
	if v, _ := fk.RegRead(platform.HKCU, facts.KeyUserEnv, "ANTHROPIC_AUTH_TOKEN"); !v.Exists || v.Str != "sk-secret" {
		t.Errorf("用户变量没还原：%+v", v)
	}
	b = ListBackups(fk.P.LocalAppData)[0]
	if !b.Restored || len(b.Restores) != 2 {
		t.Errorf("全部还原后应整次标记：%+v", b)
	}
	for _, it := range b.Items {
		if it.RestoredAt == "" {
			t.Errorf("每项都应有还原时间：%+v", it)
		}
	}
	if r := RestoreItems(fk, res.BackupID, []string{"nope"}, ""); r.OK || !strings.Contains(r.Message, "没有") {
		t.Errorf("不存在的项应拒绝：%+v", r)
	}
}

// 按项还原跨管理员部分：只还原普通项时不弹 UAC；还原管理员项时提权进程只动选中的项。
func TestRestoreItemsElevated(t *testing.T) {
	fk := newFake(t)
	elevations := 0
	fk.ElevateFunc = func(exe string, args []string) (int, error) {
		elevations++
		code, ok := HandleElevatedArgs(fk, args)
		if !ok {
			t.Fatalf("提权参数没被识别：%v", args)
		}
		return code, nil
	}
	settings := filepath.Join(fk.P.Home, ".claude", "settings.json")
	f := &facts.Facts{Settings: appconfig.Defaults()}
	plan := model.FixPlan{Actions: []model.FixAction{
		*planDesktopPolicy(f),
		{ID: "u", Title: "普通", Steps: []model.Step{jsonEnvStep(settings, map[string]string{"HTTP_PROXY": "x"}, nil)}},
	}}
	res := Apply(fk, plan, []string{checks.FixDesktopPolicy, "u"}, "self.exe")
	if !res.OK || elevations != 1 {
		t.Fatalf("执行失败：%+v", res)
	}
	rr := RestoreItems(fk, res.BackupID, []string{"u"}, "self.exe")
	if !rr.OK || elevations != 1 {
		t.Fatalf("只还原普通项不应提权：%+v（提权 %d 次）", rr, elevations)
	}
	if facts.Exists(settings) {
		t.Errorf("普通项应已还原")
	}
	if v, _ := fk.RegRead(platform.HKLM, facts.KeyPolicy, "egressProxyUrl"); !v.Exists {
		t.Errorf("没勾的策略不应被还原")
	}
	rr = RestoreItems(fk, res.BackupID, []string{checks.FixDesktopPolicy}, "self.exe")
	if !rr.OK || elevations != 2 {
		t.Fatalf("还原管理员项失败：%+v", rr)
	}
	if v, _ := fk.RegRead(platform.HKLM, facts.KeyPolicy, "egressProxyUrl"); v.Exists {
		t.Errorf("策略应已还原")
	}
	if b := ListBackups(fk.P.LocalAppData)[0]; !b.Restored {
		t.Errorf("两项都还原后应整次标记：%+v", b)
	}
	if facts.Exists(filepath.Join(BackupRoot(fk.P.LocalAppData), res.BackupID, requestRestore)) {
		t.Errorf("还原请求文件用完应删除")
	}
}

// v2.2 之前的备份没有逐项清单：按备份记录拼出来，区域格式显示成能看懂的一行。
func TestOldManifestItems(t *testing.T) {
	m := Manifest{Actions: []string{"区域格式与国家改为 en-US / 美国"}, Records: []Record{{ActionID: "sys.region", Op: "region_set",
		Params: map[string]string{"cc": "US", "locale": "en-US"}, Before: map[string]string{"locale": "zh-CN", "nation": "45"}, Applied: true}}}
	items := itemsOf(m, Manifest{})
	if len(items) != 1 || items[0].Title != "区域格式与国家改为 en-US / 美国" || !items[0].OK || items[0].RestoredAt != "" {
		t.Fatalf("旧备份逐项清单不对：%+v", items)
	}
	if c := items[0].Changes; len(c) != 2 || c[0].From != "zh-CN" || c[1].From != "CN" || c[1].To != "US" {
		t.Errorf("区域格式改动描述不对：%+v", c)
	}
	m.Records[0].Restored, m.RestoredAt = true, "2026-10-11 12:50:43"
	if items := itemsOf(m, Manifest{}); items[0].RestoredAt != "2026-10-11 12:50:43" {
		t.Errorf("已还原的旧备份应带还原时间：%+v", items)
	}
	// 令牌这类值不显示
	r := Record{Op: "reg_delete", Params: map[string]string{"name": "ANTHROPIC_AUTH_TOKEN"}, Before: map[string]string{"existed": "true", "value": "sk-secret"}, Applied: true}
	for _, c := range describeRecord(r) {
		if strings.Contains(c.From+c.To, "sk-secret") {
			t.Errorf("不应显示原值：%+v", c)
		}
	}
}
