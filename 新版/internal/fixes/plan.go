// Package fixes 生成修复计划、执行修复（先备份）、按备份还原。
// 计划只由检查结果和现场事实推出；执行时每一步先记录「改之前是什么」，还原按记录倒序恢复。
package fixes

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ClaudeEnvGuard/internal/checks"
	"ClaudeEnvGuard/internal/chrome"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/geo"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

// Options 是生成计划时的用户输入。
type Options struct {
	Alias string // 新建专用浏览器的名字
}

// AliasPattern 和分享包创建脚本一致：字母、数字、下划线、短横线，1–40 个字符。
var AliasPattern = regexp.MustCompile(`^[\p{L}\p{N}_-]{1,40}$`)

// ShortcutName 按分享包约定生成快捷方式文件名。
func ShortcutName(alias string) string {
	if strings.Contains(strings.ToLower(alias), "claude") {
		return alias + "-专用Chrome.lnk"
	}
	return "Claude-" + alias + "-专用Chrome.lnk"
}

// Plan 根据检查结果生成修复计划。只为状态是警告或失败、且带 FixID 的项生成动作。
func Plan(f *facts.Facts, t model.Target, results []model.Result, opt Options) model.FixPlan {
	plan := model.FixPlan{Target: t}
	need := map[string][]model.Result{}
	for _, r := range results {
		if r.FixID == "" || r.Pending || (r.Status != model.Warn && r.Status != model.Fail) {
			continue
		}
		need[r.FixID] = append(need[r.FixID], r)
	}
	add := func(a *model.FixAction, rs []model.Result) {
		if a == nil {
			return
		}
		for _, r := range rs {
			a.CheckIDs = append(a.CheckIDs, r.ID)
		}
		plan.Actions = append(plan.Actions, *a)
	}
	anyFail := func(rs []model.Result) bool {
		for _, r := range rs {
			if r.Status == model.Fail {
				return true
			}
		}
		return false
	}
	for _, id := range []string{checks.FixBrowserCreate, checks.FixBrowserShortcut, checks.FixCLIProxy, checks.FixCLIThirdParty,
		checks.FixCLIEnvProxy, checks.FixCLIAutoUpdate, checks.FixDesktopPolicy, checks.FixTimezone, checks.FixRegion,
		checks.FixIPv6, checks.FixFirewall} {
		rs, ok := need[id]
		if !ok {
			continue
		}
		switch id {
		case checks.FixBrowserCreate:
			a, note := planBrowserCreate(f, t, opt)
			if note != "" {
				plan.Notes = append(plan.Notes, note)
			}
			add(a, rs)
		case checks.FixBrowserShortcut:
			add(planBrowserShortcut(f, t, rs), rs)
		case checks.FixCLIProxy:
			add(planCLIProxy(f), rs)
		case checks.FixCLIThirdParty:
			a := planCLIThirdParty(f)
			if a != nil {
				a.DefaultOn = anyFail(rs)
			}
			add(a, rs)
		case checks.FixCLIEnvProxy:
			add(planCLIEnvProxy(f), rs)
		case checks.FixCLIAutoUpdate:
			add(planCLIAutoUpdate(f), rs)
		case checks.FixDesktopPolicy:
			add(planDesktopPolicy(f), rs)
		case checks.FixTimezone:
			add(planTimezone(f, t), rs)
		case checks.FixRegion:
			add(planRegion(f, t), rs)
		case checks.FixIPv6:
			a := planIPv6(f)
			a.DefaultOn = anyFail(rs)
			add(a, rs)
		case checks.FixFirewall:
			add(planFirewall(f), rs)
		}
	}
	if len(plan.Actions) == 0 {
		plan.Notes = append(plan.Notes, "没有需要一键修复的项。")
	}
	return plan
}

func step(op string, kv ...string) model.Step {
	p := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		p[kv[i]] = kv[i+1]
	}
	return model.Step{Op: op, Params: p}
}

// ---------------------------------------------------------------- 浏览器

func pickLang(cur string, t model.Target) (lang, accept string) {
	if t.Source != "none" && t.Lang != "" {
		b := geo.LangBase(cur)
		if cur != "" && (b == geo.LangBase(t.Lang) || b == "en") {
			return cur, ""
		}
		return t.Lang, t.AcceptLang
	}
	if cur != "" && geo.LangBase(cur) != "zh" {
		return cur, ""
	}
	return "en-US", "en-US,en"
}

func shortcutAction(f *facts.Facts, t model.Target, b *facts.Browser, sc platform.Shortcut, isNew bool) (model.Step, []model.Change) {
	old := chrome.Parse(sc.Args)
	lang, accept := pickLang(old.Get(chrome.FlagLang), t)
	if accept == "" {
		accept = old.Get(chrome.FlagAcceptLang)
		if accept == "" || geo.LangBase(strings.Split(accept, ",")[0]) != geo.LangBase(lang) {
			accept = geo.AcceptLangFor(lang)
		}
	}
	want := chrome.Want{UserDataDir: b.UserDataDir, SocksPort: f.Settings.PortBrowser, Lang: lang, AcceptLang: accept, StartURL: f.Settings.StartURL}
	newArgs := chrome.Build(sc.Args, want)
	target := sc.Target
	if f.ChromeExe != "" && (strings.ToLower(filepath.Base(target)) != "chrome.exe" || !facts.Exists(target)) {
		target = f.ChromeExe
	}
	workDir := sc.WorkDir
	if workDir == "" || target != sc.Target {
		workDir = filepath.Dir(target)
	}
	icon := sc.Icon
	if icon == "" || target != sc.Target {
		icon = target + ",0"
	}
	desc := sc.Description
	if desc == "" {
		desc = "Claude 独立账号浏览器：" + b.Name
	}
	st := step("shortcut_write", "path", sc.Path, "target", target, "args", newArgs, "workdir", workDir, "icon", icon, "desc", desc)
	var ch []model.Change
	name := filepath.Base(sc.Path)
	if isNew {
		ch = append(ch, model.Change{What: "新建桌面快捷方式 " + name, From: "无", To: target + " " + newArgs})
		return st, ch
	}
	if target != sc.Target {
		ch = append(ch, model.Change{What: name + " 启动程序", From: orNone(sc.Target), To: target})
	}
	nf := chrome.Parse(newArgs)
	for _, k := range []string{chrome.FlagUserDataDir, chrome.FlagProxyServer, chrome.FlagHostResolver, chrome.FlagLang, chrome.FlagAcceptLang, chrome.FlagWebRTCPolicy} {
		if o, n := old.Get(k), nf.Get(k); o != n {
			ch = append(ch, model.Change{What: name + " " + k, From: orNone(o), To: n})
		}
	}
	if len(old.URLs) == 0 && len(nf.URLs) > 0 {
		ch = append(ch, model.Change{What: name + " 首页", From: "无", To: strings.Join(nf.URLs, " ")})
	}
	return st, ch
}

func orNone(s string) string {
	if s == "" {
		return "未设置"
	}
	return s
}

func planBrowserShortcut(f *facts.Facts, t model.Target, rs []model.Result) *model.FixAction {
	b := f.Selected
	if b == nil {
		return nil
	}
	a := &model.FixAction{ID: checks.FixBrowserShortcut, Title: "补全专用浏览器「" + b.Name + "」的快捷方式参数", DefaultOn: true,
		Note: "改完后请关掉这个浏览器的全部窗口，再从桌面快捷方式重新打开，新参数才会生效。"}
	if len(b.Shortcuts) == 0 {
		if f.ChromeExe == "" {
			return nil
		}
		sc := platform.Shortcut{Path: filepath.Join(f.Paths.Desktop, ShortcutName(b.Name)), Target: f.ChromeExe}
		st, ch := shortcutAction(f, t, b, sc, true)
		a.Steps, a.Changes = append(a.Steps, st), ch
		a.Title = "为专用浏览器「" + b.Name + "」新建桌面快捷方式"
		return a
	}
	for _, sc := range b.Shortcuts {
		st, ch := shortcutAction(f, t, b, sc.Shortcut, false)
		if len(ch) == 0 {
			continue
		}
		a.Steps, a.Changes = append(a.Steps, st), append(a.Changes, ch...)
	}
	if len(a.Steps) == 0 {
		return nil
	}
	return a
}

// UniqueAlias 在别名已被占用时加序号。
func UniqueAlias(f *facts.Facts, alias string) string {
	root := facts.StandardRoot(f.Paths)
	cand := alias
	for i := 2; i < 100; i++ {
		if !facts.Exists(filepath.Join(root, cand)) && !facts.Exists(filepath.Join(f.Paths.Desktop, ShortcutName(cand))) {
			return cand
		}
		cand = alias + strconv.Itoa(i)
	}
	return cand
}

func planBrowserCreate(f *facts.Facts, t model.Target, opt Options) (*model.FixAction, string) {
	if f.ChromeExe == "" {
		return nil, "没找到 Google Chrome，无法新建专用浏览器：请先安装 Chrome。"
	}
	alias := strings.TrimSpace(opt.Alias)
	if alias == "" {
		alias = UniqueAlias(f, "账号1")
	}
	if !AliasPattern.MatchString(alias) {
		return nil, "浏览器名字只能用字母、数字、下划线和短横线（1–40 个字符）。"
	}
	ud := filepath.Join(facts.StandardRoot(f.Paths), alias, "UserData")
	b := &facts.Browser{Name: alias, UserDataDir: ud, Standard: true}
	sc := platform.Shortcut{Path: filepath.Join(f.Paths.Desktop, ShortcutName(alias)), Target: f.ChromeExe}
	st, ch := shortcutAction(f, t, b, sc, true)
	a := &model.FixAction{ID: checks.FixBrowserCreate, Title: "新建专用浏览器「" + alias + "」", DefaultOn: true,
		Note:    "建好后双击桌面上的「" + strings.TrimSuffix(ShortcutName(alias), ".lnk") + "」，在里面登录 Claude；一个专用浏览器只登一个账号。",
		Steps:   []model.Step{step("mkdir", "path", ud), st},
		Changes: append([]model.Change{{What: "新建数据目录", From: "无", To: ud}}, ch...)}
	return a, ""
}

// ---------------------------------------------------------------- Claude Code

func settingsPath(f *facts.Facts, local bool) string {
	name := "settings.json"
	if local {
		name = "settings.local.json"
	}
	return filepath.Join(f.Paths.Home, ".claude", name)
}

func jsonEnvStep(path string, set map[string]string, del []string) model.Step {
	b, _ := json.Marshal(set)
	sort.Strings(del)
	return step("json_env", "path", path, "set", string(b), "delete", strings.Join(del, ","))
}

func planCLIProxy(f *facts.Facts) *model.FixAction {
	want := "http://127.0.0.1:" + strconv.Itoa(f.Settings.PortCLI)
	env := f.CLI.Settings.Env
	set := map[string]string{}
	var ch []model.Change
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		if !strings.EqualFold(strings.TrimRight(env[k], "/"), want) {
			set[k] = want
			ch = append(ch, model.Change{What: "settings.json env." + k, From: orNone(env[k]), To: want})
		}
	}
	np := env["NO_PROXY"]
	parts := []string{}
	if np != "" {
		parts = strings.Split(np, ",")
	}
	have := map[string]bool{}
	for _, p := range parts {
		have[strings.ToLower(strings.TrimSpace(p))] = true
	}
	changed := false
	for _, need := range []string{"localhost", "127.0.0.1"} {
		if !have[need] {
			parts = append(parts, need)
			changed = true
		}
	}
	if np == "" {
		parts, changed = []string{"localhost", "127.0.0.1", "*.local"}, true
	}
	if changed {
		set["NO_PROXY"] = strings.Join(parts, ",")
		ch = append(ch, model.Change{What: "settings.json env.NO_PROXY", From: orNone(np), To: set["NO_PROXY"]})
	}
	a := &model.FixAction{ID: checks.FixCLIProxy, Title: "Claude Code 走 CLI 专用端口", DefaultOn: true,
		Note: "只改 env 里的代理字段，其他配置原样保留。改完重启 Claude Code 生效。"}
	if len(set) > 0 {
		a.Steps = append(a.Steps, jsonEnvStep(settingsPath(f, false), set, nil))
	}
	// settings.local.json 优先级更高：里面写错的代理字段删掉，让 settings.json 生效
	var del []string
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
		if v, ok := f.CLI.LocalSettings.Env[k]; ok && !strings.EqualFold(strings.TrimRight(v, "/"), want) {
			del = append(del, k)
			ch = append(ch, model.Change{What: "settings.local.json env." + k, From: v, To: "删除（改由 settings.json 决定）"})
		}
	}
	if len(del) > 0 {
		a.Steps = append(a.Steps, jsonEnvStep(settingsPath(f, true), nil, del))
	}
	a.Changes = ch
	if len(a.Steps) == 0 {
		return nil
	}
	return a
}

func planCLIThirdParty(f *facts.Facts) *model.FixAction {
	a := &model.FixAction{ID: checks.FixCLIThirdParty, Title: "删除指向第三方接口的变量", Note: "会先备份，可在「还原」里恢复。"}
	for _, local := range []bool{false, true} {
		sf := f.CLI.Settings
		label := "settings.json"
		if local {
			sf, label = f.CLI.LocalSettings, "settings.local.json"
		}
		var del []string
		for _, k := range facts.ThirdPartyKeys {
			if v, ok := sf.Env[k]; ok {
				del = append(del, k)
				a.Changes = append(a.Changes, model.Change{What: label + " env." + k, From: v, To: "删除"})
			}
		}
		if len(del) > 0 {
			a.Steps = append(a.Steps, jsonEnvStep(settingsPath(f, local), nil, del))
		}
	}
	userEnvSteps(a, f, facts.ThirdPartyKeys, func(string) bool { return true })
	if len(a.Steps) == 0 {
		return nil
	}
	return a
}

func userEnvSteps(a *model.FixAction, f *facts.Facts, keys []string, match func(v string) bool) {
	for _, k := range keys {
		if v, ok := f.CLI.UserEnv[k]; ok && match(v) {
			a.Steps = append(a.Steps, step("reg_delete", "root", "HKCU", "key", facts.KeyUserEnv, "name", k))
			a.Changes = append(a.Changes, model.Change{What: "用户环境变量 " + k, From: v, To: "删除"})
		}
	}
	for _, k := range keys {
		if v, ok := f.CLI.MachineEnv[k]; ok && match(v) {
			a.Steps = append(a.Steps, step("reg_delete", "root", "HKLM", "key", facts.KeyMachineEnv, "name", k))
			a.Changes = append(a.Changes, model.Change{What: "系统环境变量 " + k, From: v, To: "删除（需要管理员）"})
			a.NeedsAdmin = true
		}
	}
}

func planCLIEnvProxy(f *facts.Facts) *model.FixAction {
	want := "http://127.0.0.1:" + strconv.Itoa(f.Settings.PortCLI)
	a := &model.FixAction{ID: checks.FixCLIEnvProxy, Title: "删除指向别处的全局代理变量",
		Note: "这些变量影响所有程序；确认其他软件不依赖它们再勾选。"}
	userEnvSteps(a, f, []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"}, func(v string) bool {
		return !strings.EqualFold(strings.TrimRight(v, "/"), want)
	})
	if len(a.Steps) == 0 {
		return nil
	}
	return a
}

func planCLIAutoUpdate(f *facts.Facts) *model.FixAction {
	return &model.FixAction{ID: checks.FixCLIAutoUpdate, Title: "关闭 Claude Code 自动更新", DefaultOn: true,
		Note:    "以后按分享包的更新流程手动升级，升级后同步直连防护。",
		Changes: []model.Change{{What: "settings.json env.DISABLE_AUTOUPDATER", From: orNone(f.CLI.Settings.Env["DISABLE_AUTOUPDATER"]), To: "1"}},
		Steps:   []model.Step{jsonEnvStep(settingsPath(f, false), map[string]string{"DISABLE_AUTOUPDATER": "1"}, nil)}}
}

// ---------------------------------------------------------------- Desktop

func planDesktopPolicy(f *facts.Facts) *model.FixAction {
	want := "http://127.0.0.1:" + strconv.Itoa(f.Settings.PortDesktop)
	d := f.Desktop
	a := &model.FixAction{ID: checks.FixDesktopPolicy, Title: "Claude Desktop 走 Desktop 专用端口", NeedsAdmin: true, DefaultOn: true,
		Note: "写的是 HKLM 策略，会弹一次 UAC。完全退出 Claude Desktop（含托盘）后重开生效。"}
	a.Steps = append(a.Steps,
		step("reg_set", "root", "HKLM", "key", facts.KeyPolicy, "name", "egressProxyUrl", "type", "sz", "value", want),
		step("reg_set", "root", "HKLM", "key", facts.KeyPolicy, "name", "disableAutoUpdates", "type", "dword", "value", "1"))
	a.Changes = append(a.Changes,
		model.Change{What: `HKLM\` + facts.KeyPolicy + ` egressProxyUrl`, From: orNone(d.HKLM.Egress), To: want},
		model.Change{What: `HKLM\` + facts.KeyPolicy + ` disableAutoUpdates`, From: dwordText(d.HKLM.DisableAU), To: "1"})
	if d.HKCU.Present() {
		a.Steps = append(a.Steps,
			step("reg_delete", "root", "HKCU", "key", facts.KeyPolicy, "name", "egressProxyUrl"),
			step("reg_delete", "root", "HKCU", "key", facts.KeyPolicy, "name", "disableAutoUpdates"))
		a.Changes = append(a.Changes, model.Change{What: `HKCU\` + facts.KeyPolicy, From: "有一份策略", To: "删除（避免和 HKLM 混淆）"})
	}
	return a
}

func dwordText(p *uint32) string {
	if p == nil {
		return "未设置"
	}
	return strconv.Itoa(int(*p))
}

// ---------------------------------------------------------------- 系统

func planTimezone(f *facts.Facts, t model.Target) *model.FixAction {
	if t.WindowsTZ == "" {
		return nil
	}
	a := &model.FixAction{ID: checks.FixTimezone, Title: "系统时区改为 " + t.WindowsTZ, DefaultOn: true,
		Note:    "已打开的浏览器要重启后网页才会读到新时区。",
		Steps:   []model.Step{step("tz_set", "id", t.WindowsTZ)},
		Changes: []model.Change{{What: "系统时区", From: orNone(f.System.WindowsTZ), To: t.WindowsTZ + "（" + t.IANA + "）"}}}
	if f.System.AutoTZ != nil && *f.System.AutoTZ {
		a.NeedsAdmin = true
		a.Steps = append([]model.Step{step("reg_set", "root", "HKLM", "key", facts.KeyTZAuto, "name", "Start", "type", "dword", "value", "4")}, a.Steps...)
		a.Changes = append(a.Changes, model.Change{What: "自动设置时区", From: "开", To: "关（否则 Windows 会按定位改回去）"})
	}
	return a
}

func planRegion(f *facts.Facts, t model.Target) *model.FixAction {
	if t.Locale == "" || t.CountryCode == "" {
		return nil
	}
	return &model.FixAction{ID: checks.FixRegion, Title: "区域格式与国家改为 " + t.Locale + " / " + t.Country,
		Note:    "只影响当前用户的日期、数字格式和「国家或地区」，不改显示语言。",
		Steps:   []model.Step{step("region_set", "locale", t.Locale, "cc", t.CountryCode)},
		Changes: []model.Change{{What: "区域格式", From: orNone(f.System.Locale), To: t.Locale}, {What: "国家或地区", From: orNone(f.System.GeoName), To: t.CountryCode}}}
}

func planIPv6(f *facts.Facts) *model.FixAction {
	return &model.FixAction{ID: checks.FixIPv6, Title: "IPv6 设为「IPv4 优先」", NeedsAdmin: true,
		Note:    "需要重启电脑后生效；不会断网，只是让系统优先走 IPv4（经代理）。",
		Steps:   []model.Step{step("reg_set", "root", "HKLM", "key", facts.KeyTcpip6, "name", "DisabledComponents", "type", "dword", "value", "32")},
		Changes: []model.Change{{What: `HKLM\` + facts.KeyTcpip6 + ` DisabledComponents`, From: dwordText(f.System.IPv6Disabled), To: "0x20（IPv4 优先）"}}}
}

func planFirewall(f *facts.Facts) *model.FixAction {
	return &model.FixAction{ID: checks.FixFirewall, Title: "添加 Anthropic 网段直连阻断规则", NeedsAdmin: true,
		Note:    "只拦截不经代理、直接连 160.79.104.0/23 的出站连接；经本机代理的流量不受影响。",
		Steps:   []model.Step{step("firewall_add", "name", facts.FirewallRuleID)},
		Changes: []model.Change{{What: "Windows 防火墙出站规则", From: map[bool]string{true: "存在但未生效", false: "无"}[f.Firewall.Exists], To: "阻断 160.79.104.0/23"}}}
}

// Describe 用于日志：一行说明一个动作。
func Describe(a model.FixAction) string {
	s := a.Title
	if a.NeedsAdmin {
		s += "（需要管理员）"
	}
	return fmt.Sprintf("%s：%d 处改动", s, len(a.Changes))
}
