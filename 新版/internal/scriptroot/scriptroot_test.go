package scriptroot

import (
	"os"
	"path/filepath"
	"testing"

	"ClaudeEnvGuard/internal/manifest"
	"ClaudeEnvGuard/internal/platform"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeShare 在 dir 下放分享包标志文件；guarded 时再放直连防护状态文件。
func makeShare(t *testing.T, dir string, guarded bool) {
	t.Helper()
	touch(t, filepath.Join(DocDir(dir), "README.md"))
	if guarded {
		touch(t, filepath.Join(DocDir(dir), "Desktop防护", "应用直连防护状态.json"))
	}
}

func writeManifest(t *testing.T, la, root string) {
	t.Helper()
	p := manifest.Path(la)
	touch(t, p)
	b := []byte(`{"格式版本":1,"端口":{"通用":43865,"浏览器":43866,"Desktop":43867},"脚本根目录":` + quote(root) + `,"专用浏览器":[]}`)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func quote(s string) string {
	out := `"`
	for _, r := range s {
		if r == '\\' || r == '"' {
			out += `\`
		}
		out += string(r)
	}
	return out + `"`
}

func TestFindOrder(t *testing.T) {
	p := platform.NewFake(t.TempDir()).P
	rel := filepath.Join("docs", "a.ps1")
	active := filepath.Join(p.Desktop, "AI应用开发")
	share := filepath.Join(p.Desktop, "Claude环境分享版")
	listed := filepath.Join(p.Home, "Tools", "Claude环境")
	custom := filepath.Join(p.Home, "别处", "分享包")

	if got := Find(p, custom, rel); got != filepath.Join(custom, rel) {
		t.Errorf("都没有时返回第一个候选（设置里的目录）：%s", got)
	}
	if got := Find(p, "", rel); got != filepath.Join(active, rel) {
		t.Errorf("都没有、也没设置时返回桌面\\AI应用开发：%s", got)
	}
	makeShare(t, share, false)
	if got := Find(p, custom, rel); got != filepath.Join(share, rel) {
		t.Errorf("只有桌面分享包时用它：%s", got)
	}
	makeShare(t, active, false)
	if got := Find(p, custom, rel); got != filepath.Join(active, rel) {
		t.Errorf("AI应用开发 排在桌面分享包前：%s", got)
	}
	makeShare(t, listed, false)
	writeManifest(t, p.LocalAppData, listed)
	if got := Find(p, custom, rel); got != filepath.Join(listed, rel) {
		t.Errorf("部署清单的脚本根目录排在桌面候选前：%s", got)
	}
	makeShare(t, custom, false)
	if got := Find(p, custom, rel); got != filepath.Join(custom, rel) {
		t.Errorf("设置里手填的最优先：%s", got)
	}
}

func TestStaleManifestFallsBack(t *testing.T) {
	p := platform.NewFake(t.TempDir()).P
	active := filepath.Join(p.Desktop, "AI应用开发")
	makeShare(t, active, false)
	writeManifest(t, p.LocalAppData, filepath.Join(p.Home, "已经搬走了"))
	if got := Root(p, ""); got != active {
		t.Errorf("清单里的目录不存在时继续往下找：%s", got)
	}
	_ = os.WriteFile(manifest.Path(p.LocalAppData), []byte("{坏了"), 0o644)
	if got := Root(p, ""); got != active {
		t.Errorf("清单读坏时继续往下找：%s", got)
	}
}

func TestScanPrefersGuarded(t *testing.T) {
	p := platform.NewFake(t.TempDir()).P
	if got := Root(p, ""); got != "" {
		t.Errorf("什么都没有时返回空：%s", got)
	}
	plain := filepath.Join(p.Desktop, "A-副本")
	guarded := filepath.Join(p.Home, "Documents", "Z-分享包")
	makeShare(t, plain, false)
	if got := Root(p, ""); got != plain {
		t.Errorf("扫描到的唯一一份：%s", got)
	}
	makeShare(t, guarded, true)
	if got := Root(p, ""); got != guarded {
		t.Errorf("扫描时有防护状态文件的优先：%s", got)
	}
}
