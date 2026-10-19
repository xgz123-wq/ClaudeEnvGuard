package checks

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ClaudeEnvGuard/internal/chrome"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/geo"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/netprobe"
)

// 修复动作 ID，与 fixes 包对应。
const (
	FixBrowserCreate   = "browser.create"
	FixBrowserShortcut = "browser.shortcut"
	FixCLIProxy        = "cli.proxy"
	FixCLIThirdParty   = "cli.thirdparty"
	FixCLIEnvProxy     = "cli.envproxy"
	FixCLIAutoUpdate   = "cli.autoupdate"
	FixDesktopPolicy   = "desktop.policy"
	FixTimezone        = "sys.timezone"
	FixRegion          = "sys.region"
	FixIPv6            = "sys.ipv6"
	FixFirewall        = "sys.firewall"
)

type ev struct {
	f       *facts.Facts
	t       model.Target
	pending bool // 联网阶段未完成
	out     []model.Result
}

func (e *ev) add(r model.Result) *model.Result {
	e.out = append(e.out, r)
	return &e.out[len(e.out)-1]
}

// netPending 把依赖联网的项标记为检测中。
func (e *ev) netPending(r model.Result) model.Result {
	r.Status, r.Pending = model.Unknown, true
	if r.Detail == "" {
		r.Detail = "正在联网检测…"
	}
	return r
}

func (e *ev) offline(r model.Result) model.Result {
	r.Status = model.Unknown
	r.Detail = "离线模式：已跳过联网检测"
	return r
}

// Evaluate 产出全部检查结果。
func Evaluate(f *facts.Facts, t model.Target) []model.Result {
	e := &ev{f: f, t: t, pending: !f.NetworkDone && !f.Offline}
	e.overview()
	e.browser()
	e.cli()
	e.desktop()
	e.system()
	for name, msg := range f.Errors {
		e.markCollectorError(name, msg)
	}
	return e.out
}

// markCollectorError：某个收集器失败时，把它负责的项改成「未知」，绝不留下误导性的通过。
func (e *ev) markCollectorError(name, msg string) {
	group := map[string]model.Group{"proxy": model.GroupOverview, "browsers": model.GroupBrowser, "cli": model.GroupCLI,
		"desktop": model.GroupDesktop, "system": model.GroupSystem}[name]
	ids := map[string][]string{"guard": {"D3"}, "firewall": {"S7"}, "processes": {"B7"}}[name]
	for i := range e.out {
		r := &e.out[i]
		hit := (group != "" && r.Group == group && (name != "proxy" || r.ID == "P1" || r.ID == "P2"))
		for _, id := range ids {
			hit = hit || r.ID == id
		}
		if hit {
			r.Status, r.Pending = model.Unknown, false
			r.Current, r.FixID = "", ""
			r.Detail = "读取现场信息出错（" + msg + "），这一项无法判断。"
			r.Advice = "重新检查一次；仍然出错请把日志发给维护者。"
		}
	}
}

// ---------------------------------------------------------------- 总览

func portLine(o facts.PortOwner) string {
	if !o.Listening {
		return strconv.Itoa(o.Port) + " 未监听"
	}
	if o.Name != "" {
		return strconv.Itoa(o.Port) + " " + o.Name
	}
	return strconv.Itoa(o.Port) + " 已监听"
}

func (e *ev) overview() {
	f := e.f
	s := f.Settings
	ports := []int{s.PortCLI, s.PortBrowser, s.PortDesktop}
	var lines []string
	listening, unknownOwner := 0, ""
	for _, p := range ports {
		o := f.Proxy.Owners[p]
		lines = append(lines, portLine(o))
		if o.Listening {
			listening++
			if o.Name != "" && !facts.IsKnownProxy(o.Name) {
				unknownOwner = o.Name
			}
		}
	}
	r := model.Result{ID: "P1", Group: model.GroupOverview, Title: "代理客户端与三个专用端口", Kind: model.KindFixed,
		Current: strings.Join(lines, "；"), Target: fmt.Sprintf("%d / %d / %d 都由代理客户端监听", s.PortCLI, s.PortBrowser, s.PortDesktop)}
	switch {
	case listening == 0:
		r.Status = model.Fail
		r.Detail = "三个端口都没有程序在监听：代理客户端没开，或还没导入分享包的 Clash 全局脚本。"
		r.Advice = "打开 Clash Verge。还没装的话，按分享包 README 第一步安装，并导入「Clash全局账号通道.js」脚本，它负责开出这三个端口。"
	case listening < 3:
		r.Status = model.Fail
		r.Detail = "部分端口没开，对应入口会连不上或直连。"
		r.Advice = "确认 Clash Verge 已导入分享包的「Clash全局账号通道.js」并重新加载配置；端口号和本工具设置里的一致。"
	case unknownOwner != "":
		r.Status = model.Warn
		r.Detail = "端口被 " + unknownOwner + " 占用，它不像常见代理客户端。"
		r.Advice = "确认这个程序确实是你的代理客户端；否则关掉它，让 Clash Verge 使用这些端口。"
	default:
		r.Status = model.Pass
		r.Detail = "三个入口各自的端口都在监听。"
	}
	e.add(r)

	r = model.Result{ID: "P2", Group: model.GroupOverview, Title: "Clash Verge 关键设置", Kind: model.KindFixed, Advanced: false,
		Target: fmt.Sprintf("TUN 开、系统代理关、混合端口 %d、两个专用监听器存在", s.PortCLI)}
	if !f.Proxy.VergeFound {
		r.Status = model.NA
		r.Detail = "没检测到 Clash Verge 的配置；用其他代理客户端时跳过本项。"
		if f.Proxy.VergeErr != "" {
			r.Status, r.Detail = model.Unknown, "读取 Clash Verge 配置失败："+f.Proxy.VergeErr
		}
		e.add(r)
	} else {
		var cur, fails, warns []string
		if f.Proxy.TunMode != nil {
			cur = append(cur, "TUN "+onOff(*f.Proxy.TunMode))
			if !*f.Proxy.TunMode {
				warns = append(warns, "TUN 没开：没走专用端口的程序会直连")
			}
		}
		if f.Proxy.SystemProxy != nil {
			cur = append(cur, "系统代理 "+onOff(*f.Proxy.SystemProxy))
			if *f.Proxy.SystemProxy {
				warns = append(warns, "系统代理开着：分享包用 TUN，建议关掉系统代理")
			}
		}
		if f.Proxy.MixedPort != 0 {
			cur = append(cur, "混合端口 "+strconv.Itoa(f.Proxy.MixedPort))
			if f.Proxy.MixedPort != s.PortCLI {
				fails = append(fails, fmt.Sprintf("混合端口是 %d，CLI 端口设置是 %d", f.Proxy.MixedPort, s.PortCLI))
			}
		}
		if f.Proxy.Listeners != nil {
			for _, n := range []string{"account-us-socks", "claude-desktop-http"} {
				if !f.Proxy.Listeners[n] {
					fails = append(fails, "配置里没有专用监听器 "+n+"（全局脚本没生效）")
				}
			}
		}
		r.Current = strings.Join(cur, "，")
		switch {
		case len(fails) > 0:
			r.Status, r.Detail = model.Fail, strings.Join(append(fails, warns...), "；")
			r.Advice = "在 Clash Verge 里确认导入了分享包的全局脚本，混合端口改成和本工具一致，然后重新加载。"
		case len(warns) > 0:
			r.Status, r.Detail = model.Warn, strings.Join(warns, "；")
			r.Advice = "在 Clash Verge 设置里打开「虚拟网卡模式（TUN）」，关闭「系统代理」。"
		default:
			r.Status, r.Detail = model.Pass, "与分享包约定一致。"
		}
		e.add(r)
	}

	// 出口相关
	rc := model.Result{ID: "P3", Group: model.GroupOverview, Title: "三个入口出口一致", Kind: model.KindFixed,
		Target: "三个入口从同一个 IP 出去"}
	rr := model.Result{ID: "P4", Group: model.GroupOverview, Title: "出口地区 Claude 支持", Kind: model.KindFixed,
		Target: "出口不在 Claude 不支持的地区"}
	rq := model.Result{ID: "P5", Group: model.GroupOverview, Title: "出口 IP 质量（ip.net.coffee）", Kind: model.KindInfo,
		Target: "信任评分 ≥ 80，住宅 IP"}
	switch {
	case f.Offline:
		e.add(e.offline(rc))
		e.add(e.offline(rr))
		e.add(e.offline(rq))
		return
	case e.pending:
		e.add(e.netPending(rc))
		e.add(e.netPending(rr))
		e.add(e.netPending(rq))
		return
	}
	measured := e.measuredExits()
	if len(measured) == 0 {
		for _, r := range []model.Result{rc, rr, rq} {
			r.Status = model.Unknown
			r.Detail = "三个入口都没测到出口（端口没开或网络不通）。"
			e.add(r)
		}
		return
	}
	ips, ccs := map[string][]string{}, map[string][]string{}
	for _, ex := range measured {
		ips[ex.IP] = append(ips[ex.IP], model.EntryLabel(ex.Entry))
		ccs[ex.CountryCode] = append(ccs[ex.CountryCode], model.EntryLabel(ex.Entry))
	}
	rc.Current = describeGroups(ips)
	switch {
	case len(measured) == 1:
		rc.Status, rc.Detail = model.Pass, "只有一个入口测到了出口，无法比较。"
	case len(ips) == 1:
		rc.Status, rc.Detail = model.Pass, "测到的入口都从同一个 IP 出去。"
	case len(ccs) == 1:
		rc.Status, rc.Detail = model.Warn, "IP 不同但在同一国家：同一账号可能被看到来自多个 IP。"
		rc.Advice = "在 Clash 里让三个入口都绑定同一个出口组（分享包是 ACCOUNT-US）。"
	default:
		rc.Status, rc.Detail = model.Fail, "不同入口从不同国家出去：同一账号会被看到在多个国家同时登录。"
		rc.Advice = "在 Clash 里让三个入口都绑定同一个出口组（分享包是 ACCOUNT-US），并固定一个节点。"
	}
	e.add(rc)

	var bad []string
	var all []string
	for cc, who := range ccs {
		c := geo.LookupCountry(cc, "")
		all = append(all, c.Name+"（"+strings.Join(who, "、")+"）")
		if !geo.ClaudeSupported(cc) {
			bad = append(bad, c.Name)
		}
	}
	sort.Strings(all)
	rr.Current = strings.Join(all, "；")
	if len(bad) > 0 {
		rr.Status, rr.Detail = model.Fail, "出口在 "+strings.Join(bad, "、")+"，Claude 不在这些地区提供服务。"
		rr.Advice = "在 Clash 里给出口组换一个 Claude 支持地区的节点（如美国、日本、新加坡）。"
	} else {
		rr.Status, rr.Detail = model.Pass, "出口地区在 Claude 服务范围内。"
	}
	e.add(rr)

	seen := map[string]bool{}
	var parts []string
	worst := model.Pass
	anyScore := false
	for _, ex := range measured {
		if seen[ex.IP] {
			continue
		}
		seen[ex.IP] = true
		desc := ex.IP
		st := model.Unknown
		if ex.TrustScore != nil {
			anyScore = true
			desc += fmt.Sprintf(" 评分 %d", *ex.TrustScore)
			switch {
			case *ex.TrustScore >= 80:
				st = model.Pass
			case *ex.TrustScore >= 50:
				st = model.Warn
			default:
				st = model.Fail
			}
		}
		if ex.Residential != nil {
			if *ex.Residential {
				desc += "，住宅"
			} else {
				desc += "，非住宅"
				if st == model.Pass {
					st = model.Warn
				}
			}
		}
		if len(ex.RiskFlags) > 0 {
			desc += "，标记：" + strings.Join(ex.RiskFlags, "/")
		}
		if ex.Org != "" {
			desc += "（" + ex.Org + "）"
		}
		parts = append(parts, desc)
		if st.Rank() > worst.Rank() {
			worst = st
		}
	}
	rq.Current = strings.Join(parts, "；")
	if !anyScore {
		rq.Status, rq.Detail = model.Unknown, "ip.net.coffee 没返回评分（网站不可达或接口变化），可在专用浏览器里打开 ip.net.coffee/claude 人工查看。"
	} else {
		rq.Status = worst
		switch worst {
		case model.Pass:
			rq.Detail = "IP 信誉良好。"
		case model.Warn:
			rq.Detail = "IP 信誉一般或不是住宅 IP，风控概率更高。"
			rq.Advice = "可以换一个原生住宅 IP 节点；这项不影响能否使用。"
		default:
			rq.Detail = "IP 信誉差，容易触发风控。"
			rq.Advice = "建议更换节点，优先原生住宅 IP。"
		}
	}
	e.add(rq)
}

func (e *ev) measuredExits() []*model.Exit {
	var out []*model.Exit
	for _, en := range []model.Entry{model.EntryBrowser, model.EntryCLI, model.EntryDesktop} {
		if ex := e.f.Exits[en]; ex != nil && ex.IP != "" {
			out = append(out, ex)
		}
	}
	return out
}

func describeGroups(m map[string][]string) string {
	var parts []string
	for k, who := range m {
		parts = append(parts, k+"（"+strings.Join(who, "、")+"）")
	}
	sort.Strings(parts)
	return strings.Join(parts, "；")
}

func onOff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

// reachResult 把某入口的可达性转成结果。
func (e *ev) reachResult(r model.Result, en model.Entry) model.Result {
	if e.f.Offline {
		return e.offline(r)
	}
	if e.pending {
		return e.netPending(r)
	}
	ex := e.f.Exits[en]
	if ex == nil || !ex.Listening {
		r.Status = model.Fail
		r.Current = "端口没在监听"
		r.Detail = "这个入口的专用端口没开，Claude 连不上（或会直连）。先处理总览里的「代理客户端」。"
		return r
	}
	r.Current = netprobe.ReachLabel(ex.Reach)
	if ex.IP != "" {
		r.Current += "，出口 " + ex.IP
		if ex.Country != "" {
			r.Current += " " + ex.Country
		}
	}
	r.Detail = ex.ReachDetail
	switch ex.Reach {
	case netprobe.ReachOK:
		r.Status = model.Pass
	case netprobe.ReachChallenge:
		if en == model.EntryCLI {
			r.Status = model.Warn
			r.Advice = "API 接口不该出现人机验证，说明出口 IP 被 Cloudflare 重点盯防：建议换节点。"
		} else {
			// claude.ai 对任何非浏览器客户端都会先给人机验证页：能拿到它说明已到达 Claude 且没被地区封锁
			r.Status = model.Pass
			r.Current = "可达（到达 Claude 的 Cloudflare 边缘）"
			if ex.IP != "" {
				r.Current += "，出口 " + ex.IP
			}
			r.Detail = "命令行访问 claude.ai 都会被要求人机验证，这是正常现象；没有出现地区封锁页面。"
		}
	case netprobe.ReachBlocked:
		r.Status = model.Fail
		r.Advice = "出口所在地区被 Claude 拒绝：换一个支持地区的节点。"
	default:
		r.Status = model.Fail
		r.Advice = "代理节点不通或被墙：在 Clash 里换节点后重新检查。"
	}
	return r
}

// ---------------------------------------------------------------- 专用浏览器

func (e *ev) browser() {
	f := e.f
	g := model.GroupBrowser
	na := func(id, title string, kind model.Kind, why string) {
		e.add(model.Result{ID: id, Group: g, Title: title, Kind: kind, Status: model.NA, Detail: why})
	}
	titles := []struct {
		id, title string
		kind      model.Kind
	}{{"B2", "快捷方式启动的是 Chrome", model.KindFixed}, {"B3", "走专用代理端口", model.KindFixed}, {"B4", "DNS 防漏", model.KindFixed},
		{"B5", "WebRTC 防漏", model.KindFixed}, {"B6", "浏览器语言", model.KindIP}, {"B7", "正在运行的窗口带防护参数", model.KindFixed},
		{"B8", "Claude 可达（经浏览器端口）", model.KindFixed}}
	if len(f.Browsers) == 0 || f.Selected == nil {
		r := model.Result{ID: "B1", Group: g, Title: "专用浏览器", Kind: model.KindFixed, Status: model.Fail,
			Current: "没有找到专用浏览器", Target: "每个 Claude 账号一个独立数据目录的 Chrome",
			Detail: "没在 " + facts.StandardRoot(f.Paths) + " 下找到专用浏览器，桌面上也没有带 --user-data-dir 的快捷方式。",
			Advice: "点「一键修复」新建一个（需要先装 Chrome）。不要用 Chrome 自带的「添加用户」，那样数据没有隔离。",
			FixID:  FixBrowserCreate}
		if f.ChromeExe == "" {
			r.Advice = "先安装 Google Chrome，再回来点「一键修复」新建专用浏览器。"
			r.FixID = ""
		}
		e.add(r)
		for _, t := range titles {
			na(t.id, t.title, t.kind, "还没有专用浏览器。")
		}
		return
	}
	b := f.Selected
	r := model.Result{ID: "B1", Group: g, Title: "专用浏览器「" + b.Name + "」", Kind: model.KindFixed,
		Current: b.UserDataDir, Target: "独立数据目录 + 桌面快捷方式"}
	defaultDir := chrome.NormDir(facts.DefaultChromeUserData(f.Paths)) == chrome.NormDir(b.UserDataDir)
	switch {
	case defaultDir:
		r.Status = model.Fail
		r.Detail = "用的是系统 Chrome 的默认数据目录，和日常浏览共用 Cookie，起不到隔离作用。"
		r.Advice = "点「一键修复」新建一个独立的专用浏览器。"
		r.FixID = FixBrowserCreate
	case len(b.Shortcuts) == 0:
		r.Status = model.Fail
		r.Detail = "数据目录在，但桌面上没有它的快捷方式，只能从别处打开，容易漏掉防护参数。"
		r.Advice = "点「一键修复」重新生成桌面快捷方式。"
		r.FixID = FixBrowserShortcut
	case len(b.DuplicateOf) > 0:
		r.Status = model.Warn
		r.Detail = "同一个数据目录被多个快捷方式共用（" + strings.Join(b.DuplicateOf, "、") + "）：一个目录只能登一个账号。"
		r.Advice = "给每个账号单独建一个专用浏览器，删掉多余的快捷方式。"
	default:
		r.Status = model.Pass
		r.Detail = "有独立数据目录和桌面快捷方式。"
		if !b.DirExists {
			r.Detail += "数据目录还没创建（第一次打开后自动生成）。"
		}
		if !b.Standard {
			r.Detail += "目录不在标准位置 ClaudeAccountBrowsers 下，也可以正常使用。"
		}
	}
	e.add(r)
	if len(b.Shortcuts) == 0 {
		for _, t := range titles[:5] {
			na(t.id, t.title, t.kind, "没有快捷方式，先完成上一项。")
		}
		e.runningCheck(b)
		e.add(e.reachResult(model.Result{ID: "B8", Group: g, Title: titles[6].title, Kind: model.KindFixed}, model.EntryBrowser))
		return
	}
	scs := b.Shortcuts

	// B2 启动程序
	r = model.Result{ID: "B2", Group: g, Title: titles[0].title, Kind: model.KindFixed, Target: "chrome.exe"}
	var bad []string
	for _, sc := range scs {
		base := strings.ToLower(filepath.Base(sc.Target))
		r.Current = sc.Target
		if base != "chrome.exe" {
			bad = append(bad, filepath.Base(sc.Path)+" 指向 "+filepath.Base(sc.Target))
		} else if !fileExists(sc.Target) {
			bad = append(bad, filepath.Base(sc.Path)+" 指向的 Chrome 不存在")
		}
	}
	if len(bad) > 0 {
		r.Status, r.Detail = model.Fail, strings.Join(bad, "；")
		if f.ChromeExe != "" {
			r.FixID, r.Advice = FixBrowserShortcut, "点「一键修复」把快捷方式改成指向 "+f.ChromeExe
		} else {
			r.Advice = "先安装 Google Chrome。"
		}
	} else {
		r.Status, r.Detail = model.Pass, "快捷方式启动的是 Chrome。"
	}
	e.add(r)

	// B3 代理端口
	want := chrome.ProxyValue(f.Settings.PortBrowser)
	r = model.Result{ID: "B3", Group: g, Title: titles[1].title, Kind: model.KindFixed, Target: want}
	bad = nil
	for _, sc := range scs {
		v := sc.Flags.Get(chrome.FlagProxyServer)
		r.Current = orNone(v)
		if !chrome.ProxyOK(v, f.Settings.PortBrowser) {
			if v == "" {
				bad = append(bad, filepath.Base(sc.Path)+" 没设代理，浏览器会按系统网络直连")
			} else {
				bad = append(bad, filepath.Base(sc.Path)+" 代理是 "+v)
			}
		}
	}
	e.add(flagResult(r, bad, "浏览器所有流量都走专用 SOCKS 端口。"))

	// B4 DNS 防漏
	r = model.Result{ID: "B4", Group: g, Title: titles[2].title, Kind: model.KindFixed,
		Target: "--host-resolver-rules 让域名交给代理解析；代理那头不用中国 DNS"}
	bad = nil
	for _, sc := range scs {
		v := sc.Flags.Get(chrome.FlagHostResolver)
		r.Current = orNone(v)
		if !chrome.ResolverOK(v) {
			bad = append(bad, filepath.Base(sc.Path)+" 缺少远端解析参数，域名可能被本地 DNS 解析")
		}
	}
	r = flagResult(r, bad, "域名全部交给代理解析。")
	if r.Status == model.Pass {
		r = e.dnsVerdict(r, model.EntryBrowser)
	}
	e.add(r)

	// B5 WebRTC
	r = model.Result{ID: "B5", Group: g, Title: titles[3].title, Kind: model.KindFixed, Target: chrome.WebRTCSafePolicy}
	bad = nil
	for _, sc := range scs {
		v := sc.Flags.Get(chrome.FlagWebRTCPolicy)
		r.Current = orNone(v)
		if !strings.EqualFold(v, chrome.WebRTCSafePolicy) {
			bad = append(bad, filepath.Base(sc.Path)+" 没禁用非代理 UDP，网页能通过 WebRTC 拿到真实 IP")
		}
	}
	e.add(flagResult(r, bad, "WebRTC 只能走代理，不会暴露真实 IP。"))

	// B6 语言
	e.browserLang(b)

	// B7 运行状态
	e.runningCheck(b)

	// B8 可达
	e.add(e.reachResult(model.Result{ID: "B8", Group: g, Title: titles[6].title, Kind: model.KindFixed}, model.EntryBrowser))
}

func flagResult(r model.Result, bad []string, okText string) model.Result {
	if len(bad) > 0 {
		r.Status, r.Detail = model.Fail, strings.Join(bad, "；")
		r.FixID, r.Advice = FixBrowserShortcut, "点「一键修复」补全快捷方式参数，然后关掉这个浏览器的所有窗口，从桌面快捷方式重新打开。"
	} else {
		r.Status, r.Detail = model.Pass, okText
	}
	return r
}

func orNone(v string) string {
	if v == "" {
		return "未设置"
	}
	return v
}

// dnsVerdict 把联网 DNS 解析器探测并入判定。
func (e *ev) dnsVerdict(r model.Result, en model.Entry) model.Result {
	if e.f.Offline {
		r.Detail += "（离线模式：没做 DNS 解析器探测）"
		return r
	}
	if e.pending {
		// 参数已正确，但解析器探测还没回来：先显示「检测中」，不提前给绿灯
		r.Pending, r.Status = true, model.Unknown
		r.Detail += "正在探测代理那头用的 DNS…"
		return r
	}
	ex := e.f.Exits[en]
	if ex == nil || ex.DNS == nil || !ex.DNS.Tested {
		r.Detail += "（端口没开，没做 DNS 解析器探测）"
		return r
	}
	if ex.DNS.Err != "" {
		r.Detail += "（DNS 解析器探测失败：" + ex.DNS.Err + "）"
		return r
	}
	if len(ex.DNS.Resolvers) == 0 {
		r.Detail += "探测显示 DNS 解析器未暴露。"
		return r
	}
	var cn, list []string
	for _, rs := range ex.DNS.Resolvers {
		item := rs.IP
		if rs.CountryCode != "" {
			item += " " + geo.LookupCountry(rs.CountryCode, "").Name
		}
		list = append(list, item)
		if rs.CountryCode == "CN" && ex.CountryCode != "CN" {
			cn = append(cn, rs.IP)
		}
	}
	r.Current += "；解析器：" + strings.Join(list, "、")
	if len(cn) > 0 {
		r.Status = model.Fail
		r.Detail = "参数正确，但代理那头用的是中国大陆的 DNS（" + strings.Join(cn, "、") + "），访问记录会暴露。"
		r.Advice = "这是节点或 Clash DNS 配置的问题：在 Clash 里把出口组的 DNS 指向境外（分享包的 DNS 分层说明里有做法），或换节点。"
		r.FixID = ""
		return r
	}
	r.Detail += "代理那头的 DNS 不在中国大陆。"
	return r
}

func (e *ev) browserLang(b *facts.Browser) {
	f := e.f
	r := model.Result{ID: "B6", Group: model.GroupBrowser, Title: "浏览器语言", Kind: model.KindIP}
	lang, accept := "", ""
	for _, sc := range b.Shortcuts {
		lang, accept = sc.Flags.Get(chrome.FlagLang), sc.Flags.Get(chrome.FlagAcceptLang)
	}
	r.Current = "lang=" + orNone(lang) + "，accept-lang=" + orNone(accept)
	if b.PrefLang != "" && accept == "" {
		r.Current += "（浏览器设置里：" + b.PrefLang + "）"
	}
	if e.t.Source == "none" {
		r.Status, r.Target = model.Unknown, "随出口 IP"
		r.Detail = e.t.SourceText
		r.Pending = e.pending
		e.add(r)
		return
	}
	r.Target = e.t.Lang + "（英语在任何地区都可接受）"
	effective := lang
	if effective == "" {
		effective = f.System.UILang
	}
	st, why := langVerdict(effective, e.t)
	if lang == "" {
		why = "没指定 --lang，会跟随 Windows 显示语言（" + orNone(f.System.UILang) + "）。" + why
		if st == model.Pass {
			st = model.Warn
		}
	}
	if accept != "" {
		if ast, awhy := langVerdict(strings.Split(accept, ",")[0], e.t); ast.Rank() > st.Rank() {
			st, why = ast, "accept-lang："+awhy
		}
	}
	r.Status, r.Detail = st, why
	if st != model.Pass {
		r.FixID = FixBrowserShortcut
		r.Advice = "点「一键修复」把语言参数改成 " + e.t.Lang + "，然后从桌面快捷方式重开浏览器。"
	}
	e.add(r)
}

// langVerdict：一致或英语 → 通过；中文但出口不在中文地区 → 失败；其他不一致 → 警告。
func langVerdict(cur string, t model.Target) (model.Status, string) {
	return langVerdictAs("界面", cur, t)
}

// langVerdictAs 同 langVerdict，what 用于说明（界面 / 格式）。
func langVerdictAs(what, cur string, t model.Target) (model.Status, string) {
	cb, tb := geo.LangBase(cur), geo.LangBase(t.Lang)
	switch {
	case cur == "":
		return model.Warn, "语言未知。"
	case cb == tb:
		return model.Pass, "与出口地区（" + t.Country + "）一致。"
	case cb == "en":
		return model.Pass, "英语在任何地区都常见。"
	case cb == "zh":
		return model.Fail, "中文" + what + "配" + t.Country + "出口，容易被看出不在当地。"
	default:
		return model.Warn, "和出口地区（" + t.Country + "）的常用语言不一致。"
	}
}

func (e *ev) runningCheck(b *facts.Browser) {
	r := model.Result{ID: "B7", Group: model.GroupBrowser, Title: "正在运行的窗口带防护参数", Kind: model.KindFixed,
		Target: "只从桌面快捷方式打开"}
	if len(b.Running) == 0 {
		r.Status, r.Current, r.Detail = model.Pass, "未运行", "这个浏览器现在没开。"
		e.add(r)
		return
	}
	var bad []string
	for _, p := range b.Running {
		fl := chrome.Parse(p.CmdLine)
		var miss []string
		if !chrome.ProxyOK(fl.Get(chrome.FlagProxyServer), e.f.Settings.PortBrowser) {
			miss = append(miss, "代理")
		}
		if !chrome.ResolverOK(fl.Get(chrome.FlagHostResolver)) {
			miss = append(miss, "DNS 防漏")
		}
		if !strings.EqualFold(fl.Get(chrome.FlagWebRTCPolicy), chrome.WebRTCSafePolicy) {
			miss = append(miss, "WebRTC 防漏")
		}
		if len(miss) > 0 {
			bad = append(bad, fmt.Sprintf("进程 %d 缺少：%s", p.PID, strings.Join(miss, "、")))
		}
	}
	r.Current = fmt.Sprintf("运行中（%d 个窗口进程）", len(b.Running))
	if len(bad) > 0 {
		r.Status = model.Fail
		r.Detail = strings.Join(bad, "；") + "。多半是从开始菜单、任务栏或别的链接直接打开的，Chrome 会沿用第一次启动的参数。"
		r.Advice = "关掉这个浏览器的全部窗口（包括托盘里的后台），再从桌面快捷方式打开。"
	} else {
		r.Status, r.Detail = model.Pass, "正在运行，启动参数完整。"
	}
	e.add(r)
}

func fileExists(p string) bool {
	return p != "" && facts.Exists(p)
}

// ---------------------------------------------------------------- Claude Code

func (e *ev) cli() {
	f := e.f
	c := f.CLI
	g := model.GroupCLI
	r := model.Result{ID: "C1", Group: g, Title: "Claude Code", Kind: model.KindInfo}
	active := c.Installed || c.Settings.Exists
	if c.Installed {
		r.Status = model.Pass
		r.Current = orUnknown(c.Version)
		r.Detail = "安装位置：" + c.Exe
	} else if c.Settings.Exists {
		r.Status, r.Current = model.Pass, "没找到程序，但有配置文件"
		r.Detail = "可能装在非常规位置；下面按配置文件检查。"
	} else {
		r.Status, r.Current, r.Detail = model.NA, "未安装", "没检测到 Claude Code，本页其余项跳过。"
	}
	e.add(r)
	ids := []struct{ id, title string }{{"C2", "代理设置（settings.json）"}, {"C3", "没有指向第三方接口"}, {"C4", "系统环境变量里的代理"},
		{"C5", "自动更新"}, {"C6", "Claude API 可达（经 CLI 端口）"}}
	if !active {
		for _, x := range ids {
			e.add(model.Result{ID: x.id, Group: g, Title: x.title, Kind: model.KindFixed, Status: model.NA, Detail: "没装 Claude Code。"})
		}
		return
	}
	env := mergedEnv(c)
	want := "http://127.0.0.1:" + strconv.Itoa(f.Settings.PortCLI)

	// C2
	r = model.Result{ID: "C2", Group: g, Title: ids[0].title, Kind: model.KindFixed,
		Target: "HTTP(S)_PROXY = " + want + "，NO_PROXY 含 localhost 和 127.0.0.1"}
	if c.Settings.Err != "" || c.LocalSettings.Err != "" {
		r.Status = model.Unknown
		r.Detail = strings.TrimSpace(c.Settings.Err + " " + c.LocalSettings.Err)
		r.Advice = "用记事本打开 " + c.Settings.Path + " 修正 JSON 格式（常见是多了或少了逗号），再重新检查。"
		e.add(r)
	} else {
		var fails, warns []string
		var cur []string
		for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY"} {
			v := env[k]
			cur = append(cur, k+"="+orNone(v))
			switch {
			case v == "":
				warns = append(warns, k+" 没设：只能靠 TUN 规则分流，TUN 关掉就会直连")
			case !sameProxy(v, want):
				fails = append(fails, k+" 是 "+v+"，应为 "+want)
			}
		}
		if v := env["ALL_PROXY"]; v != "" && !sameProxy(v, want) {
			warns = append(warns, "ALL_PROXY 是 "+v+"，和 CLI 端口不一致")
		}
		np := strings.ToLower(env["NO_PROXY"])
		if !strings.Contains(np, "localhost") || !strings.Contains(np, "127.0.0.1") {
			warns = append(warns, "NO_PROXY 没包含 localhost 和 127.0.0.1，本地服务（如 MCP）可能被送进代理")
		}
		for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
			if strings.HasPrefix(strings.ToLower(env[k]), "socks5://") {
				warns = append(warns, k+" 用 socks5:// 会在本地解析域名，建议用 http://（或 socks5h://）")
			}
		}
		r.Current = strings.Join(cur, "，")
		switch {
		case len(fails) > 0:
			r.Status, r.Detail = model.Fail, strings.Join(append(fails, warns...), "；")
		case len(warns) > 0:
			r.Status, r.Detail = model.Warn, strings.Join(warns, "；")
		default:
			r.Status, r.Detail = model.Pass, "Claude Code 的请求都走 CLI 专用端口，域名由代理解析（不会本地 DNS 泄露）。"
		}
		if r.Status != model.Pass {
			r.FixID = FixCLIProxy
			r.Advice = "点「一键修复」写入 settings.json（保留你其他配置），然后重启 Claude Code。"
		}
		e.add(r)
	}

	// C3
	r = model.Result{ID: "C3", Group: g, Title: ids[1].title, Kind: model.KindFixed, Target: "以下变量都不设置：" + strings.Join(facts.ThirdPartyKeys, "、")}
	var fails, warns, cur []string
	machineHit := false
	for _, src := range []struct {
		name string
		m    map[string]string
	}{{"settings.json", c.Settings.Env}, {"settings.local.json", c.LocalSettings.Env}, {"用户环境变量", c.UserEnv}, {"系统环境变量", c.MachineEnv}} {
		for _, k := range facts.ThirdPartyKeys {
			v, ok := src.m[k]
			if !ok {
				continue
			}
			cur = append(cur, src.name+"："+k+"="+v)
			if src.name == "系统环境变量" {
				machineHit = true
			}
			if k == "ANTHROPIC_BASE_URL" || k == "ANTHROPIC_API_URL" {
				fails = append(fails, src.name+" 里的 "+k+" 会把请求发到第三方中转")
			} else {
				warns = append(warns, src.name+" 里的 "+k+" 表示没用官方账号登录")
			}
		}
	}
	r.Current = strings.Join(cur, "；")
	if r.Current == "" {
		r.Current = "都没设置"
	}
	switch {
	case len(fails) > 0:
		r.Status, r.Detail = model.Fail, strings.Join(append(fails, warns...), "；")
	case len(warns) > 0:
		r.Status, r.Detail = model.Warn, strings.Join(warns, "；")
	default:
		r.Status, r.Detail = model.Pass, "直接连官方接口。"
	}
	if r.Status != model.Pass {
		r.FixID = FixCLIThirdParty
		r.Advice = "点「一键修复」删除这些变量（会先备份，可还原）。"
		if machineHit {
			r.Advice += "系统级变量需要管理员权限，会弹一次 UAC。"
		}
	}
	e.add(r)

	// C4
	r = model.Result{ID: "C4", Group: g, Title: ids[2].title, Kind: model.KindFixed, Target: "不设，或和 CLI 端口一致"}
	cur, warns = nil, nil
	for _, src := range []struct {
		name string
		m    map[string]string
	}{{"用户", c.UserEnv}, {"系统", c.MachineEnv}} {
		for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
			if v, ok := src.m[k]; ok {
				cur = append(cur, src.name+"："+k+"="+v)
				if !sameProxy(v, want) {
					warns = append(warns, src.name+"环境变量 "+k+" 指向 "+v)
				}
			}
		}
	}
	r.Current = strings.Join(cur, "；")
	if r.Current == "" {
		r.Current = "没有"
	}
	switch {
	case len(warns) == 0:
		r.Status, r.Detail = model.Pass, "没有冲突的全局代理变量。"
	case env["HTTPS_PROXY"] != "" && sameProxy(env["HTTPS_PROXY"], want):
		r.Status = model.Pass
		r.Detail = strings.Join(warns, "；") + "。Claude Code 以 settings.json 为准，不受影响；但其他程序会走这个代理。"
	default:
		r.Status, r.Detail = model.Warn, strings.Join(warns, "；")+"。settings.json 没设代理时，Claude Code 会走这个地址。"
		r.FixID = FixCLIEnvProxy
		r.Advice = "推荐先修复「代理设置」（写进 settings.json 后这里就不影响 Claude Code）；确认别的程序也不需要时，再删除这些全局变量。"
	}
	e.add(r)

	// C5 自动更新（只有装了直连防护才需要关）
	r = model.Result{ID: "C5", Group: g, Title: ids[3].title, Kind: model.KindFixed, Advanced: true, Target: "DISABLE_AUTOUPDATER=1（装了直连防护时）"}
	r.Current = "DISABLE_AUTOUPDATER=" + orNone(env["DISABLE_AUTOUPDATER"])
	guardOn := f.Guard.Found && strings.HasPrefix(strings.ToLower(f.Guard.Status), "installed")
	switch {
	case !guardOn:
		r.Status, r.Detail = model.NA, "没装直连防护，自动更新开着也没关系。"
	case env["DISABLE_AUTOUPDATER"] == "1" || strings.EqualFold(env["DISABLE_AUTOUPDATER"], "true"):
		r.Status, r.Detail = model.Pass, "自动更新已关，程序不会悄悄变化导致防护失效。"
	default:
		r.Status = model.Warn
		r.Detail = "直连防护按程序哈希绑定，Claude Code 自己更新后防护会对不上。"
		r.FixID = FixCLIAutoUpdate
		r.Advice = "点「一键修复」关闭自动更新；以后按分享包的更新流程手动升级。"
	}
	e.add(r)

	// C6
	e.add(e.reachResult(model.Result{ID: "C6", Group: g, Title: ids[4].title, Kind: model.KindFixed, Target: "api.anthropic.com 可达"}, model.EntryCLI))
}

func mergedEnv(c facts.CLIFacts) map[string]string {
	out := map[string]string{}
	for k, v := range c.Settings.Env {
		out[k] = v
	}
	for k, v := range c.LocalSettings.Env {
		out[k] = v
	}
	return out
}

func sameProxy(a, b string) bool {
	n := func(s string) string {
		s = strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "/"))
		return strings.Replace(s, "localhost", "127.0.0.1", 1)
	}
	return n(a) == n(b)
}

func orUnknown(s string) string {
	if s == "" {
		return "版本未知"
	}
	return s
}

// ---------------------------------------------------------------- Claude Desktop

func (e *ev) desktop() {
	f := e.f
	d := f.Desktop
	g := model.GroupDesktop
	r := model.Result{ID: "D1", Group: g, Title: "Claude Desktop", Kind: model.KindInfo}
	if d.Installed {
		r.Status, r.Current = model.Pass, d.Version
		r.Detail = "MSIX 包：" + d.FullName
		if d.Running {
			r.Detail += "（正在运行）"
		}
	} else {
		r.Status, r.Current = model.NA, "未安装"
		r.Detail = "没检测到 Claude Desktop（应用商店/MSIX 版），本页其余项跳过。"
		if d.Err != "" {
			r.Status, r.Detail = model.Unknown, "查询安装状态失败："+d.Err
		}
		e.add(r)
		for _, x := range []struct{ id, title string }{{"D2", "专用通道（Desktop 代理策略）"}, {"D3", "直连防护"}, {"D4", "Claude 可达（经 Desktop 端口）"}} {
			e.add(model.Result{ID: x.id, Group: g, Title: x.title, Kind: model.KindFixed, Status: model.NA, Detail: "没装 Claude Desktop。", Advanced: x.id == "D3"})
		}
		return
	}
	e.add(r)

	want := "http://127.0.0.1:" + strconv.Itoa(f.Settings.PortDesktop)
	egress, layer, dau := d.EffectivePolicy()
	r = model.Result{ID: "D2", Group: g, Title: "专用通道（Desktop 代理策略）", Kind: model.KindFixed,
		Target: "egressProxyUrl = " + want + "，且同一层 disableAutoUpdates = 1"}
	switch {
	case layer == "":
		r.Status, r.Current = model.Fail, "没有配置"
		r.Detail = "Desktop 没有代理策略，会按系统网络连接（TUN 关时直连）。"
	case !sameProxy(egress, want):
		r.Status, r.Current = model.Fail, layer+"："+orNone(egress)
		r.Detail = "代理地址不是 Desktop 专用端口。"
	case layer != "本地配置" && (dau == nil || *dau != 1):
		r.Status, r.Current = model.Fail, layer+"："+egress+"，disableAutoUpdates 未设"
		r.Detail = "disableAutoUpdates 不在同一层时，Desktop 会忽略这一层的代理设置。"
	default:
		r.Status, r.Current = model.Pass, layer+"："+egress
		r.Detail = "Desktop 的流量走专用端口。"
		if layer == "本地配置" {
			r.Detail += "（用的是本地配置方式）"
		} else {
			r.Detail += "自动更新已关，更新时按分享包流程手动做。"
		}
		if layer != "HKCU" && d.HKCU.Present() {
			r.Status = model.Warn
			r.Detail += "另外 HKCU 下也有一份策略，容易混淆，建议删除。"
		}
	}
	if r.Status != model.Pass {
		r.FixID = FixDesktopPolicy
		r.Advice = "点「一键修复」写入 HKLM 策略（需要管理员，会弹一次 UAC），然后完全退出并重开 Desktop。"
	}
	e.add(r)

	// D3 直连防护
	gd := f.Guard
	r = model.Result{ID: "D3", Group: g, Title: "直连防护", Kind: model.KindFixed, Advanced: true,
		Target: "防护规则与当前程序版本一致"}
	switch {
	case !gd.Found:
		r.Status, r.Current = model.NA, "未安装（可选）"
		r.Detail = "直连防护让 Desktop / Code 只能经代理出网，是分享包的进阶项。"
		if gd.ShareRoot == "" {
			r.Detail += "没找到分享包目录，如已安装可在设置里指定分享包位置。"
		}
	case gd.Err != "":
		r.Status, r.Current, r.Detail = model.Unknown, "证据文件异常", gd.Err
	default:
		var issues, bySig []string
		r.Current = "状态 " + orNone(gd.Status)
		if !strings.HasPrefix(strings.ToLower(gd.Status), "installed") {
			issues = append(issues, "状态不是已安装（"+orNone(gd.Status)+"）")
		}
		if !gd.ManifestOK {
			issues = append(issues, "目标清单被改动过，和安装时不一致")
		}
		if gd.ManifestVersion != "" && d.Version != "" && gd.ManifestVersion != d.Version {
			issues = append(issues, "防护绑定的 Desktop 版本是 "+gd.ManifestVersion+"，当前是 "+d.Version)
		}
		if gd.HashChecked {
			for _, t := range gd.Targets {
				if t.Missing {
					issues = append(issues, t.Name+" 的程序文件不在了")
				} else if t.Changed {
					issues = append(issues, t.Name+" 的程序已更新，和防护记录对不上")
				} else if t.AcceptedBySignature {
					bySig = append(bySig, t.Name)
				}
			}
		}
		sigNote := ""
		if len(bySig) > 0 {
			sigNote = "CLI 已自更新，按 Anthropic 签名放行（" + strings.Join(bySig, "、") + "）。"
		}
		if gd.VerifyAt != "" {
			r.Current += "，最近验证 " + gd.VerifyAt + "（" + orNone(gd.VerifyStatus) + "）"
		}
		switch {
		case len(issues) > 0:
			r.Status, r.Detail = model.Warn, strings.Join(issues, "；")
			if sigNote != "" {
				r.Detail += "。另：" + sigNote
			}
			r.Advice = "按分享包「Desktop防护/更新三端Claude.ps1 -Action Plan」的提示补同步防护。"
		case !gd.HashChecked && e.pending:
			r = e.netPending(r)
			r.Detail = "正在核对程序文件哈希…"
		case !gd.HashChecked:
			r.Status, r.Detail = model.Unknown, "没来得及核对程序文件哈希。"
		default:
			r.Status, r.Detail = model.Pass, "防护证据完整，程序文件和记录一致。"
			if sigNote != "" {
				r.Detail = "防护证据完整；" + sigNote + "其余程序文件和记录一致。"
			}
			r.Detail += "实时规则需要管理员才能读取，这里只核对证据文件。"
		}
	}
	e.add(r)

	e.add(e.reachResult(model.Result{ID: "D4", Group: g, Title: "Claude 可达（经 Desktop 端口）", Kind: model.KindFixed, Target: "claude.ai 可达"}, model.EntryDesktop))
}

// ---------------------------------------------------------------- 系统

func (e *ev) system() {
	f := e.f
	sy := f.System
	t := e.t
	g := model.GroupSystem
	unknownTarget := t.Source == "none"

	// S1 时区
	r := model.Result{ID: "S1", Group: g, Title: "系统时区", Kind: model.KindIP, Current: orNone(sy.WindowsTZ)}
	if off, ok := geo.OffsetMinutes(geo.IANAForWindows(sy.WindowsTZ), f.Now); ok {
		r.Current += "（" + geo.FormatOffset(off) + "）"
	}
	switch {
	case sy.TZErr != "":
		r.Status, r.Detail = model.Unknown, "读取时区失败："+sy.TZErr
	case unknownTarget:
		r.Status, r.Target, r.Detail, r.Pending = model.Unknown, "随出口 IP", t.SourceText, e.pending
	default:
		r.Target = t.IANA
		if t.WindowsTZ != "" {
			r.Target = t.WindowsTZ + "（" + t.IANA + "）"
		}
		curOff, ok1 := geo.OffsetMinutes(geo.IANAForWindows(sy.WindowsTZ), f.Now)
		tgtOff, ok2 := geo.OffsetMinutes(t.IANA, f.Now)
		switch {
		case sy.WindowsTZ != "" && sy.WindowsTZ == t.WindowsTZ:
			r.Status, r.Detail = model.Pass, "和出口所在地一致。"
		case ok1 && ok2 && curOff == tgtOff:
			r.Status, r.Detail = model.Pass, "和出口所在地当前时差一致（"+geo.FormatOffset(curOff)+"）。"
		case !ok1 || !ok2:
			r.Status, r.Detail = model.Unknown, "无法换算时区（"+sy.WindowsTZ+" / "+t.IANA+"）。"
		default:
			r.Status = model.Warn
			r.Detail = fmt.Sprintf("系统是 %s，出口所在地是 %s：网页能读到系统时区，和 IP 对不上。", geo.FormatOffset(curOff), geo.FormatOffset(tgtOff))
		}
		if r.Status == model.Warn && t.WindowsTZ != "" {
			r.FixID = FixTimezone
			r.Advice = "点「一键修复」把系统时区改成 " + t.WindowsTZ + "。改完后已打开的浏览器要重启才生效。"
		}
		if sy.AutoTZ != nil && *sy.AutoTZ {
			r.Detail += "「自动设置时区」开着，Windows 可能按定位把时区改回去"
			if r.FixID != "" {
				r.Detail += "（修复时会一并关闭，需要管理员）"
			}
			r.Detail += "。"
		}
	}
	e.add(r)

	// S2 区域格式与国家
	r = model.Result{ID: "S2", Group: g, Title: "区域格式与「国家或地区」", Kind: model.KindIP, Advanced: true,
		Current: "格式 " + orNone(sy.Locale) + "，国家 " + orNone(sy.GeoName)}
	if unknownTarget {
		r.Status, r.Target, r.Detail, r.Pending = model.Unknown, "随出口 IP", t.SourceText, e.pending
	} else {
		r.Target = "格式 " + t.Locale + "（或英语），国家 " + t.CountryCode
		var warns []string
		if st, why := langVerdictAs("格式", sy.Locale, t); st != model.Pass {
			warns = append(warns, strings.TrimSuffix(why, "。"))
		}
		if sy.GeoName != "" && sy.GeoName != t.CountryCode {
			warns = append(warns, "「国家或地区」是 "+geo.LookupCountry(sy.GeoName, sy.GeoName).Name)
		}
		if len(warns) == 0 {
			r.Status, r.Detail = model.Pass, "和出口地区一致。"
		} else {
			r.Status = model.Warn
			r.Detail = strings.Join(warns, "；") + "。网页一般读不到这两项，但部分桌面应用会读取。"
			r.FixID = FixRegion
			r.Advice = "可选：点「一键修复」改成 " + t.Locale + " / " + t.CountryCode + "（只影响当前用户的日期、数字格式，可还原）。"
		}
	}
	e.add(r)

	// S3 显示语言
	r = model.Result{ID: "S3", Group: g, Title: "Windows 显示语言", Kind: model.KindIP, Advanced: true, Current: orNone(sy.UILang)}
	if unknownTarget {
		r.Status, r.Target, r.Detail, r.Pending = model.Unknown, "随出口 IP", t.SourceText, e.pending
	} else {
		r.Target = t.Lang + "（或英语）"
		st, why := langVerdict(sy.UILang, t)
		if st == model.Pass {
			r.Status, r.Detail = model.Pass, why
		} else {
			r.Status = model.Warn
			r.Detail = why + "Desktop 和没加 --lang 的浏览器会跟随它。"
			r.Advice = "可选，需手动：设置 → 时间和语言 → 语言和区域 → 添加 English (United States) → 设为 Windows 显示语言 → 注销重登。专用浏览器和 Desktop 英文启动器已经单独指定语言，不改也行。"
		}
	}
	e.add(r)

	// S4 IPv6
	r = model.Result{ID: "S4", Group: g, Title: "IPv6 防漏", Kind: model.KindFixed, Target: "IPv6 连不出去，或也走代理出口"}
	cur := "公网 IPv6 地址：" + strconv.Itoa(len(f.IPv6.Global)) + " 个"
	if sy.IPv6Disabled != nil {
		switch *sy.IPv6Disabled {
		case 0x20:
			cur += "，已设 IPv4 优先"
		case 0xFF:
			cur += "，已禁用 IPv6"
		default:
			cur += fmt.Sprintf("，DisabledComponents=0x%X", *sy.IPv6Disabled)
		}
	}
	r.Current = cur
	switch {
	case f.IPv6.Tested && f.IPv6.DirectIP != "":
		r.Current += "；能直连 IPv6，出口 " + f.IPv6.DirectIP
		switch {
		case f.IPv6.DirectCC != "" && t.CountryCode != "" && f.IPv6.DirectCC != t.CountryCode:
			r.Status = model.Fail
			r.Detail = "有 IPv6 流量绕过代理直接出去（在 " + geo.LookupCountry(f.IPv6.DirectCC, "").Name + "），网页可能借此拿到真实位置。"
		case f.IPv6.DirectCC != "" && f.IPv6.DirectCC == t.CountryCode:
			r.Status, r.Detail = model.Pass, "IPv6 也从代理出口出去。"
		default:
			r.Status, r.Detail = model.Warn, "IPv6 能直接连外网，没确认它是否经过代理。"
		}
	case f.IPv6.Tested:
		r.Status, r.Detail = model.Pass, "IPv6 连不出去，不会经 IPv6 泄露。"
	case len(f.IPv6.Global) == 0:
		r.Status, r.Detail = model.Pass, "没有公网 IPv6 地址。"
	case f.Offline:
		r = e.offline(r)
		r.Detail = "有公网 IPv6 地址；离线模式没测它会不会绕过代理。"
	default:
		r = e.netPending(r)
	}
	if r.Status == model.Fail || r.Status == model.Warn {
		r.FixID = FixIPv6
		r.Advice = "点「一键修复」设置「IPv4 优先」（需要管理员，重启电脑后生效，可还原）。"
	}
	e.add(r)

	// S5 系统 DNS 去向（信息）
	r = model.Result{ID: "S5", Group: g, Title: "不走专用端口的程序用的 DNS", Kind: model.KindInfo, Advanced: true}
	switch {
	case f.Offline:
		r = e.offline(r)
	case e.pending:
		r = e.netPending(r)
	case f.SystemDNS == nil || f.SystemDNS.Err != "":
		r.Status = model.Unknown
		if f.SystemDNS != nil {
			r.Detail = "探测失败：" + f.SystemDNS.Err
		}
	case len(f.SystemDNS.Resolvers) == 0:
		r.Status, r.Current, r.Detail = model.Pass, "未暴露", "看不到解析器（加密 DNS 或由代理处理）。"
	default:
		var list []string
		for _, rs := range f.SystemDNS.Resolvers {
			list = append(list, rs.IP+" "+geo.LookupCountry(rs.CountryCode, rs.CountryCode).Name)
		}
		r.Status, r.Current = model.Pass, strings.Join(list, "、")
		r.Detail = "这是普通程序的 DNS 去向，只做参考。国内 DNS 是分流设计的正常现象；Claude 三个入口的 DNS 见各自页签。"
	}
	e.add(r)

	// S6 系统代理（信息）
	r = model.Result{ID: "S6", Group: g, Title: "Windows 系统代理", Kind: model.KindInfo, Advanced: true}
	if sy.ProxyEnable {
		r.Current = "开：" + orNone(sy.ProxyServer)
		r.Status = model.Pass
		r.Detail = "三个入口各走各的端口，不依赖系统代理。"
		if f.Proxy.TunMode != nil && *f.Proxy.TunMode {
			r.Status = model.Warn
			r.Detail = "TUN 已开时，分享包建议关闭系统代理，避免两套机制混用。"
			r.Advice = "在 Clash Verge 里关闭「系统代理」。"
		}
	} else {
		r.Status, r.Current, r.Detail = model.Pass, "关", "三个入口各走各的端口，不依赖系统代理。"
	}
	e.add(r)

	// S7 防火墙阻断
	fw := f.Firewall
	r = model.Result{ID: "S7", Group: g, Title: "Anthropic 网段直连阻断", Kind: model.KindFixed, Advanced: true,
		Target: "出站阻断 160.79.104.0/23"}
	switch {
	case !fw.Read:
		r.Status, r.Detail = model.Unknown, "读取防火墙规则失败："+fw.Err
	case fw.Exists && fw.Active && fw.Block && fw.Out && fw.Range:
		r.Status, r.Current, r.Detail = model.Pass, "已启用（"+fw.Name+"）", "程序绕过代理直连 Anthropic 时会被拦下。"
	case fw.Exists:
		r.Status, r.Current, r.Detail = model.Warn, "规则存在但未启用或内容不对", "规则 "+fw.Name+" 没有生效。"
		r.FixID, r.Advice = FixFirewall, "可选：点「一键修复」重建规则（需要管理员）。"
	default:
		r.Status, r.Current = model.Warn, "未安装（可选）"
		r.Detail = "多一道保险：哪怕某个程序绕过了代理，也连不上 Anthropic 的 API 网段。"
		r.FixID, r.Advice = FixFirewall, "可选：点「一键修复」添加防火墙规则（需要管理员，可还原）。"
	}
	e.add(r)
}
