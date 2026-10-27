package engine

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/netprobe"
	"ClaudeEnvGuard/internal/platform"
	"ClaudeEnvGuard/internal/report"
)

func fakeNet(delay time.Duration) facts.Net {
	return facts.Net{
		Client: func(string, time.Duration) *http.Client { return &http.Client{} },
		Trace: func(ctx context.Context, c *http.Client) (netprobe.Trace, error) {
			time.Sleep(delay)
			return netprobe.Trace{IP: "1.2.3.4", Loc: "JP"}, nil
		},
		Probe: func(ctx context.Context, c *http.Client, u string) (string, string) { return netprobe.ReachOK, "ok" },
		Lookup: func(ctx context.Context, c *http.Client, ip string) (netprobe.IPInfo, error) {
			return netprobe.IPInfo{IP: ip, CountryCode: "JP", Country: "Japan", City: "Tokyo", TimeZone: "Asia/Tokyo"}, nil
		},
		DNS:  func(ctx context.Context, via, api *http.Client) ([]netprobe.Resolver, error) { return nil, nil },
		IPv6: func(ctx context.Context) (string, error) { return "", errors.New("无") },
	}
}

func TestCheckEndToEndOnFakeMachine(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	fk.Ports[43865], fk.Ports[43866], fk.Ports[43867] = 7, 7, 7
	fk.Procs = []platform.Process{{PID: 7, Name: "verge-mihomo.exe"}}
	fk.SetReg(platform.HKLM, facts.KeyTZ, "TimeZoneKeyName", platform.RegValue{Type: "sz", Str: "China Standard Time"})
	e := New(fk, "test", "self.exe")
	e.Net = fakeNet(0)
	var partial *model.Report
	rep, err := e.Check(context.Background(), CheckOptions{}, func(r model.Report) { partial = &r })
	if err != nil {
		t.Fatal(err)
	}
	if partial == nil || partial.Final {
		t.Errorf("应先推送一份未完成的报告")
	}
	if !rep.Final || rep.Target.CountryCode != "JP" || rep.Target.WindowsTZ != "Tokyo Standard Time" {
		t.Fatalf("目标应按日本出口推算：%+v", rep.Target)
	}
	var s1 model.Result
	for _, r := range rep.Results {
		if r.ID == "S1" {
			s1 = r
		}
		if r.Pending {
			t.Errorf("最终报告里不应有检测中的项：%s", r.ID)
		}
	}
	if s1.Status != model.Warn || s1.FixID == "" {
		t.Errorf("中国时区配日本出口应警告：%+v", s1)
	}
	p := e.Plan(fixes.Options{})
	found := false
	for _, a := range p.Actions {
		if a.ID == "sys.timezone" {
			found = true
		}
	}
	if !found {
		t.Errorf("计划里应有改时区：%+v", p.Actions)
	}
	txt := report.Report(rep, report.Style{}, false)
	for _, want := range []string{"结论：", "出口", "系统时区", "合计："} {
		if !strings.Contains(txt, want) {
			t.Errorf("文本报告缺少 %s", want)
		}
	}
}

func TestCheckBusyAndOffline(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	e := New(fk, "test", "")
	e.Net = fakeNet(300 * time.Millisecond)
	fk.Ports[43866] = 1
	done := make(chan struct{})
	go func() {
		_, _ = e.Check(context.Background(), CheckOptions{}, nil)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := e.Check(context.Background(), CheckOptions{}, nil); !errors.As(err, &ErrBusy{}) {
		t.Errorf("并发检查应返回忙：%v", err)
	}
	<-done
	rep, err := e.Check(context.Background(), CheckOptions{Offline: true}, nil)
	if err != nil || !rep.Offline || !rep.Final {
		t.Fatalf("离线检查不对：%v %+v", err, rep)
	}
	for _, r := range rep.Results {
		if r.ID == "P3" && r.Status != model.Unknown {
			t.Errorf("离线时出口一致应为未知")
		}
	}
}

func TestCheckCancelled(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	fk.Ports[43866] = 1
	e := New(fk, "test", "")
	n := fakeNet(0)
	n.Probe = func(ctx context.Context, c *http.Client, u string) (string, string) {
		<-ctx.Done()
		return netprobe.ReachFail, "取消"
	}
	e.Net = n
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	rep, _ := e.Check(ctx, CheckOptions{}, nil)
	if time.Since(start) > 2*time.Second {
		t.Errorf("取消后应尽快返回：%v", time.Since(start))
	}
	if len(rep.Notes) == 0 || !strings.Contains(strings.Join(rep.Notes, ""), "取消") {
		t.Errorf("应提示检查被取消：%v", rep.Notes)
	}
}

// 端口以部署清单为准、设置页只读：保存设置不会把清单里的端口写进设置文件，也改不了端口。
func TestSettingsPortsFromManifestAreReadOnly(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	e := New(fk, "test", "")
	p := filepath.Join(fk.P.LocalAppData, "ClaudeNetworkGuard", "部署清单.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"格式版本":1,"端口":{"通用":7897,"浏览器":7900,"Desktop":7901}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, note, src := e.SettingsWithPortSource()
	if src != appconfig.PortFromManifest || note != "" || s.PortCLI != 7897 || s.PortBrowser != 7900 || s.PortDesktop != 7901 {
		t.Fatalf("应按清单取端口：%q %q %+v", src, note, s)
	}
	s.PortDesktop, s.SelectedBrowser = 12345, "KK"
	if err := e.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	saved, _ := appconfig.Load(fk.P.LocalAppData)
	if saved.PortCLI != appconfig.DefaultPortCLI || saved.PortDesktop != appconfig.DefaultPortDesktop || saved.SelectedBrowser != "KK" {
		t.Errorf("设置文件应保留原端口、只存其他设置：%+v", saved)
	}
	if got, _ := e.Settings(); got.PortDesktop != 7901 {
		t.Errorf("保存后端口仍按清单：%+v", got)
	}
}
