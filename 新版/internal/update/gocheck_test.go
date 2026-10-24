package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ClaudeEnvGuard/internal/platform"
)

// 假的三个版本源：Desktop 入口回 307 + Location，npm 回 latest，市场回一组版本（含预发布和别的平台，都不该算）。
type fakeSources struct {
	desktopStatus   int
	desktopLocation string
	npmVersion      string
	marketVersions  string // JSON 数组
	hits            map[string]int
	mu              sync.Mutex // 三个源同时查，请求并发进来
}

func (f *fakeSources) server(t *testing.T) *httptest.Server {
	f.hits = map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.URL.Path]++
		f.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/desktop/"):
			if r.Method != http.MethodGet {
				w.WriteHeader(405) // 和官方入口一样：HEAD 不行，只能 GET
				return
			}
			if f.desktopLocation != "" {
				w.Header().Set("Location", f.desktopLocation)
			}
			w.WriteHeader(f.desktopStatus)
		case r.URL.Path == "/npm/latest":
			_, _ = io.WriteString(w, `{"name":"@anthropic-ai/claude-code","version":"`+f.npmVersion+`"}`)
		case r.URL.Path == "/market":
			b, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || !strings.Contains(string(b), "anthropic.claude-code") {
				w.WriteHeader(400)
				return
			}
			_, _ = io.WriteString(w, `{"results":[{"extensions":[{"versions":`+f.marketVersions+`}]}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const marketLatest310 = `[
 {"version":"2.1.311","targetPlatform":"win32-x64","properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"true"}]},
 {"version":"2.1.312","targetPlatform":"darwin-arm64","properties":[]},
 {"version":"2.1.310","targetPlatform":"win32-x64","properties":[]},
 {"version":"2.1.9","targetPlatform":"win32-x64","properties":[]}
]`

type goEnv struct {
	r       *Runner
	sys     *platform.Fake
	targets map[string]string // 名字 → 路径
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newGoEnv 搭一台「三端都装好、4 个程序都和清单一致」的假电脑。
func newGoEnv(t *testing.T, src *fakeSources) *goEnv {
	t.Helper()
	sys := platform.NewFake(t.TempDir())
	sys.Env["PROCESSOR_ARCHITECTURE"] = "AMD64"
	sys.Pkg[DesktopFamily] = [2]string{"Claude_3.6556.2.0_x64__pzs8sxrjxfjjc", ""}
	r := New(sys, "")
	srv := src.server(t)
	r.Proxy = ""
	r.Src = Sources{
		DesktopRedirect: func(arch string) string { return srv.URL + "/desktop/" + arch },
		NpmLatest:       srv.URL + "/npm/latest",
		Marketplace:     srv.URL + "/market",
	}
	p := sys.P
	mustWrite(t, filepath.Join(p.AppData, "npm", "node_modules", "@anthropic-ai", "claude-code", "package.json"), `{"name":"@anthropic-ai/claude-code","version":"2.1.310"}`)
	plugDir := filepath.Join(p.Home, ".vscode", "extensions", "anthropic.claude-code-2.1.310-win32-x64")
	loc := "/" + strings.ReplaceAll(plugDir, `\`, "/")
	ext := []map[string]any{
		{"identifier": map[string]string{"id": "ms-python.python"}, "version": "1.0.0", "location": map[string]string{"path": "/c:/x"}},
		{"identifier": map[string]string{"id": "Anthropic.claude-code"}, "version": "2.1.310", "location": map[string]string{"path": loc}},
	}
	b, _ := json.Marshal(ext)
	mustWrite(t, filepath.Join(p.Home, ".vscode", "extensions", "extensions.json"), string(b))

	e := &goEnv{r: r, sys: sys, targets: map[string]string{
		"Claude Desktop 主程序及内置浏览器": filepath.Join(p.ProgramFiles, "WindowsApps", "Claude_3.6556.2.0_x64__pzs8sxrjxfjjc", "app", "claude.exe"),
		"Claude Code npm 启动目标":     filepath.Join(p.AppData, "npm", "node_modules", "@anthropic-ai", "claude-code", "bin", "claude.exe"),
		"Claude Code npm 原生包":      filepath.Join(p.AppData, "npm", "node_modules", "@anthropic-ai", "claude-code-win32-x64", "claude.exe"),
		"Claude Code VS Code 插件":   filepath.Join(plugDir, "resources", "native-binary", "claude.exe"),
	}}
	e.writeManifest(t, nil)
	mustWrite(t, r.Script, "# 假脚本：Go 查版本不调用它，但一键更新要它，找不到时界面报 checkfail")
	for name, path := range e.targets {
		mustWrite(t, path, "程序 "+name)
	}
	return e
}

// writeManifest 写防护清单；override 可以把某个目标的登记路径换掉（模拟插件换了目录）。
func (e *goEnv) writeManifest(t *testing.T, override map[string]string) {
	t.Helper()
	type tgt struct{ Name, Path, SHA256 string }
	var list []tgt
	for _, name := range []string{"Claude Desktop 主程序及内置浏览器", "Claude Code npm 启动目标", "Claude Code npm 原生包", "Claude Code VS Code 插件"} {
		path := e.targets[name]
		if o, ok := override[name]; ok {
			path = o
		}
		list = append(list, tgt{name, path, sha("程序 " + name)})
	}
	b, _ := json.Marshal(map[string]any{"PackageFamily": DesktopFamily, "Targets": list})
	mustWrite(t, filepath.Join(filepath.Dir(e.r.Script), "应用过滤目标.json"), string(b))
}

func upToDate() *fakeSources {
	return &fakeSources{desktopStatus: 307, desktopLocation: "https://downloads.claude.ai/releases/win32/x64/3.6556.2/Claude-abc.msix",
		npmVersion: "2.1.310", marketVersions: marketLatest310}
}

func TestGoCheckAllLatest(t *testing.T) {
	src := upToDate()
	e := newGoEnv(t, src)
	rec := e.r.goCheck(context.Background())
	if rec.Status != "checked" {
		t.Fatalf("应查成功：%s %s", rec.Status, rec.Error)
	}
	want := []scriptProduct{
		{Product: "Claude Desktop", Installed: "3.6556.2.0", Latest: "3.6556.2.0", Status: "up_to_date"},
		{Product: "Claude Code CLI", Installed: "2.1.310", Latest: "2.1.310", Status: "up_to_date"},
		{Product: "VS Code Claude Code 插件", Installed: "2.1.310", Latest: "2.1.310", Status: "up_to_date"},
	}
	for i, p := range want {
		if rec.Products[i] != p {
			t.Errorf("第 %d 端不对：%+v，应为 %+v（预发布版和别的平台不算）", i, rec.Products[i], p)
		}
	}
	for _, tg := range rec.Targets {
		if tg.State != "unchanged" {
			t.Errorf("%s 应为 unchanged：%+v", tg.Name, tg)
		}
	}
	if !rec.Timing.Has("查版本") {
		t.Errorf("应记查版本用时")
	}
	if src.hits["/desktop/x64"] != 1 || src.hits["/npm/latest"] != 1 || src.hits["/market"] != 1 {
		t.Errorf("三个源各查一次：%v", src.hits)
	}
	// 接到界面：已是最新
	v := e.r.Check(context.Background())
	if v.Phase != "latest" {
		t.Errorf("界面应显示已是最新：%s %s", v.Phase, v.Error)
	}
	b, err := os.ReadFile(filepath.Join(e.r.CheckDir, "检查记录.json"))
	if err != nil {
		t.Fatalf("应和脚本一样留一份 检查记录.json：%v", err)
	}
	var back scriptRecord
	if err := json.Unmarshal(b, &back); err != nil || back.Status != "checked" || !back.Timing.Has("查版本") || !strings.Contains(string(b), `"Timing": {`) {
		t.Errorf("检查记录.json 应和脚本的格式一样（Timing 是普通对象）：%v\n%s", err, b)
	}
}

func TestGoCheckUpdatesAndTargets(t *testing.T) {
	src := &fakeSources{desktopStatus: 307, desktopLocation: "https://downloads.claude.ai/releases/win32/x64/3.7001.5/Claude-x.msix",
		npmVersion: "2.1.311", marketVersions: strings.Replace(marketLatest310, `"2.1.310"`, `"2.1.313"`, 1)}
	e := newGoEnv(t, src)
	e.sys.Sigs = map[string]platform.Signature{}
	// CLI 原地自更新且有 Anthropic 签名 → changed_signed；Desktop 主程序被换成没签名的 → changed_unsigned；
	// 原生包不见了 → missing；清单里插件还登记在旧目录 → moved
	mustWrite(t, e.targets["Claude Code npm 启动目标"], "新版 CLI")
	e.sys.Sigs[e.targets["Claude Code npm 启动目标"]] = platform.Signature{Valid: true, Org: "Anthropic, PBC"}
	mustWrite(t, e.targets["Claude Desktop 主程序及内置浏览器"], "被改过")
	e.sys.Sigs[e.targets["Claude Desktop 主程序及内置浏览器"]] = platform.Signature{Valid: true, Org: "Someone Else"}
	_ = os.Remove(e.targets["Claude Code npm 原生包"])
	old := strings.Replace(e.targets["Claude Code VS Code 插件"], "2.1.310", "2.1.309", 1)
	e.writeManifest(t, map[string]string{"Claude Code VS Code 插件": old})

	rec := e.r.goCheck(context.Background())
	if rec.Status != "checked" {
		t.Fatalf("应查成功：%s", rec.Error)
	}
	if p := rec.Products[0]; p.Latest != "3.7001.5.0" || p.Status != "update_available" {
		t.Errorf("Desktop 三段版本要补成四段再比：%+v", p)
	}
	if p := rec.Products[1]; p.Latest != "2.1.311" || p.Status != "update_available" {
		t.Errorf("CLI：%+v", p)
	}
	if p := rec.Products[2]; p.Latest != "2.1.313" || p.Status != "update_available" {
		t.Errorf("插件：%+v", p)
	}
	got := map[string]scriptTarget{}
	for _, tg := range rec.Targets {
		got[tg.Name] = tg
	}
	if got["Claude Code npm 启动目标"].State != "changed_signed" || got["Claude Desktop 主程序及内置浏览器"].State != "changed_unsigned" ||
		got["Claude Code npm 原生包"].State != "missing" || got["Claude Code VS Code 插件"].State != "moved" ||
		!strings.EqualFold(got["Claude Code VS Code 插件"].NewPath, e.targets["Claude Code VS Code 插件"]) {
		t.Errorf("4 个程序的状态不对：%+v", got)
	}
	// 接到界面：有受保护程序异常时先别更新
	if v := e.r.Check(context.Background()); v.Phase != "unsafe" {
		t.Errorf("没签名的变化应拦下：%s", v.Phase)
	}
}

func TestGoCheckErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *goEnv, s *fakeSources)
		want  string
		tier  string
	}{
		{"入口没跳转", func(e *goEnv, s *fakeSources) { s.desktopStatus, s.desktopLocation = 200, "" }, "官方入口没有返回跳转，状态码：200", TierRetry},
		{"跳到别的域名", func(e *goEnv, s *fakeSources) { s.desktopLocation = "https://evil.example.com/3.7000.1/x.msix" }, "跳转地址不在 claude.ai 的 HTTPS 下", TierAI},
		{"跳转没版本号", func(e *goEnv, s *fakeSources) { s.desktopLocation = "https://claude.ai/download/latest.msix" }, "跳转地址里没有版本号", TierAI},
		{"npm 回的不是版本号", func(e *goEnv, s *fakeSources) { s.npmVersion = "next" }, "npm view 没有返回版本号", TierRetry},
		{"市场没有正式版", func(e *goEnv, s *fakeSources) {
			s.marketVersions = `[{"version":"2.1.400","targetPlatform":"win32-x64","properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"true"}]}]`
		},
			"官方市场没有返回 win32-x64 版本。", TierRetry},
		{"市场报错", func(e *goEnv, s *fakeSources) { s.marketVersions = "坏" }, "官方市场无响应", TierRetry},
		{"没装 Desktop", func(e *goEnv, s *fakeSources) { delete(e.sys.Pkg, DesktopFamily) }, "当前用户 Claude 安装包不唯一或不存在", TierAI},
		{"架构不一致", func(e *goEnv, s *fakeSources) { e.sys.Env["PROCESSOR_ARCHITECTURE"] = "ARM64" }, "已装包架构 x64 与系统架构 arm64 不一致", TierAI},
		{"插件登记两份", func(e *goEnv, s *fakeSources) {
			p := filepath.Join(e.sys.P.Home, ".vscode", "extensions", "extensions.json")
			mustWrite(t, p, `[{"identifier":{"id":"anthropic.claude-code"},"version":"1"},{"identifier":{"id":"anthropic.claude-code"},"version":"2"}]`)
		}, "extensions.json 登记的 Claude Code 插件数量为 2，预期 1", TierAI},
		{"没装 CLI", func(e *goEnv, s *fakeSources) {
			_ = os.RemoveAll(filepath.Join(e.sys.P.AppData, "npm", "node_modules", "@anthropic-ai", "claude-code", "package.json"))
		}, "没有找到全局 CLI", TierAI},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := upToDate()
			e := newGoEnv(t, src)
			c.setup(e, src)
			rec := e.r.goCheck(context.Background())
			if rec.Status != "failed" || !strings.Contains(rec.Error, c.want) {
				t.Fatalf("应报「%s」：%s %s", c.want, rec.Status, rec.Error)
			}
			v := e.r.Check(context.Background())
			if v.Phase != "checkfail" || v.Tier != c.tier {
				t.Errorf("界面应为 checkfail/%s：%s/%s %s", c.tier, v.Phase, v.Tier, v.Error)
			}
		})
	}
}

func TestCompareVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"2.1.310", "2.1.309", 1}, {"2.1.9", "2.1.10", -1}, {"3.6556.2.0", "3.6556.2.0", 0}, {"2.1.310", "2.1.310.0", -1}, {"10.0", "9.9.9", 1}} {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%s, %s) = %d，应为 %d", c.a, c.b, got, c.want)
		}
	}
}
