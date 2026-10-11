package facts

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
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
		list, sel := collectBrowsers(sys, s, procs)
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
		n := 0
		for _, d := range []string{p.Desktop, p.PublicDesktop} {
			n += len(listLnk(d, 0))
		}
		n2 := len(listLnk(p.StartMenu, 2)) + len(listLnk(p.CommonStartMenu, 1))
		t.Logf("  桌面 lnk %d，开始菜单 lnk %d", n, n2)
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
