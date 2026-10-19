package facts

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
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
	procs := []platform.Process{
		{PID: 10, Name: "chrome.exe", CmdLine: `chrome.exe --user-data-dir="` + kk + `" --proxy-server=socks5://127.0.0.1:43866`},
		{PID: 11, Name: "chrome.exe", CmdLine: `chrome.exe --type=renderer --user-data-dir="` + kk + `"`},
	}
	list, sel := collectBrowsers(fk, appconfig.Defaults(), procs)
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
	if _, sel := collectBrowsers(fk, s, nil); sel.Name != "账号1" {
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
	Guarded(context.Background(), &mu, errs, "slow", 50*time.Millisecond, func() func() {
		time.Sleep(300 * time.Millisecond)
		return func() { val = 1 }
	})
	if time.Since(start) > 200*time.Millisecond || errs["slow"] != "读取超时" {
		t.Errorf("超时没生效：%v %v", time.Since(start), errs)
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
