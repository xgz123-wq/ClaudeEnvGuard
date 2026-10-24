// Package model 定义检查结果、修复计划等跨层共享的数据结构。
// 这里只放纯数据，不依赖任何系统调用，GUI 和命令行都直接序列化它们。
package model

import "time"

// Status 是单个检查项的结论。
type Status string

const (
	Pass    Status = "pass"    // 通过
	Warn    Status = "warn"    // 警告：能用但有风险或与目标不一致
	Fail    Status = "fail"    // 失败：会导致泄露或不可用
	Unknown Status = "unknown" // 未知：读不到、超时、出错；绝不当作通过
	NA      Status = "na"      // 不适用：对应软件没装或用户没用这个入口
)

// Rank 用于汇总时取最差状态。
func (s Status) Rank() int {
	switch s {
	case Fail:
		return 4
	case Warn:
		return 3
	case Unknown:
		return 2
	case Pass:
		return 1
	default:
		return 0
	}
}

// Label 是给人看的中文状态名。
func (s Status) Label() string {
	switch s {
	case Pass:
		return "通过"
	case Warn:
		return "警告"
	case Fail:
		return "失败"
	case Unknown:
		return "未知"
	case NA:
		return "不适用"
	}
	return string(s)
}

// Kind 区分检查项的目标值从哪里来。
type Kind string

const (
	KindFixed Kind = "fixed" // 固定：与地区无关，谁都该这样
	KindIP    Kind = "ip"    // 随 IP：目标值由出口 IP 推出
	KindInfo  Kind = "info"  // 信息：只展示，状态只表示有没有读到
)

// Group 是检查项所属页签。
type Group string

const (
	GroupOverview Group = "overview"
	GroupBrowser  Group = "browser"
	GroupCLI      Group = "cli"
	GroupDesktop  Group = "desktop"
	GroupSystem   Group = "system"
)

// Result 是一个检查项的完整结论。
type Result struct {
	ID       string `json:"id"`
	Group    Group  `json:"group"`
	Title    string `json:"title"`
	Kind     Kind   `json:"kind"`
	Advanced bool   `json:"advanced,omitempty"` // 进阶项：没装不算失败
	Status   Status `json:"status"`
	Current  string `json:"current,omitempty"` // 当前值
	Target   string `json:"target,omitempty"`  // 目标值
	Detail   string `json:"detail,omitempty"`  // 为什么是这个结论
	Advice   string `json:"advice,omitempty"`  // 手动怎么修
	FixID    string `json:"fixId,omitempty"`   // 有一键修复时填修复动作 ID
	Pending  bool   `json:"pending,omitempty"` // 联网项还在检测
}

// Entry 是 Claude 的三个使用入口。
type Entry string

const (
	EntryBrowser Entry = "browser"
	EntryCLI     Entry = "cli"
	EntryDesktop Entry = "desktop"
)

// EntryLabel 返回入口的中文名。
func EntryLabel(e Entry) string {
	switch e {
	case EntryBrowser:
		return "专用浏览器"
	case EntryCLI:
		return "Claude Code"
	case EntryDesktop:
		return "Claude Desktop"
	}
	return string(e)
}

// Exit 是某个入口经自己端口测到的出口。
type Exit struct {
	Entry       Entry    `json:"entry"`
	Port        int      `json:"port"`
	Listening   bool     `json:"listening"`
	IP          string   `json:"ip,omitempty"`
	CountryCode string   `json:"countryCode,omitempty"` // 大写 ISO 两位
	Country     string   `json:"country,omitempty"`     // 中文名
	City        string   `json:"city,omitempty"`
	TimeZone    string   `json:"timeZone,omitempty"` // IANA
	ASN         string   `json:"asn,omitempty"`
	Org         string   `json:"org,omitempty"`
	TrustScore  *int     `json:"trustScore,omitempty"` // ip.net.coffee 信任评分
	Residential *bool    `json:"residential,omitempty"`
	RiskFlags   []string `json:"riskFlags,omitempty"` // VPN / 代理 / 机房 等
	GeoSource   string   `json:"geoSource,omitempty"`
	Reach       string   `json:"reach,omitempty"` // ok / challenge / blocked / fail
	ReachDetail string   `json:"reachDetail,omitempty"`
	DNS         *DNSLeak `json:"dns,omitempty"`
	Err         string   `json:"err,omitempty"`
}

// DNSLeak 是经某条路径做的 DNS 解析器探测。
type DNSLeak struct {
	Tested    bool       `json:"tested"`
	Resolvers []Resolver `json:"resolvers,omitempty"`
	Err       string     `json:"err,omitempty"`
}

// Resolver 是一个被看到的 DNS 解析器。
type Resolver struct {
	IP          string `json:"ip"`
	CountryCode string `json:"countryCode,omitempty"`
	ISP         string `json:"isp,omitempty"`
}

// Target 是按出口 IP（或用户手选）推出来的目标环境。
type Target struct {
	Source      string `json:"source"` // auto / manual / none
	SourceText  string `json:"sourceText"`
	CountryCode string `json:"countryCode,omitempty"`
	Country     string `json:"country,omitempty"`
	IANA        string `json:"iana,omitempty"`
	WindowsTZ   string `json:"windowsTz,omitempty"`
	Lang        string `json:"lang,omitempty"`       // 浏览器 --lang
	AcceptLang  string `json:"acceptLang,omitempty"` // 浏览器 --accept-lang
	Locale      string `json:"locale,omitempty"`     // Windows 区域格式
}

// Summary 是一次检查的汇总。
type Summary struct {
	Pass     int    `json:"pass"`
	Warn     int    `json:"warn"`
	Fail     int    `json:"fail"`
	Unknown  int    `json:"unknown"`
	NA       int    `json:"na"`
	Fixable  int    `json:"fixable"`
	Headline string `json:"headline"`
	NextStep string `json:"nextStep,omitempty"`
	Overall  Status `json:"overall"`
}

// Report 是一次完整检查的输出。
type Report struct {
	Version         string        `json:"version"`
	StartedAt       time.Time     `json:"startedAt"`
	DurationMs      int64         `json:"durationMs"`
	Offline         bool          `json:"offline"`
	Final           bool          `json:"final"` // false 表示联网项还在跑
	SelectedBrowser string        `json:"selectedBrowser,omitempty"`
	Browsers        []BrowserInfo `json:"browsers"`
	Exits           []Exit        `json:"exits"`
	Target          Target        `json:"target"`
	Results         []Result      `json:"results"`
	Summary         Summary       `json:"summary"`
	Notes           []string      `json:"notes,omitempty"`
}

// BrowserInfo 是下拉框里的一个专用浏览器。
type BrowserInfo struct {
	Name        string `json:"name"`
	UserDataDir string `json:"userDataDir"`
	Shortcut    string `json:"shortcut,omitempty"`
	Standard    bool   `json:"standard"` // 是否在 ClaudeAccountBrowsers 标准目录下
	LastUsed    string `json:"lastUsed,omitempty"`
}

// Change 是修复计划里给人看的一行改动。
type Change struct {
	What string `json:"what"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Step 是可执行的原子操作，执行前会记录备份。
type Step struct {
	Op     string            `json:"op"`
	Params map[string]string `json:"params"`
}

// FixAction 是一个可勾选的修复动作。
type FixAction struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	CheckIDs   []string `json:"checkIds"`
	Changes    []Change `json:"changes"`
	NeedsAdmin bool     `json:"needsAdmin"`
	DefaultOn  bool     `json:"defaultOn"`
	Note       string   `json:"note,omitempty"` // 例如「需要重启生效」
	Steps      []Step   `json:"steps"`
}

// FixPlan 是一次修复计划。
type FixPlan struct {
	Target  Target      `json:"target"`
	Actions []FixAction `json:"actions"`
	Notes   []string    `json:"notes,omitempty"`
}

// StepOutcome 是单步执行结果。
type StepOutcome struct {
	ActionID string `json:"actionId"`
	Op       string `json:"op"`
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
}

// ApplyResult 是一次修复执行的结果。
type ApplyResult struct {
	BackupID string        `json:"backupId,omitempty"`
	OK       bool          `json:"ok"`
	Outcomes []StepOutcome `json:"outcomes"`
	Message  string        `json:"message"`
}

// BackupInfo 是一次可还原的修复备份。
type BackupInfo struct {
	ID         string         `json:"id"`
	Dir        string         `json:"dir"`
	CreatedAt  string         `json:"createdAt"`
	Actions    []string       `json:"actions"`
	Restored   bool           `json:"restored"`
	RestoredAt string         `json:"restoredAt,omitempty"`
	Items      []BackupItem   `json:"items"`
	Restores   []RestoreEvent `json:"restores,omitempty"`
}

// BackupItem 是一次修复里的一个动作，按项还原的最小单位。
type BackupItem struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Changes    []Change `json:"changes"`
	NeedsAdmin bool     `json:"needsAdmin,omitempty"`
	OK         bool     `json:"ok"`                   // 当时改成功了；失败的动作已自动撤回，不能再还原
	RestoredAt string   `json:"restoredAt,omitempty"` // 非空表示这一项已经还原
}

// RestoreEvent 是对某次修复做过的一次还原（整次或按项）。
type RestoreEvent struct {
	At      string   `json:"at"`
	Items   []string `json:"items"` // 这次还原的动作 id
	OK      bool     `json:"ok"`
	Message string   `json:"message,omitempty"`
}

// CleanItem 是清理清单中的一项。
type CleanItem struct {
	Path   string `json:"path"`
	IsDir  bool   `json:"isDir"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
	Action string `json:"action"` // delete / edit
	Note   string `json:"note,omitempty"`
}

// CleanPlan 是清理前列出的清单。
type CleanPlan struct {
	Kind    string      `json:"kind"`
	Title   string      `json:"title"`
	Items   []CleanItem `json:"items"`
	Blocked string      `json:"blocked,omitempty"` // 非空表示不能执行及原因
	Warning string      `json:"warning,omitempty"`
	TotalMB float64     `json:"totalMb"`
}

// CleanResult 是清理执行结果。
type CleanResult struct {
	OK        bool     `json:"ok"`
	Removed   []string `json:"removed"`
	Errors    []string `json:"errors"`
	BackupDir string   `json:"backupDir,omitempty"`
	Message   string   `json:"message"`
	LogPath   string   `json:"logPath,omitempty"` // 留痕文件：记录\清理-<时间>.json
}

// LogRecord 是「记录」页的一条：修复、还原、更新、清理。
type LogRecord struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // fix / restore / update / clean
	At       string    `json:"at"`   // 2006-01-02 15:04
	Title    string    `json:"title"`
	Meta     string    `json:"meta,omitempty"`
	State    string    `json:"state"` // restorable / partial / restored / done / failed / irreversible
	Diffs    []LogDiff `json:"diffs"`
	Dir      string    `json:"dir,omitempty"`
	BackupID string    `json:"backupId,omitempty"` // 修复记录：按项还原时传回
}

// LogDiff 是记录里「改了什么：改之前 → 改之后」的一行。
type LogDiff struct {
	ItemID     string `json:"itemId,omitempty"` // 修复记录：所属动作 id，按项还原用
	Item       string `json:"item,omitempty"`   // 修复记录：所属动作的标题，一个动作可能改好几处
	What       string `json:"what"`
	From       string `json:"from"`
	To         string `json:"to"`
	Restorable bool   `json:"restorable,omitempty"`
	RestoredAt string `json:"restoredAt,omitempty"`
}
