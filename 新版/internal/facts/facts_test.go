package facts

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/manifest"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/netprobe"
	"ClaudeEnvGuard/internal/platform"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserDiscovery(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	root := StandardRoot(fk.P)
	kk := filepath.Join(root, "KK-Claude", "UserData")
	write(t, filepath.Join(kk, "Local State"), "{}")
	write(t, filepath.Join(kk, "Default", "Preferences"), `{"intl":{"accept_languages":"zh-CN,zh"}}`)
	old := filepath.Join(root, "账号1", "UserData")
	write(t, filepath.Join(old, "Local State"), "{}")
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(old, "Local State"), past, past)
	_ = fk.WriteShortcut(platform.Shortcut{Path: filepath.Join(fk.P.Desktop, "KK-Claude-专用Chrome.lnk"), Target: `C:\chrome.exe`,
		Args: `--user-data-dir="%LOCALAPPDATA%\ClaudeAccountBrowsers\KK-Claude\UserData" --proxy-server="socks5://127.0.0.1:43866"`})
	// 和 Claude 无关的独立配置浏览器：不应列出
	_ = fk.WriteShortcut(platform.Shortcut{Path: filepath.Join(fk.P.Desktop, "NotebookLM.lnk"), Target: `C:\chrome.exe`, Args: `--user-data-dir="D:\nb"`})
	// 标准目录外、但走浏览器专用端口：应列出
	_ = fk.WriteShortcut(platform.Shortcut{Path: filepath.Join(fk.P.Desktop, "工作号.lnk"), Target: `C:\chrome.exe`,
		Args: `--user-data-dir="D:\work" --proxy-server="socks5://127.0.0.1:43866"`})
	// 开始菜单和公共桌面不再扫描：即使名字带 Claude、走专用端口也不列出
	for _, d := range []string{fk.P.StartMenu, fk.P.CommonStartMenu, fk.P.PublicDesktop} {
		_ = fk.WriteShortcut(platform.Shortcut{Path: filepath.Join(d, "Claude-别处.lnk"), Target: `C:\chrome.exe`,
			Args: `--user-data-dir="D:\elsewhere" --proxy-server="socks5://127.0.0.1:43866"`})
	}
	procs := []platform.Process{
		{PID: 10, Name: "chrome.exe", CmdLine: `chrome.exe --user-data-dir="` + kk + `" --proxy-server=socks5://127.0.0.1:43866`},
		{PID: 11, Name: "chrome.exe", CmdLine: `chrome.exe --type=renderer --user-data-dir="` + kk + `"`},
	}
	list, sel := collectBrowsers(fk, appconfig.Defaults(), procs, nil)
	names := []string{}
	for _, b := range list {
		names = append(names, b.Name)
	}
	if strings.Join(names, ",") != "KK-Claude,账号1,工作号" {
		t.Fatalf("发现的浏览器不对：%v", names)
	}
	if sel == nil || sel.Name != "KK-Claude" {
		t.Fatalf("应默认选最近使用的：%+v", sel)
	}
	if len(sel.Shortcuts) != 1 || len(sel.Running) != 1 || sel.Running[0].PID != 10 || sel.PrefLang != "zh-CN,zh" {
		t.Errorf("KK 详情不对：%+v", sel)
	}
	s := appconfig.Defaults()
	s.SelectedBrowser = "账号1"
	if _, sel := collectBrowsers(fk, s, nil, nil); sel.Name != "账号1" {
		t.Errorf("应尊重设置里选中的浏览器")
	}
}

func TestReadSettingsMasksSecrets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	write(t, p, "\xEF\xBB\xBF"+`{"env":{"HTTPS_PROXY":"http://127.0.0.1:43865","ANTHROPIC_AUTH_TOKEN":"sk-ant-0123456789abcdef","MY_TOKEN":"zzz"}}`)
	sf := readSettings(p)
	if sf.Err != "" || sf.Env["HTTPS_PROXY"] != "http://127.0.0.1:43865" {
		t.Fatalf("解析错误：%+v", sf)
	}
	if strings.Contains(sf.Env["ANTHROPIC_AUTH_TOKEN"], "0123456789") {
		t.Errorf("token 没打码：%s", sf.Env["ANTHROPIC_AUTH_TOKEN"])
	}
	if _, ok := sf.Env["MY_TOKEN"]; ok || len(sf.OtherEnvKeys) != 1 {
		t.Errorf("无关变量不应保留值")
	}
	write(t, p, `{"env": [1]}`)
	if sf := readSettings(p); sf.Err == "" {
		t.Errorf("env 不是对象应报错")
	}
	if sf := readSettings(filepath.Join(dir, "none.json")); sf.Exists || sf.Err != "" {
		t.Errorf("文件不存在不是错误")
	}
}

func TestGuardedTimeoutAndPanic(t *testing.T) {
	var mu sync.Mutex
	errs := map[string]string{}
	val := 0
	start := time.Now()
	tm := Guarded(context.Background(), &mu, errs, "slow", 50*time.Millisecond, func() func() {
		time.Sleep(300 * time.Millisecond)
		return func() { val = 1 }
	})
	if time.Since(start) > 200*time.Millisecond || errs["slow"] != "读取超时" {
		t.Errorf("超时没生效：%v %v", time.Since(start), errs)
	}
	if tm.OK || tm.Attempts != 1+CollectorRetries {
		t.Errorf("全部超时应记为失败并跑满重试次数：%+v", tm)
	}
	time.Sleep(350 * time.Millisecond)
	if val != 0 {
		t.Errorf("超时后的结果不应写回")
	}
	Guarded(context.Background(), &mu, errs, "boom", time.Second, func() func() { panic("炸了") })
	if !strings.Contains(errs["boom"], "炸了") {
		t.Errorf("panic 应被捕获：%v", errs)
	}
}

// 第一次超时、第二次很快：用第二次的结果，不报错。
func TestGuardedRetryAfterTimeout(t *testing.T) {
	var mu sync.Mutex
	errs := map[string]string{}
	var calls atomic.Int32
	val := 0
	tm := Guarded(context.Background(), &mu, errs, "cold", 50*time.Millisecond, func() func() {
		n := calls.Add(1)
		if n == 1 {
			time.Sleep(300 * time.Millisecond)
		}
		return func() { val = int(n) }
	})
	if len(errs) != 0 || val != 2 || !tm.OK || tm.Attempts != 2 {
		t.Errorf("重试应成功并写回第二次的结果：errs=%v val=%d %+v", errs, val, tm)
	}
	if !strings.Contains(tm.String(), "重试后") {
		t.Errorf("日志写法应标出重试：%s", tm)
	}
}

// 第一次只是晚到一点、比重试先完成：直接用第一次的结果。
func TestGuardedLateFirstAttemptWins(t *testing.T) {
	var mu sync.Mutex
	errs := map[string]string{}
	var calls atomic.Int32
	val := 0
	start := time.Now()
	tm := Guarded(context.Background(), &mu, errs, "late", 50*time.Millisecond, func() func() {
		n := calls.Add(1)
		if n == 1 {
			time.Sleep(70 * time.Millisecond)
		} else {
			time.Sleep(time.Second)
		}
		return func() { val = int(n) }
	})
	if len(errs) != 0 || val != 1 || !tm.OK || time.Since(start) > 500*time.Millisecond {
		t.Errorf("晚到的第一次应被采用：errs=%v val=%d %+v 用时 %v", errs, val, tm, time.Since(start))
	}
}

func TestCollectLocalOnEmptyMachine(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	f := CollectLocal(context.Background(), fk, appconfig.Defaults())
	if len(f.Errors) != 0 {
		t.Errorf("空机器不应有收集错误：%v", f.Errors)
	}
	if f.CLI.Installed || f.Desktop.Installed || len(f.Browsers) != 0 || f.Proxy.VergeFound {
		t.Errorf("空机器应什么都没检测到：%+v", f)
	}
}

func TestDesktopPolicyAndFirewall(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	fk.Pkg[DesktopFamily] = [2]string{"Claude_2.1.0.0_x64__pzs8sxrjxfjjc", `C:\Program Files\WindowsApps\Claude_2.1.0.0_x64__pzs8sxrjxfjjc`}
	fk.SetReg(platform.HKCU, KeyPolicy, "egressProxyUrl", platform.RegValue{Type: "sz", Str: "http://127.0.0.1:43867/"})
	procs := []platform.Process{{PID: 1, Name: "claude.exe", Exe: `C:\Program Files\WindowsApps\Claude_2.1.0.0_x64__pzs8sxrjxfjjc\app\claude.exe`}}
	d := collectDesktop(fk, procs)
	if !d.Installed || d.Version != "2.1.0.0" || !d.Running {
		t.Errorf("Desktop 信息不对：%+v", d)
	}
	if eg, layer, _ := d.EffectivePolicy(); eg != "http://127.0.0.1:43867" || layer != "HKCU" {
		t.Errorf("生效策略不对：%s %s", eg, layer)
	}
	fk.SetReg(platform.HKLM, KeyFirewall, "{GUID}", platform.RegValue{Type: "sz", Str: "v2.33|Action=Block|Active=TRUE|Dir=Out|RA4=160.79.104.0/255.255.254.0|Name=x|"})
	fw := collectFirewall(fk)
	if !fw.Exists || !fw.Active || !fw.Range || fw.Name != "{guid}" {
		t.Errorf("等价规则应被识别：%+v", fw)
	}
}

func TestCollectNetworkBudgetAndPorts(t *testing.T) {
	f := &Facts{Settings: appconfig.Defaults(), Exits: map[model.Entry]*model.Exit{}, Errors: map[string]string{},
		Proxy: ProxyFacts{Owners: map[int]PortOwner{43866: {Listening: true}, 43865: {Listening: true}}}}
	hang := func(ctx context.Context) { <-ctx.Done() }
	n := Net{
		Client: func(string, time.Duration) *http.Client { return &http.Client{} },
		Trace: func(ctx context.Context, c *http.Client) (netprobe.Trace, error) {
			return netprobe.Trace{IP: "1.2.3.4", Loc: "US"}, nil
		},
		Probe: func(ctx context.Context, c *http.Client, u string) (string, string) {
			hang(ctx)
			return netprobe.ReachFail, "x"
		},
		Lookup: func(ctx context.Context, c *http.Client, ip string) (netprobe.IPInfo, error) {
			return netprobe.IPInfo{}, errors.New("接口挂了")
		},
		DNS:  func(ctx context.Context, via, api *http.Client) ([]netprobe.Resolver, error) { return nil, nil },
		IPv6: func(ctx context.Context) (string, error) { return "", errors.New("无") },
	}
	start := time.Now()
	CollectNetwork(context.Background(), platform.NewFake(t.TempDir()), f, n, 600*time.Millisecond)
	if el := time.Since(start); el > 1500*time.Millisecond {
		t.Fatalf("总时长预算没生效：%v", el)
	}
	if !f.NetworkDone {
		t.Fatalf("应标记联网阶段结束")
	}
	br := f.Exits[model.EntryBrowser]
	if br.IP != "1.2.3.4" || br.Country != "美国" || br.TimeZone != "America/New_York" || !strings.Contains(br.GeoSource, "仅国家") {
		t.Errorf("地理查询失败时应用国家兜底：%+v", br)
	}
	if br.Reach != netprobe.ReachFail || br.ReachDetail == "" {
		t.Errorf("挂住的可达性探测应记为失败：%+v", br)
	}
	if d := f.Exits[model.EntryDesktop]; d.Listening || !strings.Contains(d.Err, "没有程序在监听") {
		t.Errorf("没监听的端口不应探测：%+v", d)
	}
}

func TestCollectNetworkProbeRetry(t *testing.T) {
	f := &Facts{Settings: appconfig.Defaults(), Exits: map[model.Entry]*model.Exit{}, Errors: map[string]string{},
		Proxy: ProxyFacts{Owners: map[int]PortOwner{43865: {Listening: true}}}}
	var mu sync.Mutex
	calls := 0
	n := Net{
		Client: func(string, time.Duration) *http.Client { return &http.Client{} },
		Trace: func(ctx context.Context, c *http.Client) (netprobe.Trace, error) {
			return netprobe.Trace{IP: "1.2.3.4", Loc: "US"}, nil
		},
		Probe: func(ctx context.Context, c *http.Client, u string) (string, string) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return netprobe.ReachFail, "api.anthropic.com：超时"
			}
			return netprobe.ReachOK, "api.anthropic.com：HTTP 401，可达"
		},
		Lookup: func(ctx context.Context, c *http.Client, ip string) (netprobe.IPInfo, error) {
			return netprobe.IPInfo{CountryCode: "US"}, nil
		},
		DNS:  func(ctx context.Context, via, api *http.Client) ([]netprobe.Resolver, error) { return nil, nil },
		IPv6: func(ctx context.Context) (string, error) { return "", errors.New("无") },
	}
	CollectNetwork(context.Background(), platform.NewFake(t.TempDir()), f, n, 10*time.Second)
	cli := f.Exits[model.EntryCLI]
	if calls != 2 || cli.Reach != netprobe.ReachOK || !strings.Contains(cli.ReachDetail, "重试成功") {
		t.Errorf("第一次失败应重试一次并记为可达：calls=%d %+v", calls, cli)
	}
}

func TestHashGuardTargetsSignature(t *testing.T) {
	dir := t.TempDir()
	sys := platform.NewFake(dir)
	sys.Sigs = map[string]platform.Signature{}
	const wrongHash = "0000000000000000000000000000000000000000000000000000000000000000"
	good := platform.Signature{Valid: true, Subject: `CN="Anthropic, PBC", O="Anthropic, PBC", C=US`, Org: "Anthropic, PBC"}
	cases := []struct {
		name, target string
		sig          *platform.Signature
		changed, ok  bool
	}{
		{"放行", "Claude Code npm 启动目标", &good, false, true},
		{"签名方不对", "Claude Code npm 原生包", &platform.Signature{Valid: true, Subject: "CN=Evil Corp, O=Evil Corp", Org: "Evil Corp"}, true, false},
		{"签名无效", "Claude Code npm 原生包", &platform.Signature{Valid: false, Status: "签名校验失败", Subject: good.Subject, Org: good.Org}, true, false},
		{"未签名", "Claude Code npm 启动目标", nil, true, false},
		{"Desktop 不放行", "Claude Desktop 主程序及内置浏览器", &good, true, false},
		{"插件不放行", "Claude Code VS Code 插件", &good, true, false},
	}
	g := GuardFacts{Found: true}
	for _, c := range cases {
		p := filepath.Join(dir, "bin", c.name+".exe")
		write(t, p, "new build "+c.name)
		if c.sig != nil {
			sys.Sigs[p] = *c.sig
		}
		g.Targets = append(g.Targets, GuardTarget{Name: c.target, Path: p, Expected: wrongHash})
	}
	HashGuardTargets(context.Background(), sys, &g)
	if !g.HashChecked {
		t.Fatal("应完成哈希核对")
	}
	for i, c := range cases {
		got := g.Targets[i]
		if got.Changed != c.changed || got.AcceptedBySignature != c.ok {
			t.Errorf("%s：Changed=%v Accepted=%v，期望 %v %v", c.name, got.Changed, got.AcceptedBySignature, c.changed, c.ok)
		}
	}
	if g.Targets[4].Signer != "" {
		t.Errorf("非 CLI 目标不该去读签名：%q", g.Targets[4].Signer)
	}

	// 哈希一致时不看签名
	p := filepath.Join(dir, "bin", "same.exe")
	write(t, p, "same")
	g = GuardFacts{Found: true, Targets: []GuardTarget{{Name: "Claude Code npm 启动目标", Path: p,
		Expected: "0967115F2813A3541EAEF77DE9D9D5773F1C0C04314B0BBFE4FF3B3B1C55B5D5"}}}
	sys.Sigs[p] = good
	HashGuardTargets(context.Background(), sys, &g)
	if g.Targets[0].Changed || g.Targets[0].AcceptedBySignature {
		t.Errorf("哈希一致应直接通过，不算签名放行：%+v", g.Targets[0])
	}
}

// 部署清单登记的浏览器：标记已登记；快捷方式不在桌面时按清单路径读；文件没了记下来；没登记的照常列出。
func TestBrowserDiscoveryWithManifest(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	root := StandardRoot(fk.P)
	kk := filepath.Join(root, "KK-Claude", "UserData")
	gone := filepath.Join(root, "已删图标", "UserData")
	moved := filepath.Join(root, "挪走了", "UserData")
	extra := filepath.Join(root, "没登记", "UserData")
	for _, d := range []string{kk, gone, moved, extra} {
		write(t, filepath.Join(d, "Local State"), "{}")
	}
	args := func(ud string) string {
		return `--user-data-dir="` + ud + `" --proxy-server="socks5://127.0.0.1:43866"`
	}
	kkLnk := filepath.Join(fk.P.Desktop, "KK-Claude-专用Chrome.lnk")
	movedLnk := filepath.Join(fk.P.Home, "图标", "挪走了.lnk")
	_ = fk.WriteShortcut(platform.Shortcut{Path: kkLnk, Target: `C:\chrome.exe`, Args: args(kk)})
	_ = fk.WriteShortcut(platform.Shortcut{Path: movedLnk, Target: `C:\chrome.exe`, Args: args(moved)})
	_ = fk.WriteShortcut(platform.Shortcut{Path: filepath.Join(fk.P.Desktop, "没登记.lnk"), Target: `C:\chrome.exe`, Args: args(extra)})
	m := &manifest.Manifest{Browsers: []manifest.Browser{
		{Alias: "KK-Claude", UserDataDir: kk, Shortcut: kkLnk},
		{Alias: "已删图标", UserDataDir: gone, Shortcut: filepath.Join(fk.P.Desktop, "已删图标.lnk")},
		{Alias: "挪走了", UserDataDir: moved, Shortcut: movedLnk},
	}}
	list, _ := collectBrowsers(fk, appconfig.Defaults(), nil, m)
	got := map[string]Browser{}
	for _, b := range list {
		got[b.Name] = b
	}
	if len(list) != 4 {
		t.Fatalf("应列出 4 个：%v", got)
	}
	if b := got["KK-Claude"]; !b.Registered || len(b.Shortcuts) != 1 || b.MissingShortcut != "" {
		t.Errorf("桌面上的登记浏览器只算一个快捷方式：%+v", b)
	}
	if b := got["已删图标"]; !b.Registered || len(b.Shortcuts) != 0 || b.MissingShortcut == "" {
		t.Errorf("登记的快捷方式没了要记下来：%+v", b)
	}
	if b := got["挪走了"]; !b.Registered || len(b.Shortcuts) != 1 {
		t.Errorf("登记的快捷方式不在桌面时按清单路径读：%+v", b)
	}
	if b := got["没登记"]; b.Registered || len(b.Shortcuts) != 1 {
		t.Errorf("桌面上没登记的照常列出：%+v", b)
	}
}
