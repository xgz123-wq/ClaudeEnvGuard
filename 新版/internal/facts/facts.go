// Package facts 收集现场事实。分两阶段：
//   - 本地阶段：读注册表、文件、快捷方式、端口，1 秒内完成；
//   - 联网阶段：经三个端口测出口、可达性、DNS 解析器，并做较慢的本地校验（文件哈希）。
//
// 每个收集器独立超时、独立捕获 panic，失败只记录到对应字段的 Err，不影响其他收集器。
package facts

import (
	"path/filepath"
	"strings"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/chrome"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

// Facts 是一次检查看到的全部现场信息。判定逻辑只读它，不再碰系统。
type Facts struct {
	Settings appconfig.Settings
	Paths    platform.Paths
	IsAdmin  bool
	Now      time.Time
	Offline  bool

	Proxy      ProxyFacts
	Browsers   []Browser
	Selected   *Browser
	ChromeExe  string
	CLI        CLIFacts
	CLIRunning int // 正在运行的 Claude Code 进程数（不含 Desktop）
	Desktop    DesktopFacts
	System     SystemFacts
	Guard      GuardFacts
	Firewall   FirewallFacts

	// 联网阶段
	NetworkDone bool
	Exits       map[model.Entry]*model.Exit
	SystemDNS   *model.DNSLeak
	IPv6        IPv6Facts

	// ManifestFound 表示读到了部署清单（%LOCALAPPDATA%\ClaudeNetworkGuard\部署清单.json）。
	ManifestFound bool

	Errors  map[string]string          // 收集器名 → 异常
	Timings map[string]CollectorTiming // 收集器名 → 本地阶段耗时
}

// PortOwner 是某个端口的监听者。
type PortOwner struct {
	Port      int    `json:"port"`
	Listening bool   `json:"listening"`
	PID       int    `json:"pid,omitempty"`
	Name      string `json:"name,omitempty"`
	Err       string `json:"err,omitempty"`
}

// ProxyFacts 是代理客户端的状态。
type ProxyFacts struct {
	Owners      map[int]PortOwner
	VergeFound  bool
	VergeErr    string
	TunMode     *bool
	SystemProxy *bool
	MixedPort   int
	Listeners   map[string]bool // clash-verge.yaml 里的专用监听器是否存在；nil 表示没读到
}

// ShortcutInfo 是一个指向专用浏览器的快捷方式。
type ShortcutInfo struct {
	platform.Shortcut
	Flags chrome.Flags
	Err   string
}

// Browser 是一个专用浏览器（一个独立 User Data 目录）。
type Browser struct {
	Name        string
	UserDataDir string
	Standard    bool // 位于 %LOCALAPPDATA%\ClaudeAccountBrowsers 下
	DirExists   bool
	Shortcuts   []ShortcutInfo
	LastUsed    time.Time
	Running     []platform.Process // 使用此目录的浏览器主进程
	PrefLang    string             // Default\Preferences 里的 intl.accept_languages
	DuplicateOf []string           // 和哪些其他名称共用同一目录
	Registered  bool               // 部署清单里登记过
	// MissingShortcut 是部署清单登记、但现在已经不在的快捷方式路径（被删或改名）。
	MissingShortcut string
}

func (b *Browser) hasShortcut(path string) bool {
	for _, sc := range b.Shortcuts {
		if strings.EqualFold(filepath.Clean(sc.Path), filepath.Clean(path)) {
			return true
		}
	}
	return false
}

// CLIFacts 是 Claude Code 的现场。
type CLIFacts struct {
	Installed     bool
	Exe           string
	Version       string
	Settings      SettingsFile
	LocalSettings SettingsFile
	UserEnv       map[string]string
	MachineEnv    map[string]string
	EnvErr        string
}

// SettingsFile 是 ~/.claude/settings*.json 的 env 字段。
type SettingsFile struct {
	Path         string
	Exists       bool
	Err          string
	Env          map[string]string // 只保留与检查相关的键；其余键只记名字不记值
	OtherEnvKeys []string
}

// PolicyLayer 是一层 Desktop 策略（HKLM / HKCU）。
type PolicyLayer struct {
	Name      string
	Egress    string
	HasEgress bool
	DisableAU *uint32
	Err       string
}

// Present 表示这一层有任何策略值。
func (p PolicyLayer) Present() bool { return p.HasEgress || p.DisableAU != nil }

// DesktopFacts 是 Claude Desktop 的现场。
type DesktopFacts struct {
	Installed   bool
	FullName    string
	Version     string
	InstallPath string
	Exe         string
	Err         string
	HKLM        PolicyLayer
	HKLMWow     PolicyLayer
	HKCU        PolicyLayer
	LocalFound  bool
	LocalEgress string
	LocalErr    string
	Running     bool
}

// SystemFacts 是 Windows 系统设置。
type SystemFacts struct {
	WindowsTZ    string
	TZErr        string
	AutoTZ       *bool
	Locale       string
	GeoName      string
	UILang       string
	ProxyEnable  bool
	ProxyServer  string
	IPv6Disabled *uint32
}

// GuardFacts 是分享包「直连防护」的证据文件状态。
type GuardFacts struct {
	ShareRoot       string
	Found           bool // 找到状态文件
	Status          string
	ManifestOK      bool
	ManifestVersion string
	VerifyStatus    string
	VerifyAt        string
	Targets         []GuardTarget
	HashChecked     bool
	Err             string
}

// GuardTarget 是被防护的一个程序。
type GuardTarget struct {
	Name     string
	Path     string
	Expected string
	Changed  bool
	Missing  bool
	// AcceptedBySignature：哈希对不上，但这是会自更新的 CLI 目标且 Anthropic 签名有效，按签名放行（Changed=false）。
	AcceptedBySignature bool
	Signer              string // 签名证书主题，只在核对过签名时有值
}

// FirewallRule 是一个网段的直连阻断规则现状。
type FirewallRule struct {
	Exists bool
	Name   string // 实际找到的规则名：可能是名字不同但内容等价的规则
	Active bool
	Block  bool
	Out    bool
	Range  bool
}

// OK 表示这条规则真的在拦这个网段。
func (r FirewallRule) OK() bool { return r.Exists && r.Active && r.Block && r.Out && r.Range }

// FirewallFacts 是 Anthropic 网段直连阻断规则，每个网段一条。
type FirewallFacts struct {
	Read  bool
	Rules map[string]FirewallRule // 键是 FirewallRange.RuleID；没有这个键等于这条规则不存在
	// ProfilesOff 是总开关关着的网络配置文件（域 / 专用 / 公用）。关着的配置文件里规则一条都不生效（2026-11-10 实见）。
	ProfilesOff []string
	Err         string
}

// Broken 按 FirewallRanges 的顺序返回没生效的网段。
func (fw FirewallFacts) Broken() []FirewallRange {
	var out []FirewallRange
	for _, g := range FirewallRanges {
		if !fw.Rules[g.RuleID].OK() {
			out = append(out, g)
		}
	}
	return out
}

// IPv6Facts 是 IPv6 泄露相关事实。
type IPv6Facts struct {
	Global    []string
	Tested    bool
	DirectIP  string
	DirectCC  string
	DirectErr string
}

// SelectedName 返回当前选中浏览器名。
func (f *Facts) SelectedName() string {
	if f.Selected == nil {
		return ""
	}
	return f.Selected.Name
}
