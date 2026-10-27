package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, la, text string, bom bool) {
	t.Helper()
	p := Path(la)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	b := []byte(text)
	if bom {
		b = append([]byte{0xEF, 0xBB, 0xBF}, b...)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// 和分享包 写部署清单.ps1 写出的格式一致（中文键、路径展开后的绝对路径）。
const sample = `{
  "格式版本": 1,
  "更新时间": "2026-10-27T17:37:22-04:00",
  "端口": { "通用": 43865, "浏览器": 43866, "Desktop": 43867 },
  "脚本根目录": "C:\\Users\\u\\Desktop\\AI应用开发",
  "专用浏览器": [
    { "别名": "KK-Claude", "数据目录": "C:\\Users\\u\\AppData\\Local\\ClaudeAccountBrowsers\\KK-Claude\\UserData", "快捷方式": "C:\\Users\\u\\Desktop\\KK-Claude-专用Chrome.lnk" }
  ]
}`

func TestLoad(t *testing.T) {
	la := t.TempDir()
	if m, err := Load(la); m != nil || err != nil {
		t.Fatalf("没有清单时应返回 nil, nil：%v %v", m, err)
	}
	for _, bom := range []bool{false, true} {
		write(t, la, sample, bom)
		m, err := Load(la)
		if err != nil || m == nil {
			t.Fatalf("bom=%v 读取失败：%v", bom, err)
		}
		if m.Ports != (Ports{43865, 43866, 43867}) || !m.PortsValid() {
			t.Errorf("端口不对：%+v", m.Ports)
		}
		if m.ScriptRoot != `C:\Users\u\Desktop\AI应用开发` || len(m.Browsers) != 1 || m.Browsers[0].Alias != "KK-Claude" ||
			m.Browsers[0].Shortcut != `C:\Users\u\Desktop\KK-Claude-专用Chrome.lnk` {
			t.Errorf("内容不对：%+v", m)
		}
	}
	write(t, la, `{"端口":`, false)
	if m, err := Load(la); m != nil || err == nil {
		t.Errorf("格式坏时应返回错误：%v %v", m, err)
	}
}

func TestPortsValid(t *testing.T) {
	var nilManifest *Manifest
	cases := []struct {
		p    Ports
		want bool
	}{
		{Ports{43865, 43866, 43867}, true},
		{Ports{0, 43866, 43867}, false},
		{Ports{43865, 70000, 43867}, false},
		{Ports{43865, 43865, 43867}, false},
	}
	for _, c := range cases {
		if got := (&Manifest{Ports: c.p}).PortsValid(); got != c.want {
			t.Errorf("%+v：得到 %v，应为 %v", c.p, got, c.want)
		}
	}
	if nilManifest.PortsValid() {
		t.Error("nil 清单不应有效")
	}
}
