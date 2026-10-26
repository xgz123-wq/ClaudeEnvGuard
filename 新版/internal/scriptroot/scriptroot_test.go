package scriptroot

import (
	"os"
	"path/filepath"
	"testing"

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

func TestFindOrder(t *testing.T) {
	p := platform.NewFake(t.TempDir()).P
	rel := filepath.Join("docs", "a.ps1")
	active := filepath.Join(p.Desktop, "AI应用开发")
	custom := filepath.Join(p.Desktop, "别处", "分享包")
	share := filepath.Join(p.Desktop, "Claude环境分享版")

	if got := Find(p, custom, rel); got != filepath.Join(active, rel) {
		t.Errorf("都没有时返回第一个候选：%s", got)
	}
	touch(t, filepath.Join(share, rel))
	if got := Find(p, custom, rel); got != filepath.Join(share, rel) {
		t.Errorf("只有桌面分享包时用它：%s", got)
	}
	touch(t, filepath.Join(custom, rel))
	if got := Find(p, custom, rel); got != filepath.Join(custom, rel) {
		t.Errorf("设置里的分享包目录优先于桌面分享包：%s", got)
	}
	touch(t, filepath.Join(active, rel))
	if got := Find(p, custom, rel); got != filepath.Join(active, rel) {
		t.Errorf("AI应用开发（活动源）最优先：%s", got)
	}
}
