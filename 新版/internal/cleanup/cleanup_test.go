package cleanup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/model"
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

func paths(items []model.CleanItem) []string {
	var out []string
	for _, i := range items {
		out = append(out, filepath.Base(i.Path))
	}
	return out
}

func TestCodePlanAndExecute(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	dir := filepath.Join(fk.P.Home, ".claude")
	write(t, filepath.Join(dir, ".credentials.json"), "{}")
	write(t, filepath.Join(dir, ".credentials.json.bak-20260101"), "{}")
	write(t, filepath.Join(dir, "settings.json"), `{"env":{}}`)
	write(t, filepath.Join(dir, "projects", "a.jsonl"), "x")
	cj := filepath.Join(fk.P.Home, ".claude.json")
	write(t, cj, `{"numStartups": 3, "oauthAccount": {"emailAddress": "a@b.c"}, "mcpServers": {"x": {}}, "primaryApiKey": "sk"}`)
	f := &facts.Facts{}
	p := Plan(fk, KindCode, f)
	got := strings.Join(paths(p.Items), ",")
	if got != ".credentials.json,.credentials.json.bak-20260101,.claude.json" || p.Blocked != "" {
		t.Fatalf("清单不对：%s %q", got, p.Blocked)
	}
	res := Execute(fk, p, f)
	if !res.OK {
		t.Fatalf("执行失败：%+v", res)
	}
	if facts.Exists(filepath.Join(dir, ".credentials.json")) || facts.Exists(filepath.Join(dir, ".credentials.json.bak-20260101")) {
		t.Errorf("凭据没删")
	}
	if !facts.Exists(filepath.Join(dir, "settings.json")) || !facts.Exists(filepath.Join(dir, "projects", "a.jsonl")) {
		t.Errorf("删到了清单以外的文件")
	}
	b, _ := os.ReadFile(cj)
	s := string(b)
	if strings.Contains(s, "oauthAccount") || strings.Contains(s, "primaryApiKey") || !strings.Contains(s, "mcpServers") || !strings.Contains(s, `"numStartups": 3`) {
		t.Errorf(".claude.json 处理不对：%s", s)
	}
}

func TestCodeBlockedWhenRunning(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	write(t, filepath.Join(fk.P.Home, ".claude", ".credentials.json"), "{}")
	f := &facts.Facts{CLIRunning: 2}
	p := Plan(fk, KindCode, f)
	if p.Blocked == "" {
		t.Fatal("Claude Code 在运行时应拒绝")
	}
	res := Execute(fk, p, f)
	if res.OK || !facts.Exists(filepath.Join(fk.P.Home, ".claude", ".credentials.json")) {
		t.Errorf("被阻止时不应删除：%+v", res)
	}
}

func TestDesktopAllowlistOnly(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	root := filepath.Join(fk.P.AppData, "Claude")
	write(t, filepath.Join(root, "Cookies"), "c")
	write(t, filepath.Join(root, "Local Storage", "leveldb", "1.log"), "x")
	write(t, filepath.Join(root, "claude_desktop_config.json"), "{}")
	write(t, filepath.Join(root, "extensions", "e.json"), "{}")
	write(t, filepath.Join(fk.P.LocalAppData, "Claude-3p", "IndexedDB", "x"), "x")
	f := &facts.Facts{}
	p := Plan(fk, KindDesktop, f)
	got := strings.Join(paths(p.Items), ",")
	if got != "Cookies,Local Storage,IndexedDB" {
		t.Fatalf("清单不对：%s", got)
	}
	// 篡改清单：混进一个不在白名单的路径
	p.Items = append(p.Items, model.CleanItem{Path: filepath.Join(root, "claude_desktop_config.json")})
	res := Execute(fk, p, f)
	if res.OK || !facts.Exists(filepath.Join(root, "claude_desktop_config.json")) || !facts.Exists(filepath.Join(root, "extensions", "e.json")) {
		t.Errorf("白名单外的文件被删了或没报错：%+v", res)
	}
	if facts.Exists(filepath.Join(root, "Cookies")) {
		t.Errorf("白名单内的没删")
	}
	f.Desktop.Running = true
	if p := Plan(fk, KindDesktop, f); p.Blocked == "" {
		t.Errorf("Desktop 运行时应拒绝")
	}
}

func TestBrowserCleanOnlySelected(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	ud := filepath.Join(fk.P.LocalAppData, "ClaudeAccountBrowsers", "KK", "UserData")
	other := filepath.Join(fk.P.LocalAppData, "ClaudeAccountBrowsers", "账号1", "UserData")
	write(t, filepath.Join(ud, "Default", "Network", "Cookies"), "c")
	write(t, filepath.Join(ud, "Default", "Bookmarks"), "b")
	write(t, filepath.Join(ud, "Profile 1", "Login Data"), "l")
	write(t, filepath.Join(other, "Default", "Network", "Cookies"), "c")
	f := &facts.Facts{Browsers: []facts.Browser{{Name: "KK", UserDataDir: ud}}}
	f.Selected = &f.Browsers[0]
	p := Plan(fk, KindBrowser, f)
	if strings.Join(paths(p.Items), ",") != "Network,Login Data" {
		t.Fatalf("清单不对：%v", paths(p.Items))
	}
	p.Items = append(p.Items, model.CleanItem{Path: filepath.Join(other, "Default", "Network")})
	Execute(fk, p, f)
	if facts.Exists(filepath.Join(ud, "Default", "Network")) || !facts.Exists(filepath.Join(ud, "Default", "Bookmarks")) {
		t.Errorf("选中浏览器清理不对")
	}
	if !facts.Exists(filepath.Join(other, "Default", "Network", "Cookies")) {
		t.Errorf("删到了别的账号浏览器")
	}
	f.Selected.Running = []platform.Process{{PID: 1}}
	if p := Plan(fk, KindBrowser, f); p.Blocked == "" {
		t.Errorf("浏览器运行时应拒绝")
	}
	if p := Plan(fk, KindBrowser, &facts.Facts{}); p.Blocked == "" {
		t.Errorf("没选浏览器应拒绝")
	}
}

func TestUnknownKind(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	if p := Plan(fk, "rm-rf", nil); p.Blocked == "" {
		t.Errorf("未知类型应拒绝")
	}
}

// 清理留痕：记下删了哪些路径、改了哪些字段，不含凭据内容；被拦下没执行时不写。
func TestCleanLeavesLog(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	dir := filepath.Join(fk.P.Home, ".claude")
	write(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"sk-ant-oat-SECRET"}}`)
	cj := filepath.Join(fk.P.Home, ".claude.json")
	write(t, cj, `{"numStartups": 3, "oauthAccount": {"emailAddress": "who@example.com"}}`)
	f := &facts.Facts{}

	blocked := Plan(fk, KindCode, &facts.Facts{CLIRunning: 1})
	if r := Execute(fk, blocked, f); r.LogPath != "" || len(ListLogs(fk.P.LocalAppData)) != 0 {
		t.Fatalf("没执行时不应留痕：%+v", r)
	}

	res := Execute(fk, Plan(fk, KindCode, f), f)
	if !res.OK || res.LogPath == "" {
		t.Fatalf("应写留痕：%+v", res)
	}
	if filepath.Dir(res.LogPath) != LogDir(fk.P.LocalAppData) || !strings.HasPrefix(filepath.Base(res.LogPath), "清理-") {
		t.Errorf("留痕位置不对：%s", res.LogPath)
	}
	raw, _ := os.ReadFile(res.LogPath)
	for _, secret := range []string{"sk-ant-oat-SECRET", "who@example.com", "claudeAiOauth"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("留痕不应含凭据内容 %q：\n%s", secret, raw)
		}
	}
	logs := ListLogs(fk.P.LocalAppData)
	if len(logs) != 1 {
		t.Fatalf("应读到 1 份留痕：%+v", logs)
	}
	lg := logs[0]
	if lg.Kind != KindCode || len(lg.Deleted) != 1 || filepath.Base(lg.Deleted[0].Path) != ".credentials.json" {
		t.Errorf("删除项不对：%+v", lg.Deleted)
	}
	if len(lg.Edited) != 1 || lg.Edited[0].Path != cj || strings.Join(lg.Edited[0].Fields, ",") != "oauthAccount" {
		t.Errorf("改动字段不对：%+v", lg.Edited)
	}
}
