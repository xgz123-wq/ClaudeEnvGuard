package facts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/chrome"
	"ClaudeEnvGuard/internal/jsonx"
	"ClaudeEnvGuard/internal/logx"
	"ClaudeEnvGuard/internal/manifest"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/netprobe"
	"ClaudeEnvGuard/internal/platform"
	"ClaudeEnvGuard/internal/scriptroot"
)

// DesktopFamily 是 Claude Desktop 的 MSIX 包族名。
const DesktopFamily = "Claude_pzs8sxrjxfjjc"

// 注册表位置。
const (
	KeyPolicy       = `SOFTWARE\Policies\Claude`
	KeyPolicyWow    = `SOFTWARE\WOW6432Node\Policies\Claude`
	KeyUserEnv      = `Environment`
	KeyMachineEnv   = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`
	KeyTZ           = `SYSTEM\CurrentControlSet\Control\TimeZoneInformation`
	KeyTZAuto       = `SYSTEM\CurrentControlSet\Services\tzautoupdate`
	KeyIntl         = `Control Panel\International`
	KeyIntlGeo      = `Control Panel\International\Geo`
	KeyInetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	KeyTcpip6       = `SYSTEM\CurrentControlSet\Services\Tcpip6\Parameters`
	KeyFirewall     = `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\FirewallRules`
	FirewallRuleID  = `Claude-Anthropic-Direct-Block-160.79.104.0-23`
)

// ProxyEnvKeys 是需要关注的代理变量。
var ProxyEnvKeys = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}

// ThirdPartyKeys 把 Claude Code 指向第三方接口或非订阅登录的变量。
var ThirdPartyKeys = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_URL", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"}

var secretKeys = map[string]bool{"ANTHROPIC_AUTH_TOKEN": true, "ANTHROPIC_API_KEY": true}

// CollectorRetries 是本地收集器超时后再跑的次数。刚开机或刚打开卫士时文件没进缓存，第一次常常偏慢。
const CollectorRetries = 1

// slowStep 超过这个时长的子步骤写进日志，方便找出慢在哪。
const slowStep = time.Second

// CollectLocal 跑本地阶段的全部收集器。每个收集器有独立超时，超时后再试一次。
func CollectLocal(ctx context.Context, sys platform.OS, s appconfig.Settings) *Facts {
	f := &Facts{Settings: s, Paths: sys.Paths(), IsAdmin: sys.IsAdmin(), Now: time.Now(),
		Exits: map[model.Entry]*model.Exit{}, Errors: map[string]string{}, Timings: map[string]CollectorTiming{}}
	procs, perr := sys.Processes("chrome.exe", "claude.exe")
	if perr != nil {
		f.Errors["processes"] = perr.Error()
	}
	// 部署清单只读：读坏了按没有清单处理，只记日志
	m, merr := manifest.Load(f.Paths.LocalAppData)
	if merr != nil {
		logx.Printf("部署清单读不了，按没有清单处理：%v", merr)
	}
	f.ManifestFound = m != nil
	for _, p := range procs {
		low := strings.ToLower(p.Exe)
		if strings.EqualFold(p.Name, "claude.exe") && !strings.Contains(low, `\windowsapps\claude_`) {
			f.CLIRunning++
		}
	}
	var mu sync.Mutex
	run := func(name string, timeout time.Duration, fn func() func()) func() {
		return func() {
			t := Guarded(ctx, &mu, f.Errors, name, timeout, fn)
			mu.Lock()
			f.Timings[name] = t
			mu.Unlock()
		}
	}
	parallel(
		run("proxy", 1500*time.Millisecond, func() func() {
			v := collectProxy(sys, s, procs)
			return func() { f.Proxy = v }
		}),
		run("browsers", 2500*time.Millisecond, func() func() {
			list, sel := collectBrowsers(sys, s, procs, m)
			st := time.Now()
			exe := findChrome(sys)
			if d := time.Since(st); d > slowStep {
				logx.Printf("找 Chrome 程序偏慢：%v", d.Round(time.Millisecond))
			}
			return func() { f.Browsers, f.Selected, f.ChromeExe = list, sel, exe }
		}),
		run("cli", 1500*time.Millisecond, func() func() {
			v := collectCLI(sys)
			return func() { f.CLI = v }
		}),
		run("desktop", 1500*time.Millisecond, func() func() {
			v := collectDesktop(sys, procs)
			return func() { f.Desktop = v }
		}),
		run("system", 1500*time.Millisecond, func() func() {
			v := collectSystem(sys)
			g := sys.GlobalIPv6()
			return func() { f.System, f.IPv6.Global = v, g }
		}),
		run("guard", 1500*time.Millisecond, func() func() {
			v := collectGuard(sys, s)
			return func() { f.Guard = v }
		}),
		run("firewall", 1500*time.Millisecond, func() func() {
			v := collectFirewall(sys)
			return func() { f.Firewall = v }
		}),
	)
	return f
}

// CollectorTiming 是一个本地收集器的耗时，写进检查日志。
type CollectorTiming struct {
	Elapsed  time.Duration // 从开始到拿到结果或放弃
	Attempts int           // 一共跑了几次
	OK       bool
}

// String 是日志里的写法，例如「20ms」「2.61s（重试后）」「超时（跑了 2 次）」。
func (t CollectorTiming) String() string {
	d := t.Elapsed.Round(time.Millisecond)
	switch {
	case !t.OK:
		return fmt.Sprintf("%v 失败（跑了 %d 次）", d, t.Attempts)
	case t.Attempts > 1:
		return fmt.Sprintf("%v（重试后）", d)
	}
	return d.String()
}

// Guarded 给收集器加超时、重试与 panic 保护。fn 在后台算出结果并返回一个「写回」函数；
// 超时就再起一次 fn，先完成的那次（包括晚到的第一次）写回；全部超时则不写回，
// 避免和判定逻辑并发读写同一字段。放弃后仍在跑的 fn 读完时把实际耗时记进日志。
func Guarded(ctx context.Context, mu *sync.Mutex, errs map[string]string, name string, timeout time.Duration, fn func() func()) CollectorTiming {
	type outcome struct {
		apply func()
		err   string
	}
	start := time.Now()
	ch := make(chan outcome, CollectorRetries+1)
	launch := func() {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- outcome{err: fmt.Sprintf("内部错误：%v\n%s", r, firstLines(string(debug.Stack()), 8))}
				}
			}()
			ch <- outcome{apply: fn()}
		}()
	}
	launch()
	attempts := 1
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var res outcome
wait:
	for {
		select {
		case res = <-ch:
			break wait
		case <-timer.C:
			if attempts > CollectorRetries {
				res.err = "读取超时"
				break wait
			}
			attempts++
			launch()
			timer.Reset(timeout)
		case <-ctx.Done():
			res.err = "已取消"
			break wait
		}
	}
	t := CollectorTiming{Elapsed: time.Since(start), Attempts: attempts, OK: res.err == ""}
	if res.err == "读取超时" {
		go func() {
			<-ch
			logx.Printf("收集器 %s 放弃后才读完：从开始共 %v", name, time.Since(start).Round(time.Millisecond))
		}()
	}
	mu.Lock()
	defer mu.Unlock()
	if res.err != "" {
		errs[name] = res.err
		return t
	}
	if res.apply != nil {
		res.apply()
	}
	return t
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func parallel(fns ...func()) {
	var wg sync.WaitGroup
	for _, fn := range fns {
		wg.Add(1)
		go func(fn func()) {
			defer wg.Done()
			fn()
		}(fn)
	}
	wg.Wait()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---------------------------------------------------------------- 代理客户端

var (
	reTun     = regexp.MustCompile(`(?m)^enable_tun_mode:\s*(true|false)`)
	reSysProx = regexp.MustCompile(`(?m)^enable_system_proxy:\s*(true|false)`)
	reMixed   = regexp.MustCompile(`(?m)^verge_mixed_port:\s*(\d+)`)
)

// KnownProxyClients 是常见代理客户端核心进程名（小写，无扩展名）。
var KnownProxyClients = []string{"verge-mihomo", "verge-mihomo-alpha", "mihomo", "clash-meta", "clash", "clash-verge", "clash-verge-service",
	"mihomo-party", "v2rayn", "xray", "v2ray", "sing-box", "nekobox", "nekoray", "hiddify", "flclash", "clashx"}

func collectProxy(sys platform.OS, s appconfig.Settings, procs []platform.Process) ProxyFacts {
	pf := ProxyFacts{Owners: map[int]PortOwner{}}
	names := map[int]string{}
	for _, p := range procs {
		names[p.PID] = p.Name
	}
	for _, port := range []int{s.PortCLI, s.PortBrowser, s.PortDesktop} {
		o := PortOwner{Port: port}
		pid, err := sys.PortOwner(port)
		if err != nil {
			o.Err = err.Error()
			o.Listening = netprobe.PortOpen(port, 300*time.Millisecond)
		} else if pid > 0 {
			o.Listening, o.PID, o.Name = true, pid, strings.TrimSuffix(names[pid], ".exe")
		}
		pf.Owners[port] = o
	}
	dir := filepath.Join(sys.Paths().AppData, "io.github.clash-verge-rev.clash-verge-rev")
	if b, err := os.ReadFile(filepath.Join(dir, "verge.yaml")); err == nil {
		pf.VergeFound = true
		txt := string(jsonx.Decode(b))
		if m := reTun.FindStringSubmatch(txt); m != nil {
			v := m[1] == "true"
			pf.TunMode = &v
		}
		if m := reSysProx.FindStringSubmatch(txt); m != nil {
			v := m[1] == "true"
			pf.SystemProxy = &v
		}
		if m := reMixed.FindStringSubmatch(txt); m != nil {
			pf.MixedPort, _ = strconv.Atoi(m[1])
		}
	} else if !os.IsNotExist(err) {
		pf.VergeErr = err.Error()
	}
	// clash-verge.yaml 含节点凭据：只做存在性匹配，内容不保留、不输出
	if b, err := os.ReadFile(filepath.Join(dir, "clash-verge.yaml")); err == nil {
		pf.Listeners = map[string]bool{}
		for _, n := range []string{"account-us-socks", "claude-desktop-http"} {
			re := regexp.MustCompile(`(?m)^\s*-?\s*name:\s*['"]?` + regexp.QuoteMeta(n) + `['"]?\s*$`)
			pf.Listeners[n] = re.Match(b)
		}
	}
	return pf
}

// IsKnownProxy 判断进程名是否是常见代理客户端。
func IsKnownProxy(name string) bool {
	n := strings.ToLower(strings.TrimSuffix(name, ".exe"))
	for _, k := range KnownProxyClients {
		if n == k || strings.HasPrefix(n, k) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- 专用浏览器

// lnkMentions 粗筛：.lnk 里参数以 UTF-16LE 存储，先查字节再用 COM 精读，省时间。
func lnkMentions(path string, needle string) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 1<<20 {
		return false
	}
	u := utf16.Encode([]rune(needle))
	nb := make([]byte, len(u)*2)
	for i, c := range u {
		nb[2*i], nb[2*i+1] = byte(c), byte(c>>8)
	}
	return bytes.Contains(b, nb) || bytes.Contains(b, []byte(needle))
}

func listLnk(dir string, depth int) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if depth > 0 {
				out = append(out, listLnk(p, depth-1)...)
			}
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".lnk") {
			out = append(out, p)
		}
	}
	return out
}

// StandardRoot 是分享包约定的专用浏览器根目录。
func StandardRoot(p platform.Paths) string {
	return filepath.Join(p.LocalAppData, "ClaudeAccountBrowsers")
}

// DefaultChromeUserData 是系统 Chrome 的默认数据目录，专用浏览器不能用它。
func DefaultChromeUserData(p platform.Paths) string {
	return filepath.Join(p.LocalAppData, "Google", "Chrome", "User Data")
}

func collectBrowsers(sys platform.OS, s appconfig.Settings, procs []platform.Process, m *manifest.Manifest) ([]Browser, *Browser) {
	paths := sys.Paths()
	byDir := map[string]*Browser{}
	var order []string
	add := func(b *Browser) *Browser {
		k := chrome.NormDir(b.UserDataDir)
		if ex, ok := byDir[k]; ok {
			return ex
		}
		byDir[k] = b
		order = append(order, k)
		return b
	}
	root := StandardRoot(paths)
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			ud := filepath.Join(root, e.Name(), "UserData")
			add(&Browser{Name: e.Name(), UserDataDir: ud, Standard: true, DirExists: exists(ud)})
		}
	}
	// 只看当前用户桌面：分享包的创建脚本只往这里放快捷方式，一键总检也只扫这里。
	// 开始菜单动辄上百个图标，刚开机时逐个读会超时。
	st := time.Now()
	lnks := listLnk(paths.Desktop, 0)
	for _, l := range lnks {
		if !lnkMentions(l, "user-data-dir") {
			continue
		}
		sc, err := sys.ReadShortcut(l)
		if err != nil {
			continue
		}
		flags := chrome.Parse(sc.Args)
		ud := flags.Get(chrome.FlagUserDataDir)
		if ud == "" {
			continue
		}
		ud = expandPercent(ud, paths)
		name := strings.TrimSuffix(filepath.Base(l), filepath.Ext(l))
		standard := strings.HasPrefix(chrome.NormDir(ud), chrome.NormDir(root)+`\`)
		_, known := byDir[chrome.NormDir(ud)]
		// 标准目录外的只收和 Claude 有关的：名字带 Claude，或走浏览器专用端口
		if !standard && !known && !strings.Contains(strings.ToLower(name), "claude") &&
			!chrome.ProxyOK(flags.Get(chrome.FlagProxyServer), s.PortBrowser) {
			continue
		}
		b := add(&Browser{Name: name, UserDataDir: ud, Standard: strings.HasPrefix(chrome.NormDir(ud), chrome.NormDir(root)+`\`), DirExists: exists(ud)})
		b.Shortcuts = append(b.Shortcuts, ShortcutInfo{Shortcut: sc, Flags: flags})
	}
	// 部署清单登记过的：标记已登记；登记的快捷方式不在桌面时按清单里的路径读，文件没了记下来给 B1 报。
	if m != nil {
		for _, mb := range m.Browsers {
			if mb.UserDataDir == "" {
				continue
			}
			ud := expandPercent(mb.UserDataDir, paths)
			name := mb.Alias
			if name == "" {
				name = filepath.Base(filepath.Dir(ud))
			}
			b := add(&Browser{Name: name, UserDataDir: ud, Standard: strings.HasPrefix(chrome.NormDir(ud), chrome.NormDir(root)+`\`), DirExists: exists(ud)})
			b.Registered = true
			if mb.Shortcut == "" || b.hasShortcut(mb.Shortcut) {
				continue
			}
			if !exists(mb.Shortcut) {
				b.MissingShortcut = mb.Shortcut
				continue
			}
			if sc, err := sys.ReadShortcut(mb.Shortcut); err == nil {
				b.Shortcuts = append(b.Shortcuts, ShortcutInfo{Shortcut: sc, Flags: chrome.Parse(sc.Args)})
			}
		}
	}
	lnkTime := time.Since(st)
	// 运行中的主进程
	for _, p := range procs {
		if !strings.EqualFold(p.Name, "chrome.exe") || p.CmdLine == "" || strings.Contains(p.CmdLine, "--type=") {
			continue
		}
		fl := chrome.Parse(p.CmdLine)
		if b, ok := byDir[chrome.NormDir(fl.Get(chrome.FlagUserDataDir))]; ok {
			b.Running = append(b.Running, p)
		}
	}
	var list []Browser
	for _, k := range order {
		b := byDir[k]
		if st, err := os.Stat(filepath.Join(b.UserDataDir, "Local State")); err == nil {
			b.LastUsed = st.ModTime()
		}
		b.PrefLang = readPrefLang(filepath.Join(b.UserDataDir, "Default", "Preferences"))
		names := map[string]bool{}
		for _, sc := range b.Shortcuts {
			n := strings.TrimSuffix(filepath.Base(sc.Path), filepath.Ext(sc.Path))
			if !strings.Contains(n, b.Name) && !strings.Contains(b.Name, n) {
				names[n] = true
			}
		}
		for n := range names {
			b.DuplicateOf = append(b.DuplicateOf, n)
		}
		sort.Strings(b.DuplicateOf)
		list = append(list, *b)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Standard != list[j].Standard {
			return list[i].Standard
		}
		return list[i].Name < list[j].Name
	})
	if d := time.Since(st); d > slowStep {
		logx.Printf("读专用浏览器偏慢：共 %v，其中桌面快捷方式 %d 个 %v，读 %d 个浏览器资料 %v",
			d.Round(time.Millisecond), len(lnks), lnkTime.Round(time.Millisecond), len(list), (d - lnkTime).Round(time.Millisecond))
	}
	var sel *Browser
	for i := range list {
		if list[i].Name == s.SelectedBrowser {
			sel = &list[i]
		}
	}
	if sel == nil {
		for i := range list {
			if sel == nil || list[i].LastUsed.After(sel.LastUsed) {
				sel = &list[i]
			}
		}
	}
	return list, sel
}

// expandPercent 展开快捷方式里常见的 %LOCALAPPDATA% / %USERPROFILE% / %APPDATA%。
func expandPercent(s string, p platform.Paths) string {
	r := strings.NewReplacer("%LOCALAPPDATA%", p.LocalAppData, "%localappdata%", p.LocalAppData,
		"%USERPROFILE%", p.Home, "%userprofile%", p.Home, "%APPDATA%", p.AppData, "%appdata%", p.AppData)
	return r.Replace(s)
}

func readPrefLang(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var pref struct {
		Intl struct {
			Accept string `json:"accept_languages"`
		} `json:"intl"`
	}
	if jsonx.Unmarshal(b, &pref) != nil {
		return ""
	}
	return pref.Intl.Accept
}

func findChrome(sys platform.OS) string {
	for _, root := range []platform.RegRoot{platform.HKLM, platform.HKCU} {
		v, _ := sys.RegRead(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, "")
		if p := strings.Trim(v.Text(), `"`); p != "" && exists(p) {
			return p
		}
	}
	p := sys.Paths()
	for _, base := range []string{p.ProgramFiles, p.ProgramFilesX86, p.LocalAppData} {
		if base == "" {
			continue
		}
		c := filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe")
		if exists(c) {
			return c
		}
	}
	return ""
}

// ---------------------------------------------------------------- Claude Code

func relevantEnvKey(k string) bool {
	u := strings.ToUpper(k)
	for _, x := range append(append([]string{}, ProxyEnvKeys...), ThirdPartyKeys...) {
		if u == x {
			return true
		}
	}
	return u == "DISABLE_AUTOUPDATER" || u == "ANTHROPIC_API_KEY"
}

// MaskSecret 只露出前后几位。
func MaskSecret(v string) string {
	if len(v) <= 8 {
		return "***"
	}
	return v[:4] + "…" + v[len(v)-2:]
}

func readSettings(p string) SettingsFile {
	sf := SettingsFile{Path: p, Env: map[string]string{}}
	b, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			sf.Err = "读取失败：" + err.Error()
		}
		return sf
	}
	sf.Exists = true
	obj, err := jsonx.ParseObject(b)
	if err != nil {
		sf.Err = "JSON 格式错误：" + err.Error()
		return sf
	}
	env, err := obj.ChildObject("env", false)
	if err != nil {
		sf.Err = "env 字段格式错误"
		return sf
	}
	if env == nil {
		return sf
	}
	for _, k := range env.Keys {
		v, _ := env.Vals[k].(string)
		if !relevantEnvKey(k) {
			sf.OtherEnvKeys = append(sf.OtherEnvKeys, k)
			continue
		}
		if secretKeys[strings.ToUpper(k)] {
			v = MaskSecret(v)
		}
		sf.Env[strings.ToUpper(k)] = v
	}
	return sf
}

func readEnvScope(sys platform.OS, root platform.RegRoot, key string) (map[string]string, error) {
	out := map[string]string{}
	names, err := sys.RegValueNames(root, key)
	if err != nil {
		return out, err
	}
	for _, n := range names {
		if !relevantEnvKey(n) {
			continue
		}
		v, err := sys.RegRead(root, key, n)
		if err != nil || !v.Exists {
			continue
		}
		val := v.Text()
		if secretKeys[strings.ToUpper(n)] {
			val = MaskSecret(val)
		}
		out[strings.ToUpper(n)] = val
	}
	return out, nil
}

func collectCLI(sys platform.OS) CLIFacts {
	p := sys.Paths()
	c := CLIFacts{}
	npmPkg := filepath.Join(p.AppData, "npm", "node_modules", "@anthropic-ai", "claude-code")
	cands := []string{
		filepath.Join(npmPkg, "bin", "claude.exe"),
		filepath.Join(p.Home, ".local", "bin", "claude.exe"),
		filepath.Join(p.AppData, "npm", "claude.cmd"),
	}
	for _, dir := range filepath.SplitList(sys.Getenv("PATH")) {
		if strings.Contains(strings.ToLower(dir), `\windowsapps`) {
			continue // Desktop 的应用别名也叫 claude.exe，要排除
		}
		cands = append(cands, filepath.Join(dir, "claude.exe"), filepath.Join(dir, "claude.cmd"))
	}
	for _, cand := range cands {
		if exists(cand) {
			c.Installed, c.Exe = true, cand
			break
		}
	}
	if b, err := os.ReadFile(filepath.Join(npmPkg, "package.json")); err == nil {
		var pkg struct {
			Version string `json:"version"`
		}
		if jsonx.Unmarshal(b, &pkg) == nil {
			c.Version = pkg.Version
		}
	}
	c.Settings = readSettings(filepath.Join(p.Home, ".claude", "settings.json"))
	c.LocalSettings = readSettings(filepath.Join(p.Home, ".claude", "settings.local.json"))
	if c.Settings.Exists || exists(filepath.Join(p.Home, ".claude", ".credentials.json")) {
		c.Installed = c.Installed || c.Exe != ""
	}
	var err1, err2 error
	c.UserEnv, err1 = readEnvScope(sys, platform.HKCU, KeyUserEnv)
	c.MachineEnv, err2 = readEnvScope(sys, platform.HKLM, KeyMachineEnv)
	if err1 != nil || err2 != nil {
		c.EnvErr = fmt.Sprint(err1, err2)
	}
	return c
}

// ---------------------------------------------------------------- Claude Desktop

func readPolicy(sys platform.OS, root platform.RegRoot, key, name string) PolicyLayer {
	pl := PolicyLayer{Name: name}
	v, err := sys.RegRead(root, key, "egressProxyUrl")
	if err != nil {
		pl.Err = err.Error()
	} else if v.Exists {
		pl.HasEgress, pl.Egress = true, strings.TrimRight(strings.TrimSpace(v.Text()), "/")
	}
	d, err := sys.RegRead(root, key, "disableAutoUpdates")
	if err == nil && d.Exists {
		n := d.DWord
		if d.Type != "dword" {
			x, _ := strconv.Atoi(strings.TrimSpace(d.Str))
			n = uint32(x)
		}
		pl.DisableAU = &n
	}
	return pl
}

func collectDesktop(sys platform.OS, procs []platform.Process) DesktopFacts {
	d := DesktopFacts{}
	full, path, err := sys.PackageByFamily(DesktopFamily)
	if err != nil {
		d.Err = err.Error()
	}
	if full != "" {
		d.Installed, d.FullName, d.InstallPath = true, full, path
		if parts := strings.Split(full, "_"); len(parts) > 1 {
			d.Version = parts[1]
		}
		if path != "" {
			d.Exe = filepath.Join(path, "app", "claude.exe")
		}
	}
	d.HKLM = readPolicy(sys, platform.HKLM, KeyPolicy, "HKLM")
	d.HKLMWow = readPolicy(sys, platform.HKLM, KeyPolicyWow, "HKLM(32位)")
	d.HKCU = readPolicy(sys, platform.HKCU, KeyPolicy, "HKCU")
	lib := filepath.Join(sys.Paths().LocalAppData, "Claude-3p", "configLibrary")
	if b, err := os.ReadFile(filepath.Join(lib, "_meta.json")); err == nil {
		var meta struct {
			Applied string `json:"appliedId"`
		}
		if err := jsonx.Unmarshal(b, &meta); err != nil {
			d.LocalErr = "_meta.json 格式错误"
		} else if regexp.MustCompile(`^[a-fA-F0-9-]{36}$`).MatchString(meta.Applied) {
			if pb, err := os.ReadFile(filepath.Join(lib, meta.Applied+".json")); err == nil {
				var prof struct {
					Egress string `json:"egressProxyUrl"`
				}
				if jsonx.Unmarshal(pb, &prof) == nil {
					d.LocalFound, d.LocalEgress = true, strings.TrimRight(prof.Egress, "/")
				}
			}
		}
	}
	for _, p := range procs {
		if !strings.EqualFold(p.Name, "claude.exe") {
			continue
		}
		low := strings.ToLower(p.Exe)
		if (d.InstallPath != "" && strings.HasPrefix(low, strings.ToLower(d.InstallPath))) || strings.Contains(low, `\windowsapps\claude_`) {
			d.Running = true
		}
	}
	return d
}

// EffectivePolicy 按 HKLM > HKCU > 本地配置 的优先级返回生效的代理设置和来源层。
func (d DesktopFacts) EffectivePolicy() (egress string, layer string, disableAU *uint32) {
	for _, l := range []PolicyLayer{d.HKLM, d.HKLMWow, d.HKCU} {
		if l.Present() {
			return l.Egress, l.Name, l.DisableAU
		}
	}
	if d.LocalFound {
		return d.LocalEgress, "本地配置", nil
	}
	return "", "", nil
}

// ---------------------------------------------------------------- 系统

func collectSystem(sys platform.OS) SystemFacts {
	s := SystemFacts{}
	if v, err := sys.RegRead(platform.HKLM, KeyTZ, "TimeZoneKeyName"); err != nil {
		s.TZErr = err.Error()
	} else {
		s.WindowsTZ = strings.TrimSpace(v.Text())
	}
	if v, _ := sys.RegRead(platform.HKLM, KeyTZAuto, "Start"); v.Exists {
		on := v.DWord != 4
		s.AutoTZ = &on
	}
	if v, _ := sys.RegRead(platform.HKCU, KeyIntl, "LocaleName"); v.Exists {
		s.Locale = v.Text()
	}
	if v, _ := sys.RegRead(platform.HKCU, KeyIntlGeo, "Name"); v.Exists {
		s.GeoName = strings.ToUpper(v.Text())
	}
	s.UILang = sys.UILanguage()
	if v, _ := sys.RegRead(platform.HKCU, KeyInetSettings, "ProxyEnable"); v.Exists {
		s.ProxyEnable = v.DWord == 1
	}
	if v, _ := sys.RegRead(platform.HKCU, KeyInetSettings, "ProxyServer"); v.Exists {
		s.ProxyServer = v.Text()
	}
	if v, _ := sys.RegRead(platform.HKLM, KeyTcpip6, "DisabledComponents"); v.Exists {
		n := v.DWord
		s.IPv6Disabled = &n
	}
	return s
}

// ---------------------------------------------------------------- 分享包直连防护

func collectGuard(sys platform.OS, s appconfig.Settings) GuardFacts {
	g := GuardFacts{ShareRoot: scriptroot.Root(sys.Paths(), s.SharePackageRoot)}
	if g.ShareRoot == "" {
		return g
	}
	dir := filepath.Join(scriptroot.DocDir(g.ShareRoot), "Desktop防护")
	sb, err := os.ReadFile(filepath.Join(dir, "应用直连防护状态.json"))
	if err != nil {
		return g
	}
	g.Found = true
	var state struct {
		Status         string `json:"Status"`
		ManifestSHA256 string `json:"ManifestSHA256"`
	}
	if err := jsonx.Unmarshal(sb, &state); err != nil {
		g.Err = "状态文件格式错误"
		return g
	}
	g.Status = state.Status
	mb, err := os.ReadFile(filepath.Join(dir, "应用过滤目标.json"))
	if err != nil {
		g.Err = "找不到 应用过滤目标.json"
		return g
	}
	sum := sha256.Sum256(mb)
	g.ManifestOK = strings.EqualFold(hex.EncodeToString(sum[:]), state.ManifestSHA256)
	var man struct {
		PackageVersion string `json:"PackageVersion"`
		Targets        []struct {
			Name   string `json:"Name"`
			Path   string `json:"Path"`
			SHA256 string `json:"SHA256"`
		} `json:"Targets"`
	}
	if err := jsonx.Unmarshal(mb, &man); err != nil {
		g.Err = "应用过滤目标.json 格式错误"
		return g
	}
	g.ManifestVersion = man.PackageVersion
	for _, t := range man.Targets {
		g.Targets = append(g.Targets, GuardTarget{Name: t.Name, Path: t.Path, Expected: t.SHA256})
	}
	if vb, err := os.ReadFile(filepath.Join(dir, "应用直连防护-Verify.json")); err == nil {
		var v struct {
			Status string `json:"Status"`
			At     string `json:"At"`
		}
		if jsonx.Unmarshal(vb, &v) == nil {
			g.VerifyStatus, g.VerifyAt = v.Status, v.At
		}
	}
	return g
}

// AnthropicSignerOrg 是 Claude 程序签名证书的组织名。
const AnthropicSignerOrg = "Anthropic, PBC"

// signatureAcceptable 判断哈希不一致的目标能否按签名放行。和分享包总检 Test-GuardBinary 一致：
// 只有名字以「Claude Code npm」开头的目标（CLI 会原地自更新，路径不变，WFP 规则仍生效）；
// Desktop 和 VS Code 插件只认登记哈希。
func signatureAcceptable(name string) bool {
	return strings.HasPrefix(name, "Claude Code npm")
}

// HashGuardTargets 计算被防护程序的哈希（较慢，放在联网阶段做）。
// CLI 目标哈希对不上时再校验签名，有效且签名方是 Anthropic 就放行。
func HashGuardTargets(ctx context.Context, sys platform.OS, g *GuardFacts) {
	for i := range g.Targets {
		if ctx.Err() != nil {
			return
		}
		t := &g.Targets[i]
		fh, err := os.Open(t.Path)
		if err != nil {
			t.Missing = true
			continue
		}
		h := sha256.New()
		_, err = io.Copy(h, &ctxReader{ctx: ctx, r: fh})
		fh.Close()
		if err != nil {
			return
		}
		t.Changed = !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), t.Expected)
		if t.Changed && signatureAcceptable(t.Name) {
			if sig, err := sys.FileSignature(t.Path); err == nil {
				t.Signer = sig.Subject
				if sig.Valid && sig.Org == AnthropicSignerOrg {
					t.Changed, t.AcceptedBySignature = false, true
				}
			}
		}
	}
	g.HashChecked = true
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// ---------------------------------------------------------------- 防火墙阻断规则

func parseFirewallRule(v string) (active, block, out, rng bool) {
	for _, part := range strings.Split(v, "|") {
		k, val, _ := strings.Cut(part, "=")
		switch strings.ToLower(k) {
		case "active":
			active = strings.EqualFold(val, "TRUE")
		case "action":
			block = strings.EqualFold(val, "Block")
		case "dir":
			out = strings.EqualFold(val, "Out")
		case "ra4":
			if val == "160.79.104.0/255.255.254.0" || val == "160.79.104.0/23" {
				rng = true
			}
		}
	}
	return
}

func collectFirewall(sys platform.OS) FirewallFacts {
	fw := FirewallFacts{}
	v, err := sys.RegRead(platform.HKLM, KeyFirewall, FirewallRuleID)
	if err != nil {
		fw.Err = err.Error()
		return fw
	}
	fw.Read = true
	if !v.Exists {
		// 名字不同但内容等价的规则也算
		names, _ := sys.RegValueNames(platform.HKLM, KeyFirewall)
		for _, n := range names {
			rv, err := sys.RegRead(platform.HKLM, KeyFirewall, n)
			if err != nil || !strings.Contains(rv.Str, "160.79.104.0") {
				continue
			}
			if a, b, o, r := parseFirewallRule(rv.Str); b && o && r {
				fw.Exists, fw.Name, fw.Active, fw.Block, fw.Out, fw.Range = true, n, a, b, o, r
				if a {
					return fw
				}
			}
		}
		return fw
	}
	fw.Exists, fw.Name = true, FirewallRuleID
	fw.Active, fw.Block, fw.Out, fw.Range = parseFirewallRule(v.Str)
	return fw
}

// Exists 判断路径是否存在（供判定逻辑使用）。
func Exists(p string) bool { return exists(p) }
