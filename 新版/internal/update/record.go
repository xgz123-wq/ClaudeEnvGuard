package update

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 脚本写的记录文件。字段名和 更新三端Claude.ps1 / 一键更新ClaudeDesktop.ps1 保持一致，只读不写。

// scriptRecord 是 检查记录.json 和 更新记录-<时间>\记录.json 的内容。
type scriptRecord struct {
	At               string           `json:"At"`
	Action           string           `json:"Action"`
	Status           string           `json:"Status"`
	RecordPath       string           `json:"RecordPath"`
	Products         []scriptProduct  `json:"Products"`
	CliInstall       *cliInstall      `json:"CliInstall"`
	Desktop          *desktopPackage  `json:"Desktop"`
	Targets          []scriptTarget   `json:"Targets"`
	Maintenance      *maintenance     `json:"Maintenance"`
	After            *afterVersions   `json:"After"`
	Timing           orderedSeconds   `json:"Timing"`
	Error            string           `json:"Error"`
	Command          string           `json:"Command"`
	RunningProcesses []runningProcess `json:"RunningProcesses"`
}

type scriptProduct struct {
	Product   string `json:"Product"`
	Installed string `json:"Installed"`
	Latest    string `json:"Latest"`
	Status    string `json:"Status"` // update_available / up_to_date
}

type cliInstall struct {
	From string `json:"From"`
	To   string `json:"To"`
}

type desktopPackage struct {
	PackagePath     string  `json:"PackagePath"`
	NewVersion      string  `json:"NewVersion"`
	Reused          bool    `json:"Reused"`
	DownloadSeconds float64 `json:"DownloadSeconds"`
}

type scriptTarget struct {
	Name    string `json:"Name"`
	Path    string `json:"Path"`
	State   string `json:"State"` // unchanged / changed_signed / changed_unsigned / moved / missing
	NewPath string `json:"NewPath,omitempty"`
	SHA256  string `json:"SHA256,omitempty"`
	Error   string `json:"Error,omitempty"` // 只有 Go 查版本会写：文件在但读不了时的原因
}

type maintenance struct {
	Status       string         `json:"Status"`
	HashCount    int            `json:"HashCount"`
	FiltersCount int            `json:"FiltersCount"`
	StepSeconds  orderedSeconds `json:"StepSeconds"`
	Checks       map[string]int `json:"Checks"`
}

type afterVersions struct {
	Desktop         string `json:"Desktop"`
	Cli             string `json:"Cli"`
	Plugin          string `json:"Plugin"`
	PluginProtected bool   `json:"PluginProtected"`
}

type runningProcess struct {
	ProcessID      int    `json:"ProcessId"`
	ExecutablePath string `json:"ExecutablePath"`
}

// maintResult 是 统一维护\结果.json：UAC 里那个进程的结果。授权被取消等原因只记在这里。
type maintResult struct {
	Status       string         `json:"Status"`
	Error        string         `json:"Error"`
	StepSeconds  orderedSeconds `json:"StepSeconds"`
	SummaryScope string         `json:"SummaryScope"`
}

// orderedSeconds 是保留键顺序的「名字 → 秒数」，脚本按执行顺序写入。
type orderedSeconds struct {
	Keys   []string
	Values map[string]float64
}

func (o *orderedSeconds) UnmarshalJSON(b []byte) error {
	o.Keys, o.Values = nil, map[string]float64{}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil { // {
		return err
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		k, _ := t.(string)
		var v float64
		if err := dec.Decode(&v); err != nil {
			return err
		}
		o.Keys = append(o.Keys, k)
		o.Values[k] = v
	}
	return nil
}

// MarshalJSON 按原来的键顺序写回成普通对象，和脚本写的 Timing 一样（Go 查版本写 检查记录.json 时用）。
func (o orderedSeconds) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(o.Values[k])
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Has 判断某个计时点是否已经写下。
func (o orderedSeconds) Has(k string) bool { _, ok := o.Values[k]; return ok }

// Max 是最后一个计时点，即到目前为止的总秒数。
func (o orderedSeconds) Max() float64 {
	m := 0.0
	for _, v := range o.Values {
		if v > m {
			m = v
		}
	}
	return m
}

// readJSON 读脚本写的 json；兼容带 BOM 的文件。脚本可能正在写，读到半截时返回错误，由调用方下一秒再读。
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	return json.Unmarshal(b, v)
}

// readLines 读 进度.log，只返回以换行结尾的完整行（最后半行可能还没写完）。
func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[:i]
	} else {
		return nil
	}
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// runDirPrefix 是脚本 Update 记录目录的前缀；卫士指定的目录用同一个前缀，桌面图标和卫士的记录排在一起。
const runDirPrefix = "更新记录-"

// stampLayout 是记录目录名里的时间格式。
const stampLayout = "20060102-150405"

// runDirs 按时间从新到旧列出 更新记录-* 目录。
func runDirs(root string) []string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), runDirPrefix) {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	sort.Slice(out, func(i, j int) bool { return filepath.Base(out[i]) > filepath.Base(out[j]) })
	return out
}

// stampOf 从目录名解析时间；解析不了返回零值。
func stampOf(dir string) time.Time {
	s := strings.TrimPrefix(filepath.Base(dir), runDirPrefix)
	if len(s) > len(stampLayout) {
		s = s[:len(stampLayout)]
	}
	t, err := time.ParseInLocation(stampLayout, s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}
