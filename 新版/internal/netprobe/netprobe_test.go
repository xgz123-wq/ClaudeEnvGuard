package netprobe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseTrace(t *testing.T) {
	tr, err := ParseTrace("fl=1\nh=claude.ai\nip=198.51.100.10\nts=1\nloc=us\ncolo=IAD\nwarp=off\n")
	if err != nil || tr.IP != "198.51.100.10" || tr.Loc != "US" || tr.Colo != "IAD" {
		t.Fatalf("解析错误：%+v %v", tr, err)
	}
	if _, err := ParseTrace("<html>blocked</html>"); err == nil {
		t.Errorf("非 trace 内容应报错")
	}
}

func TestClassifyProbe(t *testing.T) {
	h := http.Header{}
	cf := http.Header{"Cf-Mitigated": []string{"challenge"}}
	cases := []struct {
		host, body, url string
		code            int
		hd              http.Header
		want            string
	}{
		{"claude.ai", "<html>Claude</html>", "https://claude.ai/", 200, h, ReachOK},
		{"claude.ai", "Just a moment...", "https://claude.ai/", 403, h, ReachChallenge},
		{"claude.ai", "", "https://claude.ai/", 403, cf, ReachChallenge},
		{"claude.ai", "", "https://claude.ai/app-unavailable-in-region", 200, h, ReachBlocked},
		{"claude.ai", "forbidden", "https://claude.ai/", 403, h, ReachBlocked},
		{"claude.ai", "", "https://claude.ai/", 502, h, ReachFail},
		{"api.anthropic.com", `{"error":"auth"}`, "", 401, h, ReachOK},
		{"api.anthropic.com", `{"error":{"message":"Request not allowed"}}`, "", 403, h, ReachBlocked},
		{"api.anthropic.com", "", "", 403, cf, ReachChallenge},
	}
	for _, c := range cases {
		if got := ClassifyProbe(c.host, c.code, c.hd, c.body, c.url); got != c.want {
			t.Errorf("%s %d %q → %s，期望 %s", c.host, c.code, c.body, got, c.want)
		}
	}
}

func TestParseCoffeeRisk(t *testing.T) {
	body := []byte(`{"ip": "8.8.8.8", "is_datacenter": true, "isResidential": false, "is_vpn": true, "is_proxy": false, "is_tor": false, "is_crawler": false, "is_abuser": true, "asn": 15169, "asOrganization": "Google LLC", "country": "United States", "countryCode": "us", "city": "", "timezone": "America/Los_Angeles", "trust_score": 63}`)
	info, err := ParseCoffeeRisk(body)
	if err != nil || info.CountryCode != "US" || info.ASN != "AS15169" || *info.Trust != 63 || *info.Residential || info.TimeZone != "America/Los_Angeles" {
		t.Fatalf("解析错误：%+v %v", info, err)
	}
	if strings.Join(info.Flags, ",") != "机房,VPN,滥用记录" {
		t.Errorf("风险标记不对：%v", info.Flags)
	}
	if _, err := ParseCoffeeRisk([]byte(`{"error":"rate limited"}`)); err == nil {
		t.Errorf("没有国家应报错")
	}
	if _, err := ParseCoffeeRisk([]byte(`<html>`)); err == nil {
		t.Errorf("非 JSON 应报错")
	}
}

func TestParseIPInfo(t *testing.T) {
	info, err := ParseIPInfoIO([]byte(`{"ip":"1.2.3.4","city":"Tokyo","country":"JP","org":"AS2516 KDDI CORPORATION","timezone":"Asia/Tokyo"}`))
	if err != nil || info.ASN != "AS2516" || info.Org != "KDDI CORPORATION" || info.CountryCode != "JP" {
		t.Fatalf("解析错误：%+v %v", info, err)
	}
}

func TestClientIgnoresEnvProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ip=1.2.3.4\nloc=jp\n"))
	}))
	defer srv.Close()
	tr, err := GetTrace(context.Background(), Client("", 3*time.Second), srv.URL)
	if err != nil || tr.Loc != "JP" {
		t.Fatalf("直连客户端不应走环境变量里的代理：%v", err)
	}
}

func TestTraceFallbackAndTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ip=9.9.9.9\nloc=de\n"))
	}))
	defer good.Close()
	start := time.Now()
	tr, err := GetTrace(context.Background(), Client("", 300*time.Millisecond), slow.URL, good.URL)
	if err != nil || tr.Loc != "DE" {
		t.Fatalf("第一个地址超时后应换下一个：%v", err)
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Errorf("超时没生效：%v", time.Since(start))
	}
}

func TestFriendlyErr(t *testing.T) {
	if FriendlyErr(context.DeadlineExceeded) != "超时" {
		t.Errorf("超时文案不对")
	}
	if !strings.Contains(FriendlyErr(errors.New("dial tcp: connectex: No connection could be made because the target machine actively refused it.")), "拒绝") {
		t.Errorf("拒绝连接文案不对")
	}
}

func TestPortOpen(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	port := srv.Listener.Addr().(interface{ String() string }).String()
	srv.Close()
	_ = port
	if PortOpen(1, 200*time.Millisecond) {
		t.Errorf("1 号端口不应能连上")
	}
}
