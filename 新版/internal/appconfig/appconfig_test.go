package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSaveAndFallbacks(t *testing.T) {
	dir := t.TempDir()
	if s, note := Load(dir); s != Defaults() || note != "" {
		t.Errorf("没有设置文件应返回默认值：%+v %q", s, note)
	}
	s := Defaults()
	s.SelectedBrowser, s.RegionOverride = "KK", "jp"
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(dir); got != s {
		t.Errorf("保存后读回不一致：%+v", got)
	}
	bad := s
	bad.PortBrowser = bad.PortCLI
	if err := Save(dir, bad); err == nil {
		t.Errorf("端口重复应拒绝保存")
	}
	p := filepath.Join(Dir(dir), "设置.json")
	_ = os.WriteFile(p, []byte("{坏了"), 0o644)
	if got, note := Load(dir); got != Defaults() || note == "" {
		t.Errorf("损坏的设置应回退默认并提示：%+v %q", got, note)
	}
	_ = os.WriteFile(p, []byte(`{"portCli": 70000, "selectedBrowser": "KK"}`), 0o644)
	if got, note := Load(dir); got.PortCLI != DefaultPortCLI || got.SelectedBrowser != "KK" || note == "" {
		t.Errorf("非法端口应恢复默认端口但保留其他设置：%+v %q", got, note)
	}
}
