// Package netprobe 负责所有联网探测：出口 IP、地理与信誉、Claude 可达性、DNS 解析器。
// 每个请求都显式指定走哪个代理端口，绝不继承进程环境里的 HTTPS_PROXY。
package netprobe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UA 用常见浏览器标识，部分检测站会拒绝非浏览器客户端。
const UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

// Client 返回经指定代理的 HTTP 客户端。proxy 为空表示直连（走系统路由，TUN 开着时由 TUN 接管）。
// proxy 形如 http://127.0.0.1:43865 或 socks5://127.0.0.1:43866（SOCKS 由代理端解析域名）。
func Client(proxy string, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		IdleConnTimeout:       20 * time.Second,
		Proxy:                 nil, // 显式置空：不读环境变量
	}
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		}}
}

func get(ctx context.Context, c *http.Client, rawURL string, referer string, limit int64) (int, http.Header, []byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, nil, "", err
	}
	req.Header.Set("User-Agent", UA)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return resp.StatusCode, resp.Header, body, final, nil
}

// FriendlyErr 把网络错误转成一句中文。
func FriendlyErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(s, "Timeout") || strings.Contains(s, "timeout"):
		return "超时"
	case strings.Contains(s, "connection refused") || strings.Contains(s, "actively refused") || strings.Contains(s, "积极拒绝"):
		return "连接被拒绝（端口没在监听）"
	case strings.Contains(s, "proxyconnect") || strings.Contains(s, "socks connect"):
		return "代理握手失败"
	case strings.Contains(s, "no such host"):
		return "域名解析失败"
	case strings.Contains(s, "certificate"):
		return "证书校验失败（可能被中间人劫持）"
	case strings.Contains(s, "EOF") || strings.Contains(s, "reset"):
		return "连接被中断"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// ---------------------------------------------------------------- Cloudflare trace

// Trace 是 /cdn-cgi/trace 的关键字段。
type Trace struct {
	IP   string
	Loc  string // 大写国家代码
	Colo string
	Warp string
}

// ParseTrace 解析 key=value 行文本。
func ParseTrace(body string) (Trace, error) {
	var t Trace
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			t.IP = v
		case "loc":
			t.Loc = strings.ToUpper(v)
		case "colo":
			t.Colo = v
		case "warp":
			t.Warp = v
		}
	}
	if net.ParseIP(t.IP) == nil {
		return t, errors.New("返回内容不是有效的 trace")
	}
	return t, nil
}

// GetTrace 请求 trace；依次尝试多个地址，第一个成功即返回。
func GetTrace(ctx context.Context, c *http.Client, urls ...string) (Trace, error) {
	var last error
	for _, u := range urls {
		code, _, body, _, err := get(ctx, c, u, "", 8192)
		if err != nil {
			last = err
			continue
		}
		if code != 200 {
			last = fmt.Errorf("HTTP %d", code)
			continue
		}
		t, err := ParseTrace(string(body))
		if err != nil {
			last = err
			continue
		}
		return t, nil
	}
	if last == nil {
		last = errors.New("没有可用的 trace 地址")
	}
	return Trace{}, last
}

// ---------------------------------------------------------------- Claude 可达性

const (
	ReachOK        = "ok"
	ReachChallenge = "challenge"
	ReachBlocked   = "blocked"
	ReachFail      = "fail"
)

// ReachLabel 是可达性的中文说明。
func ReachLabel(s string) string {
	switch s {
	case ReachOK:
		return "可达"
	case ReachChallenge:
		return "Cloudflare 人机验证（浏览器里通常能过）"
	case ReachBlocked:
		return "地区受限"
	case ReachFail:
		return "连接失败"
	}
	return s
}

// ClassifyProbe 根据响应判断可达性（纯函数）。
func ClassifyProbe(host string, status int, header http.Header, body, finalURL string) string {
	low := strings.ToLower(body)
	fu := strings.ToLower(finalURL)
	challenge := strings.EqualFold(header.Get("cf-mitigated"), "challenge") || strings.Contains(low, "just a moment")
	if host == "claude.ai" {
		if challenge {
			return ReachChallenge
		}
		if strings.Contains(fu, "unavailable") || strings.Contains(low, "app-unavailable-in-region") || strings.Contains(low, "unavailable in your region") {
			return ReachBlocked
		}
		if status == 403 {
			// 403 但不是 Cloudflare 页面，多为地区封锁
			if strings.Contains(low, "cloudflare") {
				return ReachChallenge
			}
			return ReachBlocked
		}
		if status >= 500 {
			return ReachFail
		}
		return ReachOK
	}
	// api.anthropic.com：没带 key 正常是 401/404；403 视为封锁
	if status == 403 {
		if challenge {
			return ReachChallenge
		}
		if strings.Contains(low, "request not allowed") || strings.Contains(low, "forbidden") || strings.Contains(low, "region") {
			return ReachBlocked
		}
		return ReachBlocked
	}
	if status >= 500 || status == 0 {
		return ReachFail
	}
	return ReachOK
}

// Probe 访问 URL 并分类，返回状态和一句说明。
func Probe(ctx context.Context, c *http.Client, rawURL string) (string, string) {
	u, _ := url.Parse(rawURL)
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	code, h, body, final, err := get(ctx, c, rawURL, "", 64*1024)
	if err != nil {
		return ReachFail, host + "：" + FriendlyErr(err)
	}
	st := ClassifyProbe(host, code, h, string(body), final)
	return st, fmt.Sprintf("%s：HTTP %d，%s", host, code, ReachLabel(st))
}

// ---------------------------------------------------------------- 出口地理与信誉

// IPInfo 是出口 IP 的地理和信誉信息。
type IPInfo struct {
	IP          string
	CountryCode string // 大写
	Country     string // 英文名
	City        string
	TimeZone    string
	ASN         string
	Org         string
	Trust       *int
	Residential *bool
	Flags       []string
	Source      string
}

// CoffeeRisk 对应 ip.net.coffee /api/iprisk 的字段。
type CoffeeRisk struct {
	IP           string          `json:"ip"`
	Country      string          `json:"country"`
	CountryCode  string          `json:"countryCode"`
	City         string          `json:"city"`
	Region       string          `json:"region"`
	TimeZone     string          `json:"timezone"`
	ASN          json.RawMessage `json:"asn"`
	ASOrg        string          `json:"asOrganization"`
	Trust        *int            `json:"trust_score"`
	Residential  *bool           `json:"isResidential"`
	IsDatacenter *bool           `json:"is_datacenter"`
	IsVPN        *bool           `json:"is_vpn"`
	IsProxy      *bool           `json:"is_proxy"`
	IsTor        *bool           `json:"is_tor"`
	IsCrawler    *bool           `json:"is_crawler"`
	IsAbuser     *bool           `json:"is_abuser"`
	CompanyType  string          `json:"company_type"`
}

func truthy(b *bool) bool { return b != nil && *b }

// ParseCoffeeRisk 把 iprisk 返回转成 IPInfo。
func ParseCoffeeRisk(body []byte) (IPInfo, error) {
	var r CoffeeRisk
	if err := json.Unmarshal(body, &r); err != nil {
		return IPInfo{}, fmt.Errorf("ip.net.coffee 返回格式变了：%w", err)
	}
	if r.CountryCode == "" && r.Country == "" {
		return IPInfo{}, errors.New("ip.net.coffee 没返回国家")
	}
	info := IPInfo{IP: r.IP, CountryCode: strings.ToUpper(r.CountryCode), Country: r.Country, City: r.City,
		TimeZone: r.TimeZone, Org: r.ASOrg, Trust: r.Trust, Residential: r.Residential, Source: "ip.net.coffee"}
	asn := strings.Trim(string(r.ASN), `"`)
	if asn != "" && asn != "null" {
		info.ASN = "AS" + strings.TrimPrefix(strings.ToUpper(asn), "AS")
	}
	for _, f := range []struct {
		b    *bool
		name string
	}{{r.IsDatacenter, "机房"}, {r.IsVPN, "VPN"}, {r.IsProxy, "代理"}, {r.IsTor, "Tor"}, {r.IsCrawler, "爬虫"}, {r.IsAbuser, "滥用记录"}} {
		if truthy(f.b) {
			info.Flags = append(info.Flags, f.name)
		}
	}
	return info, nil
}

// ParseIPInfoIO 解析 ipinfo.io/<ip>/json（兜底来源）。
func ParseIPInfoIO(body []byte) (IPInfo, error) {
	var r struct {
		IP       string `json:"ip"`
		City     string `json:"city"`
		Country  string `json:"country"`
		Org      string `json:"org"`
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return IPInfo{}, err
	}
	if r.Country == "" {
		return IPInfo{}, errors.New("ipinfo.io 没返回国家")
	}
	info := IPInfo{IP: r.IP, CountryCode: strings.ToUpper(r.Country), City: r.City, TimeZone: r.Timezone, Source: "ipinfo.io"}
	if asn, org, ok := strings.Cut(r.Org, " "); ok && strings.HasPrefix(asn, "AS") {
		info.ASN, info.Org = asn, org
	} else {
		info.Org = r.Org
	}
	return info, nil
}

// LookupIP 查 IP 的地理和信誉：先 ip.net.coffee，失败再 ipinfo.io。
func LookupIP(ctx context.Context, c *http.Client, ip string) (IPInfo, error) {
	var errs []string
	code, _, body, _, err := get(ctx, c, "https://ip.net.coffee/api/iprisk/"+url.PathEscape(ip), "https://ip.net.coffee/claude/", 64*1024)
	if err == nil && code == 200 {
		if info, perr := ParseCoffeeRisk(body); perr == nil {
			if info.IP == "" {
				info.IP = ip
			}
			return info, nil
		} else {
			errs = append(errs, perr.Error())
		}
	} else if err != nil {
		errs = append(errs, "ip.net.coffee："+FriendlyErr(err))
	} else {
		errs = append(errs, fmt.Sprintf("ip.net.coffee：HTTP %d", code))
	}
	code, _, body, _, err = get(ctx, c, "https://ipinfo.io/"+url.PathEscape(ip)+"/json", "", 64*1024)
	if err == nil && code == 200 {
		if info, perr := ParseIPInfoIO(body); perr == nil {
			return info, nil
		} else {
			errs = append(errs, perr.Error())
		}
	} else if err != nil {
		errs = append(errs, "ipinfo.io："+FriendlyErr(err))
	} else {
		errs = append(errs, fmt.Sprintf("ipinfo.io：HTTP %d", code))
	}
	return IPInfo{}, errors.New(strings.Join(errs, "；"))
}

// GeoCountry 用 ip.net.coffee /api/geoip 查某 IP 的国家（用于 DNS 解析器）。
func GeoCountry(ctx context.Context, c *http.Client, ip string) (cc, isp string, err error) {
	code, _, body, _, err := get(ctx, c, "https://ip.net.coffee/api/geoip/"+url.PathEscape(ip), "https://ip.net.coffee/claude/", 16*1024)
	if err != nil {
		return "", "", err
	}
	if code != 200 {
		return "", "", fmt.Errorf("HTTP %d", code)
	}
	var r struct {
		CC  string `json:"country_code"`
		ISP string `json:"isp"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", "", err
	}
	return strings.ToUpper(r.CC), r.ISP, nil
}

// ---------------------------------------------------------------- DNS 解析器探测

// Resolver 是一个被权威 DNS 看到的解析器。
type Resolver struct {
	IP  string
	CC  string
	ISP string
}

func randomToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "ceg" + hex.EncodeToString(b)
}

// DNSResolvers 经 via 客户端访问随机子域名，再从 ip.net.coffee 的权威 DNS 读回是谁解析了它。
// via 走被测路径；api 用来读结果和查解析器国家。
func DNSResolvers(ctx context.Context, via, api *http.Client) ([]Resolver, error) {
	token := randomToken()
	var wg sync.WaitGroup
	for i := 1; i <= 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			// 证书或返回内容不重要，只要触发了解析
			_, _, _, _, _ = get(c, via, fmt.Sprintf("https://%s-%d.d.ip.net.coffee/pixel.gif?_=%d", token, i, time.Now().UnixNano()), "", 1024)
		}(i)
	}
	wg.Wait()
	var servers []string
	for attempt := 0; attempt < 3 && len(servers) == 0; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(800+attempt*700) * time.Millisecond):
		}
		code, _, body, _, err := get(ctx, api, "https://ip.net.coffee/api/dns/result/"+token, "https://ip.net.coffee/claude/", 16*1024)
		if err != nil || code != 200 {
			continue
		}
		var r struct {
			Servers []string `json:"dns_servers"`
		}
		if json.Unmarshal(body, &r) == nil {
			servers = r.Servers
		}
	}
	if len(servers) == 0 {
		return nil, nil // 没看到解析器：DNS 加密或未暴露，调用方按「未暴露」处理
	}
	if len(servers) > 6 {
		servers = servers[:6]
	}
	out := make([]Resolver, len(servers))
	var mu sync.Mutex
	for i, s := range servers {
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			cc, isp, _ := GeoCountry(ctx, api, s)
			mu.Lock()
			out[i] = Resolver{IP: s, CC: cc, ISP: isp}
			mu.Unlock()
		}(i, s)
	}
	wg.Wait()
	return out, nil
}

// DirectIPv6 不经代理请求只支持 IPv6 的地址；成功说明有 IPv6 流量能绕开专用端口。
func DirectIPv6(ctx context.Context, timeout time.Duration) (string, error) {
	c := Client("", timeout)
	code, _, body, _, err := get(ctx, c, "https://api6.ipify.org", "", 256)
	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", fmt.Errorf("HTTP %d", code)
	}
	ip := strings.TrimSpace(string(body))
	if p := net.ParseIP(ip); p == nil || p.To4() != nil {
		return "", errors.New("返回的不是 IPv6 地址")
	}
	return ip, nil
}

// PortOpen 检查本机端口能否连上。
func PortOpen(port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
