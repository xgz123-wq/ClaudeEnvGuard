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

func TestApplyManifest(t *testing.T) {
	dir := t.TempDir()
	s := Defaults()
	if src, note := ApplyManifest(&s, dir); src != PortFromDefault || note != "" || s != Defaults() {
		t.Errorf("没有清单时端口不变、来源为默认值：%q %q %+v", src, note, s)
	}
	s.PortDesktop = 50000
	if src, _ := ApplyManifest(&s, dir); src != PortFromSettings || s.PortDesktop != 50000 {
		t.Errorf("没有清单、设置文件里有旧端口时沿用它：%q %+v", src, s)
	}
	p := filepath.Join(dir, "ClaudeNetworkGuard", "部署清单.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(`{"格式版本":1,"端口":{"通用":7897,"浏览器":7900,"Desktop":7901}}`), 0o644)
	if src, note := ApplyManifest(&s, dir); src != PortFromManifest || note != "" || s.PortCLI != 7897 || s.PortBrowser != 7900 || s.PortDesktop != 7901 {
		t.Errorf("有清单时三个端口都按清单：%q %q %+v", src, note, s)
	}
	s = Defaults()
	_ = os.WriteFile(p, []byte(`{"端口":{"通用":7897,"浏览器":7897,"Desktop":7901}}`), 0o644)
	if src, note := ApplyManifest(&s, dir); src != PortFromDefault || note == "" || s != Defaults() {
		t.Errorf("清单端口重复时不用、并提示：%q %q %+v", src, note, s)
	}
	_ = os.WriteFile(p, []byte(`{坏了`), 0o644)
	if src, note := ApplyManifest(&s, dir); src != PortFromDefault || note == "" || s != Defaults() {
		t.Errorf("清单读坏时不用、并提示：%q %q %+v", src, note, s)
	}
}
