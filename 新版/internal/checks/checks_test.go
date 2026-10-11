package checks

import (
	"strings"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/chrome"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

func ptrB(b bool) *bool     { return &b }
func ptrI(n int) *int       { return &n }
func ptrU(n uint32) *uint32 { return &n }

const goodArgs = `--user-data-dir="C:\U\L\ClaudeAccountBrowsers\KK\UserData" --proxy-server="socks5://127.0.0.1:43866" --host-resolver-rules="MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost" --lang=en-US --accept-lang="en-US,en" --force-webrtc-ip-handling-policy=disable_non_proxied_udp https://ip.net.coffee/claude/`

// goodFacts 构造一台按分享包配好的机器（出口美国东部）。
func goodFacts(t *testing.T) *facts.Facts {
	t.Helper()
	s := appconfig.Defaults()
	chromeExe := t.TempDir() + `\chrome.exe`
	writeFile(t, chromeExe)
	exit := func(e model.Entry, port int, dns bool) *model.Exit {
		ex := &model.Exit{Entry: e, Port: port, Listening: true, IP: "198.51.100.10", CountryCode: "US", Country: "美国", City: "Ashburn",
			TimeZone: "America/New_York", TrustScore: ptrI(100), Residential: ptrB(true), Reach: "ok"}
		if dns {
			ex.DNS = &model.DNSLeak{Tested: true, Resolvers: []model.Resolver{{IP: "172.70.33.185", CountryCode: "US"}}}
		}
		return ex
	}
	b := facts.Browser{Name: "KK", UserDataDir: `C:\U\L\ClaudeAccountBrowsers\KK\UserData`, Standard: true, DirExists: true,
		Shortcuts: []facts.ShortcutInfo{{Shortcut: platform.Shortcut{Path: `C:\U\Desktop\KK-Claude-专用Chrome.lnk`, Target: chromeExe, Args: goodArgs}, Flags: chrome.Parse(goodArgs)}}}
	f := &facts.Facts{
		Settings: s, Now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), NetworkDone: true,
		Paths: platform.Paths{LocalAppData: `C:\U\L`, Desktop: `C:\U\Desktop`, Home: `C:\U`},
		Proxy: facts.ProxyFacts{Owners: map[int]facts.PortOwner{
			43865: {Port: 43865, Listening: true, Name: "verge-mihomo"}, 43866: {Port: 43866, Listening: true, Name: "verge-mihomo"}, 43867: {Port: 43867, Listening: true, Name: "verge-mihomo"}},
			VergeFound: true, TunMode: ptrB(true), SystemProxy: ptrB(false), MixedPort: 43865, Listeners: map[string]bool{"account-us-socks": true, "claude-desktop-http": true}},
		Browsers:  []facts.Browser{b},
		ChromeExe: chromeExe,
		CLI: facts.CLIFacts{Installed: true, Exe: `C:\npm\claude.exe`, Version: "2.1.296",
			Settings: facts.SettingsFile{Exists: true, Env: map[string]string{"HTTPS_PROXY": "http://127.0.0.1:43865", "HTTP_PROXY": "http://127.0.0.1:43865",
				"ALL_PROXY": "http://127.0.0.1:43865", "NO_PROXY": "localhost,127.0.0.1,*.local", "DISABLE_AUTOUPDATER": "1"}},
			UserEnv: map[string]string{}, MachineEnv: map[string]string{}},
		Desktop: facts.DesktopFacts{Installed: true, FullName: "Claude_2.31226.1.0_x64__pzs8sxrjxfjjc", Version: "2.31226.1.0",
			HKLM: facts.PolicyLayer{Name: "HKLM", HasEgress: true, Egress: "http://127.0.0.1:43867", DisableAU: ptrU(1)}},
		System:    facts.SystemFacts{WindowsTZ: "Eastern Standard Time", AutoTZ: ptrB(false), Locale: "en-US", GeoName: "US", UILang: "en-US"},
		Firewall:  facts.FirewallFacts{Read: true, Exists: true, Name: "x", Active: true, Block: true, Out: true, Range: true},
		IPv6:      facts.IPv6Facts{Tested: true, DirectErr: "超时"},
		SystemDNS: &model.DNSLeak{Tested: true, Resolvers: []model.Resolver{{IP: "1.1.1.1", CountryCode: "US"}}},
		Exits: map[model.Entry]*model.Exit{
			model.EntryBrowser: exit(model.EntryBrowser, 43866, true), model.EntryCLI: exit(model.EntryCLI, 43865, false), model.EntryDesktop: exit(model.EntryDesktop, 43867, true)},
		Errors: map[string]string{},
	}
	f.Selected = &f.Browsers[0]
	return f
}

func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := writeBytes(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func eval(f *facts.Facts) ([]model.Result, model.Target) {
	t := ResolveTarget(f)
	return Evaluate(f, t), t
}

func byID(rs []model.Result) map[string]model.Result {
	m := map[string]model.Result{}
	for _, r := range rs {
		m[r.ID] = r
	}
	return m
}

func TestGoodMachineAllPass(t *testing.T) {
	f := goodFacts(t)
	rs, tg := eval(f)
	if tg.Source != "auto" || tg.CountryCode != "US" || tg.WindowsTZ != "Eastern Standard Time" {
		t.Fatalf("目标推断错误：%+v", tg)
	}
	for _, r := range rs {
		if r.Status != model.Pass && r.Status != model.NA {
			t.Errorf("%s %s 应通过，实际 %s：%s", r.ID, r.Title, r.Status, r.Detail)
		}
	}
	s := Summarize(rs)
	if s.Overall != model.Pass || s.Headline != "全部通过" || s.NextStep != "" {
		t.Errorf("汇总错误：%+v", s)
	}
	if len(rs) < 28 {
		t.Errorf("检查项数量不对：%d", len(rs))
	}
}

func TestFreshMachine(t *testing.T) {
	f := &facts.Facts{Settings: appconfig.Defaults(), Now: time.Now(), NetworkDone: true,
		Proxy: facts.ProxyFacts{Owners: map[int]facts.PortOwner{}}, Exits: map[model.Entry]*model.Exit{
			model.EntryBrowser: {Entry: model.EntryBrowser, Port: 43866}, model.EntryCLI: {Entry: model.EntryCLI, Port: 43865}, model.EntryDesktop: {Entry: model.EntryDesktop, Port: 43867}},
		System:   facts.SystemFacts{WindowsTZ: "China Standard Time", Locale: "zh-CN", GeoName: "CN", UILang: "zh-CN"},
		Firewall: facts.FirewallFacts{Read: true}, Errors: map[string]string{}, ChromeExe: `C:\chrome.exe`}
	rs, tg := eval(f)
	m := byID(rs)
	if tg.Source != "none" {
		t.Errorf("没测到出口时目标应为 none：%+v", tg)
	}
	if m["P1"].Status != model.Fail {
		t.Errorf("P1 应失败：%+v", m["P1"])
	}
	if m["B1"].Status != model.Fail || m["B1"].FixID != FixBrowserCreate {
		t.Errorf("B1 应失败并可新建：%+v", m["B1"])
	}
	for _, id := range []string{"C1", "C2", "D1", "D2", "B3"} {
		if m[id].Status != model.NA {
			t.Errorf("%s 应不适用：%s", id, m[id].Status)
		}
	}
	for _, id := range []string{"S1", "S2", "S3", "B6"} {
		if id == "B6" {
			continue
		}
		if m[id].Status != model.Unknown {
			t.Errorf("没有目标时 %s 应为未知，实际 %s", id, m[id].Status)
		}
	}
	if m["S7"].Status != model.Warn || m["S7"].FixID != FixFirewall || !m["S7"].Advanced {
		t.Errorf("S7 应为可选警告：%+v", m["S7"])
	}
	s := Summarize(rs)
	if s.Overall != model.Fail || !strings.Contains(s.NextStep, "代理客户端") {
		t.Errorf("下一步应先处理代理客户端：%+v", s)
	}
}

func TestNoChromeNoFixOffered(t *testing.T) {
	f := goodFacts(t)
	f.Browsers, f.Selected, f.ChromeExe = nil, nil, ""
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["B1"].FixID != "" || !strings.Contains(m["B1"].Advice, "安装 Google Chrome") {
		t.Errorf("没装 Chrome 时不应提供一键新建：%+v", m["B1"])
	}
}

func TestPendingNeverPasses(t *testing.T) {
	f := goodFacts(t)
	f.NetworkDone = false
	f.Exits = map[model.Entry]*model.Exit{}
	f.IPv6 = facts.IPv6Facts{Global: []string{"2408::1"}}
	f.SystemDNS = nil
	rs, _ := eval(f)
	for _, r := range rs {
		if r.Pending && r.Status == model.Pass {
			t.Errorf("%s 检测中却显示通过", r.ID)
		}
	}
	m := byID(rs)
	for _, id := range []string{"P3", "P4", "P5", "B8", "C6", "D4", "S4", "S5"} {
		if !m[id].Pending || m[id].Status != model.Unknown {
			t.Errorf("%s 应为检测中/未知：%+v", id, m[id])
		}
	}
	if m["S1"].Status != model.Unknown || !m["S1"].Pending {
		t.Errorf("没测到出口时随 IP 的 S1 应等待：%+v", m["S1"])
	}
}

func TestOfflineManualTarget(t *testing.T) {
	f := goodFacts(t)
	f.NetworkDone, f.Offline = false, true
	f.Exits = map[model.Entry]*model.Exit{}
	f.Settings.RegionOverride = "us-west"
	rs, tg := eval(f)
	if tg.Source != "manual" || tg.WindowsTZ != "Pacific Standard Time" {
		t.Fatalf("手选目标错误：%+v", tg)
	}
	m := byID(rs)
	if m["S1"].Status != model.Warn || m["S1"].FixID != FixTimezone {
		t.Errorf("东部时区对西部目标应警告并可修复：%+v", m["S1"])
	}
	if m["P3"].Status != model.Unknown || m["P3"].Pending {
		t.Errorf("离线时联网项应为未知且不是检测中：%+v", m["P3"])
	}
}

func TestCollectorErrorBecomesUnknown(t *testing.T) {
	f := goodFacts(t)
	f.Errors["browsers"] = "读取超时"
	rs, _ := eval(f)
	for _, r := range rs {
		if r.Group == model.GroupBrowser && (r.Status != model.Unknown || r.FixID != "") {
			t.Errorf("收集器出错时 %s 应为未知且不给修复：%+v", r.ID, r)
		}
	}
}

func TestBrowserFlags(t *testing.T) {
	f := goodFacts(t)
	bad := `--user-data-dir="C:\U\L\ClaudeAccountBrowsers\KK\UserData" --proxy-server="http://127.0.0.1:7890" --lang=zh-CN`
	f.Browsers[0].Shortcuts[0].Args = bad
	f.Browsers[0].Shortcuts[0].Flags = chrome.Parse(bad)
	f.Selected = &f.Browsers[0]
	m := byID(Evaluate(f, ResolveTarget(f)))
	for _, id := range []string{"B3", "B4", "B5", "B6"} {
		if m[id].Status != model.Fail || m[id].FixID != FixBrowserShortcut {
			t.Errorf("%s 应失败并可修复：%+v", id, m[id])
		}
	}
}

func TestBrowserRunningWithoutFlags(t *testing.T) {
	f := goodFacts(t)
	f.Browsers[0].Running = []platform.Process{{PID: 42, Name: "chrome.exe", CmdLine: `chrome.exe --user-data-dir="C:\U\L\ClaudeAccountBrowsers\KK\UserData"`}}
	f.Selected = &f.Browsers[0]
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["B7"].Status != model.Fail || !strings.Contains(m["B7"].Detail, "42") {
		t.Errorf("绕开快捷方式打开应失败：%+v", m["B7"])
	}
}

func TestDNSLeakToChina(t *testing.T) {
	f := goodFacts(t)
	f.Exits[model.EntryBrowser].DNS = &model.DNSLeak{Tested: true, Resolvers: []model.Resolver{{IP: "114.114.114.114", CountryCode: "CN"}}}
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["B4"].Status != model.Fail || !strings.Contains(m["B4"].Detail, "中国大陆") {
		t.Errorf("代理那头用中国 DNS 应失败：%+v", m["B4"])
	}
}

func TestLangVerdicts(t *testing.T) {
	jp := model.Target{Lang: "ja-JP", Country: "日本"}
	us := model.Target{Lang: "en-US", Country: "美国"}
	cases := []struct {
		cur  string
		t    model.Target
		want model.Status
	}{{"en-US", jp, model.Pass}, {"ja-JP", jp, model.Pass}, {"zh-CN", us, model.Fail}, {"de-DE", us, model.Warn}, {"", us, model.Warn}, {"en-GB", us, model.Pass}}
	for _, c := range cases {
		if got, _ := langVerdict(c.cur, c.t); got != c.want {
			t.Errorf("langVerdict(%q, %s) = %s，期望 %s", c.cur, c.t.Lang, got, c.want)
		}
	}
}

func TestExitsSplitAndUnsupported(t *testing.T) {
	f := goodFacts(t)
	f.Exits[model.EntryCLI].IP, f.Exits[model.EntryCLI].CountryCode = "1.2.3.4", "HK"
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["P3"].Status != model.Fail {
		t.Errorf("不同国家出口应失败：%+v", m["P3"])
	}
	if m["P4"].Status != model.Fail || !strings.Contains(m["P4"].Detail, "香港") {
		t.Errorf("香港出口应判为不支持：%+v", m["P4"])
	}
	f = goodFacts(t)
	f.Exits[model.EntryCLI].IP = "198.51.100.11"
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["P3"].Status != model.Warn {
		t.Errorf("同国家不同 IP 应警告：%+v", m["P3"])
	}
}

func TestReachClassification(t *testing.T) {
	f := goodFacts(t)
	f.Exits[model.EntryBrowser].Reach = "challenge"
	f.Exits[model.EntryCLI].Reach = "challenge"
	f.Exits[model.EntryDesktop].Reach = "blocked"
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["B8"].Status != model.Pass {
		t.Errorf("claude.ai 人机验证应视为可达：%+v", m["B8"])
	}
	if m["C6"].Status != model.Warn {
		t.Errorf("API 出现人机验证应警告：%+v", m["C6"])
	}
	if m["D4"].Status != model.Fail {
		t.Errorf("地区封锁应失败：%+v", m["D4"])
	}
}

func TestTimezoneAndAutoTZ(t *testing.T) {
	f := goodFacts(t)
	f.System.WindowsTZ = "China Standard Time"
	f.System.AutoTZ = ptrB(true)
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["S1"].Status != model.Warn || m["S1"].FixID != FixTimezone || !strings.Contains(m["S1"].Detail, "自动设置时区") {
		t.Errorf("时区不符应警告并提示自动时区：%+v", m["S1"])
	}
	// 时差相同但 Windows ID 不同（如 US Eastern Standard Time）也算通过
	f = goodFacts(t)
	f.System.WindowsTZ = "US Eastern Standard Time"
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["S1"].Status != model.Pass {
		t.Errorf("同时差应通过：%+v", m["S1"])
	}
}

func TestIPv6Leak(t *testing.T) {
	f := goodFacts(t)
	f.IPv6 = facts.IPv6Facts{Tested: true, DirectIP: "2408:8207::1", DirectCC: "CN", Global: []string{"2408:8207::1"}}
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["S4"].Status != model.Fail || m["S4"].FixID != FixIPv6 {
		t.Errorf("IPv6 绕过代理应失败：%+v", m["S4"])
	}
	f.IPv6.DirectCC = "US"
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["S4"].Status != model.Pass {
		t.Errorf("IPv6 也走出口应通过：%+v", m["S4"])
	}
}

func TestDesktopPolicy(t *testing.T) {
	f := goodFacts(t)
	f.Desktop.HKLM = facts.PolicyLayer{Name: "HKLM"}
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["D2"].Status != model.Fail || m["D2"].FixID != FixDesktopPolicy {
		t.Errorf("没有策略应失败：%+v", m["D2"])
	}
	f.Desktop.HKLM = facts.PolicyLayer{Name: "HKLM", HasEgress: true, Egress: "http://127.0.0.1:43867"}
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["D2"].Status != model.Fail || !strings.Contains(m["D2"].Detail, "同一层") {
		t.Errorf("缺 disableAutoUpdates 应失败：%+v", m["D2"])
	}
	f.Desktop.HKLM = facts.PolicyLayer{Name: "HKLM"}
	f.Desktop.LocalFound, f.Desktop.LocalEgress = true, "http://127.0.0.1:43867"
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["D2"].Status != model.Pass {
		t.Errorf("本地配置方式应通过：%+v", m["D2"])
	}
	f.Desktop = facts.DesktopFacts{}
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["D2"].Status != model.NA {
		t.Errorf("没装 Desktop 应不适用：%+v", m["D2"])
	}
}

func TestCLIProblems(t *testing.T) {
	f := goodFacts(t)
	f.CLI.Settings.Env["ANTHROPIC_BASE_URL"] = "https://relay.example.com"
	f.CLI.UserEnv["ANTHROPIC_AUTH_TOKEN"] = "sk-…xx"
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["C3"].Status != model.Fail || m["C3"].FixID != FixCLIThirdParty {
		t.Errorf("BASE_URL 应失败：%+v", m["C3"])
	}
	f = goodFacts(t)
	f.CLI.Settings.Err = "JSON 格式错误"
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["C2"].Status != model.Unknown || m["C2"].FixID != "" {
		t.Errorf("settings 损坏应为未知且不给修复：%+v", m["C2"])
	}
	f = goodFacts(t)
	f.CLI.Settings.Env["HTTPS_PROXY"] = "socks5://127.0.0.1:43865"
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["C2"].Status != model.Fail {
		t.Errorf("代理地址不对应失败：%+v", m["C2"])
	}
	f = goodFacts(t)
	delete(f.CLI.Settings.Env, "HTTPS_PROXY")
	delete(f.CLI.Settings.Env, "HTTP_PROXY")
	f.CLI.UserEnv["HTTPS_PROXY"] = "http://127.0.0.1:7890"
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["C2"].Status != model.Warn || m["C4"].Status != model.Warn || m["C4"].FixID != FixCLIEnvProxy {
		t.Errorf("没设代理且全局变量指向别处：C2=%+v C4=%+v", m["C2"], m["C4"])
	}
}

func TestGuardEvidence(t *testing.T) {
	f := goodFacts(t)
	f.Guard = facts.GuardFacts{Found: true, Status: "installed_x", ManifestOK: true, ManifestVersion: "2.31226.1.0", HashChecked: true,
		Targets: []facts.GuardTarget{{Name: "Desktop", Changed: true}}}
	m := byID(Evaluate(f, ResolveTarget(f)))
	if m["D3"].Status != model.Warn || !strings.Contains(m["D3"].Detail, "对不上") {
		t.Errorf("程序更新后应提示同步防护：%+v", m["D3"])
	}
	f.Guard.Targets[0].Changed = false
	m = byID(Evaluate(f, ResolveTarget(f)))
	if m["D3"].Status != model.Pass {
		t.Errorf("证据一致应通过：%+v", m["D3"])
	}
	if m["C5"].Status != model.Pass {
		t.Errorf("装了防护且关了自动更新应通过：%+v", m["C5"])
	}
}
