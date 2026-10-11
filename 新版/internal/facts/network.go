package facts

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ClaudeEnvGuard/internal/geo"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/netprobe"
	"ClaudeEnvGuard/internal/platform"
)

// Net 是联网阶段用到的函数，测试时可整体替换。
type Net struct {
	Client func(proxy string, timeout time.Duration) *http.Client
	Trace  func(ctx context.Context, c *http.Client) (netprobe.Trace, error)
	Probe  func(ctx context.Context, c *http.Client, url string) (string, string)
	Lookup func(ctx context.Context, c *http.Client, ip string) (netprobe.IPInfo, error)
	DNS    func(ctx context.Context, via, api *http.Client) ([]netprobe.Resolver, error)
	IPv6   func(ctx context.Context) (string, error)
}

// DefaultNet 是真实网络实现。
func DefaultNet() Net {
	return Net{
		Client: netprobe.Client,
		Trace: func(ctx context.Context, c *http.Client) (netprobe.Trace, error) {
			return netprobe.GetTrace(ctx, c, "https://claude.ai/cdn-cgi/trace", "https://www.cloudflare.com/cdn-cgi/trace")
		},
		Probe:  netprobe.Probe,
		Lookup: netprobe.LookupIP,
		DNS:    netprobe.DNSResolvers,
		IPv6: func(ctx context.Context) (string, error) {
			return netprobe.DirectIPv6(ctx, 4*time.Second)
		},
	}
}

// EntrySpec 描述一个入口怎么测。
type EntrySpec struct {
	Entry    model.Entry
	Port     int
	Proxy    string
	ProbeURL string
	DNS      bool // 只有整条通道绑定到出口组的入口才做 DNS 测试（CLI 的混合端口按规则分流，测试域名不代表 Claude 域名）
}

// Entries 返回三个入口的探测参数。
func (f *Facts) Entries() []EntrySpec {
	s := f.Settings
	return []EntrySpec{
		{model.EntryBrowser, s.PortBrowser, "socks5://127.0.0.1:" + strconv.Itoa(s.PortBrowser), "https://claude.ai/", true},
		{model.EntryCLI, s.PortCLI, "http://127.0.0.1:" + strconv.Itoa(s.PortCLI), "https://api.anthropic.com/v1/models", false},
		{model.EntryDesktop, s.PortDesktop, "http://127.0.0.1:" + strconv.Itoa(s.PortDesktop), "https://claude.ai/", true},
	}
}

// CollectNetwork 跑联网阶段。总时长受 budget 限制，超时未完成的项保持「未知」。
func CollectNetwork(ctx context.Context, sys platform.OS, f *Facts, n Net, budget time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var mu sync.Mutex
	closed := false
	apply := func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			fn()
		}
	}
	const reqTimeout = 6 * time.Second

	specs := f.Entries()
	listening := map[model.Entry]bool{}
	for _, sp := range specs {
		l := f.Proxy.Owners[sp.Port].Listening
		listening[sp.Entry] = l
		ex := &model.Exit{Entry: sp.Entry, Port: sp.Port, Listening: l}
		if !l {
			ex.Err = "端口 " + strconv.Itoa(sp.Port) + " 没有程序在监听"
		}
		f.Exits[sp.Entry] = ex
	}
	// 查询地理信息走哪条路：优先浏览器通道，其次 Desktop、CLI，都不通就直连
	apiProxy := ""
	for _, e := range []model.Entry{model.EntryBrowser, model.EntryDesktop, model.EntryCLI} {
		if listening[e] {
			for _, sp := range specs {
				if sp.Entry == e {
					apiProxy = sp.Proxy
				}
			}
			break
		}
	}
	api := n.Client(apiProxy, reqTimeout)

	// 本地原生安装的版本号：~/.local/share/claude/versions/<版本>
	if f.CLI.Version == "" {
		if entries, err := os.ReadDir(filepath.Join(f.Paths.Home, ".local", "share", "claude", "versions")); err == nil && len(entries) > 0 {
			f.CLI.Version = entries[len(entries)-1].Name()
		}
	}

	var wg sync.WaitGroup
	goRun := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = recover() }()
			fn()
		}()
	}
	for _, sp := range specs {
		if !listening[sp.Entry] {
			continue
		}
		sp := sp
		c := n.Client(sp.Proxy, reqTimeout)
		goRun(func() {
			t, err := n.Trace(ctx, c)
			apply(func() {
				ex := f.Exits[sp.Entry]
				if err != nil {
					ex.Err = "测出口失败：" + netprobe.FriendlyErr(err)
					return
				}
				ex.IP, ex.CountryCode = t.IP, t.Loc
			})
		})
		goRun(func() {
			st, detail := n.Probe(ctx, c, sp.ProbeURL)
			apply(func() { f.Exits[sp.Entry].Reach, f.Exits[sp.Entry].ReachDetail = st, detail })
		})
		if sp.DNS {
			goRun(func() {
				rs, err := n.DNS(ctx, c, api)
				leak := toLeak(rs, err)
				apply(func() { f.Exits[sp.Entry].DNS = leak })
			})
		}
	}
	// 不走专用端口的程序看到的 DNS（信息项）
	goRun(func() {
		rs, err := n.DNS(ctx, n.Client("", reqTimeout), api)
		leak := toLeak(rs, err)
		apply(func() { f.SystemDNS = leak })
	})
	// IPv6 是否能绕开代理直接出去
	goRun(func() {
		ip, err := n.IPv6(ctx)
		cc := ""
		if err == nil && ip != "" {
			if info, lerr := n.Lookup(ctx, api, ip); lerr == nil {
				cc = info.CountryCode
			}
		}
		apply(func() {
			f.IPv6.Tested = true
			f.IPv6.DirectIP, f.IPv6.DirectCC = ip, cc
			if err != nil {
				f.IPv6.DirectErr = netprobe.FriendlyErr(err)
			}
		})
	})
	// 较慢的本地校验
	goRun(func() {
		g := f.Guard
		g.Targets = append([]GuardTarget(nil), f.Guard.Targets...)
		if g.Found && len(g.Targets) > 0 {
			HashGuardTargets(ctx, &g)
			apply(func() { f.Guard = g })
		}
	})
	if f.CLI.Installed && f.CLI.Version == "" && strings.HasSuffix(strings.ToLower(f.CLI.Exe), ".exe") {
		exe := f.CLI.Exe
		goRun(func() {
			c, cc := context.WithTimeout(ctx, 4*time.Second)
			defer cc()
			out, code, err := sys.Run(c, exe, "--version")
			if err == nil && code == 0 {
				if m := regexp.MustCompile(`\d+\.\d+\.\d+`).FindString(out); m != "" {
					apply(func() { f.CLI.Version = m })
				}
			}
		})
	}

	// 等 trace 都结束后再查地理（需要 IP）
	waitDone := make(chan struct{})
	go func() { wg.Wait(); close(waitDone) }()

	// trace 通常 1–2 秒内完成；这里单独等它们，再并发查地理，最后与其他任务汇合
	lookupDone := make(chan struct{})
	go func() {
		defer close(lookupDone)
		deadline := time.Now().Add(reqTimeout + 500*time.Millisecond)
		for {
			mu.Lock()
			pending := 0
			for _, sp := range specs {
				ex := f.Exits[sp.Entry]
				if listening[sp.Entry] && ex.IP == "" && !strings.HasPrefix(ex.Err, "测出口失败") {
					pending++
				}
			}
			mu.Unlock()
			if pending == 0 || time.Now().After(deadline) || ctx.Err() != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		mu.Lock()
		ips := map[string]bool{}
		for _, ex := range f.Exits {
			if ex.IP != "" {
				ips[ex.IP] = true
			}
		}
		mu.Unlock()
		var lw sync.WaitGroup
		for ip := range ips {
			ip := ip
			lw.Add(1)
			go func() {
				defer lw.Done()
				defer func() { _ = recover() }()
				info, err := n.Lookup(ctx, api, ip)
				apply(func() {
					for _, ex := range f.Exits {
						if ex.IP != ip {
							continue
						}
						fillGeo(ex, info, err)
					}
				})
			}()
		}
		lw.Wait()
	}()

	select {
	case <-waitDone:
		<-lookupDone
	case <-ctx.Done():
	}
	mu.Lock()
	closed = true
	f.NetworkDone = true
	for _, ex := range f.Exits {
		if ex.Listening && ex.IP == "" && ex.Err == "" {
			ex.Err = "测出口超时"
		}
		if ex.Listening && ex.Reach == "" {
			ex.Reach, ex.ReachDetail = netprobe.ReachFail, "可达性检测超时"
		}
	}
	mu.Unlock()
}

func toLeak(rs []netprobe.Resolver, err error) *model.DNSLeak {
	l := &model.DNSLeak{Tested: true}
	if err != nil {
		l.Err = netprobe.FriendlyErr(err)
		return l
	}
	for _, r := range rs {
		l.Resolvers = append(l.Resolvers, model.Resolver{IP: r.IP, CountryCode: r.CC, ISP: r.ISP})
	}
	return l
}

func fillGeo(ex *model.Exit, info netprobe.IPInfo, err error) {
	if err != nil {
		// 地理查询失败时保留 Cloudflare 给的国家，用该国默认时区兜底
		c := geo.LookupCountry(ex.CountryCode, "")
		ex.Country = c.Name
		ex.TimeZone = c.Primary
		ex.GeoSource = "Cloudflare（仅国家，时区按该国默认）"
		if ex.Err == "" {
			ex.Err = "查询出口详情失败：" + err.Error()
		}
		return
	}
	cc := info.CountryCode
	if cc == "" {
		cc = ex.CountryCode
	}
	if ex.CountryCode == "" {
		ex.CountryCode = cc
	}
	c := geo.LookupCountry(cc, info.Country)
	ex.Country, ex.City, ex.ASN, ex.Org = c.Name, info.City, info.ASN, info.Org
	ex.TimeZone = info.TimeZone
	if ex.TimeZone == "" {
		ex.TimeZone = c.Primary
	}
	ex.TrustScore, ex.Residential, ex.RiskFlags, ex.GeoSource = info.Trust, info.Residential, info.Flags, info.Source
}
