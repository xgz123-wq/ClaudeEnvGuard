package envscripts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ClaudeEnvGuard/internal/platform"
)

// 测试不跑真脚本：Fake.RunFunc 按参数模拟脚本的输出和它写的结果文件。
func newRunner(t *testing.T) (*Runner, *platform.Fake) {
	t.Helper()
	sys := platform.NewFake(t.TempDir())
	r := New(sys, "")
	for _, p := range []string{r.Deep, r.Launcher} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("# 假脚本"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r, sys
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

const deepJSON = `{"script":"Claude环境一键总检.ps1","scope":"All","ok":false,"pass":2,"warn":1,"fail":1,"manual":1,"results":[
 {"Status":"PASS","Item":"Rule / TUN / Claude 规则","Evidence":"基础验证 18/18"},
 {"Status":"PASS","Item":"国内连通","Evidence":"飞书 HTTP 200"},
 {"Status":"WARN","Item":"Claude Desktop 自动更新","Evidence":"x"},
 {"Status":"FAIL","Item":"Claude Desktop / Code 直连防护","Evidence":"文件基线仅 3/4 一致"},
 {"Status":"MANUAL","Item":"网页最终验收","Evidence":"人工"}]}`

func TestDeepCheckReadsResultFile(t *testing.T) {
	r, sys := newRunner(t)
	var got []string
	sys.RunFunc = func(name string, args ...string) (string, int, error) {
		got = args
		p := argAfter(args, "-ResultPath")
		if p == "" {
			t.Fatalf("应带 -ResultPath：%v", args)
		}
		return "屏幕输出不解析", 1, os.WriteFile(p, []byte("\xef\xbb\xbf"+deepJSON), 0o644)
	}
	res := r.DeepCheck(context.Background())
	if res.Error != "" || res.OK || res.Pass != 2 || res.Fail != 1 || res.Manual != 1 || len(res.Items) != 5 {
		t.Fatalf("结果不对：%+v", res)
	}
	if res.Items[3].Status != "FAIL" || !strings.Contains(res.Items[3].Evidence, "3/4") || !strings.Contains(res.Summary, "1 项失败") {
		t.Errorf("逐项结果不对：%+v", res.Items)
	}
	if argAfter(got, "-File") != r.Deep || strings.Contains(strings.Join(got, " "), "-OpenBrowser") {
		t.Errorf("只读调用总检，不带 -OpenBrowser：%v", got)
	}
}

func TestDeepCheckOldScript(t *testing.T) {
	r, sys := newRunner(t)
	sys.RunFunc = func(name string, args ...string) (string, int, error) {
		return "Claude环境一键总检.ps1: A parameter cannot be found that matches parameter name 'ResultPath'.", 1, nil
	}
	if res := r.DeepCheck(context.Background()); !strings.Contains(res.Error, "还不支持 -ResultPath") {
		t.Errorf("旧脚本应说明原因：%+v", res)
	}
	_ = os.Remove(r.Deep)
	if res := r.DeepCheck(context.Background()); !strings.Contains(res.Error, "找不到总检脚本") {
		t.Errorf("没有脚本应说明：%+v", res)
	}
}

const launcherPass = "[PASS] 固定代理：HKLM 策略 -> http://127.0.0.1:43867\r\n[PASS] 43867 代理连通，出口：1.2.3.4\r\n结论：启动前检查全部通过；本次只检查，没有启动 Claude。\r\n"

func TestLaunchEnglishChecksFirst(t *testing.T) {
	r, sys := newRunner(t)
	lnk := filepath.Join(sys.P.Desktop, LauncherShortcut)
	if err := sys.WriteShortcut(platform.Shortcut{Path: lnk, Target: `C:\Program Files\PowerShell\7\pwsh.exe`, Args: `-NoProfile -File "x\启动Claude英文浏览器.ps1"`}); err != nil {
		t.Fatal(err)
	}
	sys.RunFunc = func(name string, args ...string) (string, int, error) {
		if argAfter(args, "-File") != r.Launcher || args[len(args)-1] != "-CheckOnly" {
			t.Fatalf("应先只检查：%v", args)
		}
		return launcherPass, 0, nil
	}
	res := r.LaunchEnglish(context.Background())
	if !res.OK || !res.Started || len(res.Lines) != 2 {
		t.Fatalf("检查通过应启动：%+v", res)
	}
	last := sys.RunLog[len(sys.RunLog)-1]
	if !strings.HasPrefix(last, `start C:\Program Files\PowerShell\7\pwsh.exe -NoProfile -File "x\启动Claude英文浏览器.ps1"`) {
		t.Errorf("应按快捷方式原样启动：%s", last)
	}
}

func TestLaunchEnglishBlocked(t *testing.T) {
	r, sys := newRunner(t)
	sys.RunFunc = func(name string, args ...string) (string, int, error) {
		return "[PASS] 固定代理：HKLM 策略\n[BLOCK] Claude 专用代理 43867 未开启，已拒绝启动 Claude。\n结论：已拒绝启动 Claude；没有放行真实 IP 直连。\n", 1, nil
	}
	res := r.LaunchEnglish(context.Background())
	if res.OK || res.Started || res.Message != "Claude 专用代理 43867 未开启，已拒绝启动 Claude。" {
		t.Fatalf("没通过不应启动，并说明原因：%+v", res)
	}
	for _, l := range sys.RunLog {
		if strings.HasPrefix(l, "start ") {
			t.Errorf("没通过却启动了：%s", l)
		}
	}
}

func TestLaunchEnglishWithoutShortcut(t *testing.T) {
	r, sys := newRunner(t)
	sys.RunFunc = func(name string, args ...string) (string, int, error) { return launcherPass, 0, nil }
	res := r.LaunchEnglish(context.Background())
	last := sys.RunLog[len(sys.RunLog)-1]
	if !res.Started || !strings.Contains(last, `-NoProfile -File "`+r.Launcher+`"`) {
		t.Errorf("没有快捷方式时用 pwsh 启动同一个脚本：%+v %s", res, last)
	}
}
