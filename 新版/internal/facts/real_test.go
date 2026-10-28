package facts

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/netprobe"
	"ClaudeEnvGuard/internal/platform"
)

// TestRealLocalTiming 在真机上测本地收集各部分耗时。只读，不改任何东西。
// 运行：CEG_REAL=1 go test ./internal/facts -run TestRealLocalTiming -v
func TestRealLocalTiming(t *testing.T) {
	if os.Getenv("CEG_REAL") == "" {
		t.Skip("设置 CEG_REAL=1 才在真机上运行")
	}
	sys := platform.NewWindows()
	s := appconfig.Defaults()
	tm := func(name string, fn func()) {
		st := time.Now()
		fn()
		t.Logf("%-10s %v", name, time.Since(st))
	}
	var procs []platform.Process
	tm("processes", func() { procs, _ = sys.Processes("chrome.exe", "claude.exe") })
	tm("proxy", func() { collectProxy(sys, s, procs) })
	tm("browsers", func() {
		list, sel := collectBrowsers(sys, s, procs, nil)
		for _, b := range list {
			t.Logf("  浏览器 %s 快捷方式 %d 运行 %d", b.Name, len(b.Shortcuts), len(b.Running))
		}
		if sel != nil {
			t.Logf("  选中 %s", sel.Name)
		}
	})
	tm("chrome", func() { t.Log("  ", findChrome(sys)) })
	tm("cli", func() { collectCLI(sys) })
	tm("desktop", func() { collectDesktop(sys, procs) })
	tm("system", func() { collectSystem(sys) })
	tm("guard", func() { collectGuard(sys, s) })
	tm("firewall", func() { collectFirewall(sys) })
	p := sys.Paths()
	tm("lnk-scan", func() {
		t.Logf("  桌面 lnk %d（只扫这里）", len(listLnk(p.Desktop, 0)))
	})
	tm("all", func() { CollectLocal(context.Background(), sys, s) })
}

// TestRealLnkParse 用真机桌面上的快捷方式对比原生解析和 COM 解析，确认两者一致。
func TestRealLnkParse(t *testing.T) {
	if os.Getenv("CEG_REAL") == "" {
		t.Skip("设置 CEG_REAL=1 才在真机上运行")
	}
	sys := platform.NewWindows()
	n, mismatch := 0, 0
	for _, l := range listLnk(sys.Paths().Desktop, 0) {
		b, _ := os.ReadFile(l)
		native, err := platform.ParseLnk(b)
		if err != nil {
			t.Logf("原生解析不了（会退回 COM）：%s：%v", l, err)
			continue
		}
		com, err := sys.ReadShortcutCOM(l)
		if err != nil {
			continue
		}
		n++
		if !strings.EqualFold(native.Args, com.Args) || (native.Target != "" && !strings.EqualFold(expand(native.Target), com.Target)) {
			mismatch++
			t.Errorf("不一致：%s\n 原生 %q %q\n COM  %q %q", l, native.Target, native.Args, com.Target, com.Args)
		}
	}
	t.Logf("比较了 %d 个快捷方式，不一致 %d 个", n, mismatch)
}

func expand(s string) string {
	return os.ExpandEnv(strings.NewReplacer("%ProgramFiles%", os.Getenv("ProgramFiles"), "%LOCALAPPDATA%", os.Getenv("LOCALAPPDATA"), "%SystemRoot%", os.Getenv("SystemRoot"), "%USERPROFILE%", os.Getenv("USERPROFILE")).Replace(s))
}

// TestRealFileSignature 在真机上读 Claude Code CLI 的签名。只读。
// 运行：CEG_REAL=1 go test ./internal/facts -run TestRealFileSignature -v
func TestRealFileSignature(t *testing.T) {
	if os.Getenv("CEG_REAL") == "" {
		t.Skip("设置 CEG_REAL=1 才在真机上运行")
	}
	sys := platform.NewWindows()
	exe := os.ExpandEnv(`${APPDATA}\npm\node_modules\@anthropic-ai\claude-code\bin\claude.exe`)
	st := time.Now()
	sig, err := sys.FileSignature(exe)
	t.Logf("%v %+v（%v）", err, sig, time.Since(st))
	if err != nil || !sig.Valid || sig.Org != AnthropicSignerOrg {
		t.Errorf("CLI 应是有效的 Anthropic 签名")
	}
	self, _ := os.Executable() // 测试程序本身没签名
	sig, _ = sys.FileSignature(self)
	t.Logf("未签名：%+v", sig)
	if sig.Valid {
		t.Errorf("测试程序不该有有效签名")
	}
}

// TestRealNetworkTiming 在真机上测联网阶段每一步的起止时间，找慢在哪一步。只走三个专用端口和系统路由，不改任何东西。
// 运行：CEG_REAL=1 go test ./internal/facts -run TestRealNetworkTiming -v
func TestRealNetworkTiming(t *testing.T) {
	if os.Getenv("CEG_REAL") == "" {
		t.Skip("设置 CEG_REAL=1 才在真机上运行")
	}
	sys := platform.NewWindows()
	f := CollectLocal(context.Background(), sys, appconfig.Defaults())
	start := time.Now()
	n := DefaultNet()
	at := func() string { return time.Since(start).Truncate(10 * time.Millisecond).String() }
	trace, probe, lookup, dns := n.Trace, n.Probe, n.Lookup, n.DNS
	n.Trace = func(ctx context.Context, c *http.Client) (netprobe.Trace, error) {
		s := at()
		r, err := trace(ctx, c)
		t.Logf("trace   %s → %s err=%v", s, at(), err)
		return r, err
	}
	n.Probe = func(ctx context.Context, c *http.Client, u string) (string, string) {
		s := at()
		st, d := probe(ctx, c, u)
		t.Logf("probe   %s → %s %s %s", s, at(), st, d)
		return st, d
	}
	n.Lookup = func(ctx context.Context, c *http.Client, ip string) (netprobe.IPInfo, error) {
		s := at()
		r, err := lookup(ctx, c, ip)
		t.Logf("lookup  %s → %s src=%s err=%v", s, at(), r.Source, err)
		return r, err
	}
	n.DNS = func(ctx context.Context, via, api *http.Client) ([]netprobe.Resolver, error) {
		s := at()
		r, err := dns(ctx, via, api)
		t.Logf("dns     %s → %s n=%d err=%v", s, at(), len(r), err)
		return r, err
	}
	CollectNetwork(context.Background(), sys, f, n, 14*time.Second)
	t.Logf("总计 %s", at())
}
