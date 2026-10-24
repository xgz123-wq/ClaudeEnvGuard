// Package cleanup 清理 Claude 登录数据。流程固定为：Plan 列清单 → 用户确认 → Execute。
// 只删白名单里的东西；目标程序在运行时拒绝执行；登录凭据不留备份（留了就等于没清）。
package cleanup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/jsonx"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

// 清理类型。
const (
	KindCode    = "code"
	KindDesktop = "desktop"
	KindBrowser = "browser"
)

// DesktopAllowlist 是 Desktop 数据目录里允许删除的登录、会话、缓存项。MCP 配置等一律不碰。
var DesktopAllowlist = []string{"Cookies", "Cookies-journal", "Network", "Local Storage", "Session Storage",
	"IndexedDB", "Partitions", "Cache", "Code Cache", "GPUCache", "blob_storage", "Local State"}

// BrowserAllowlist 是专用浏览器 Default 配置里允许删除的登录、站点数据。
var BrowserAllowlist = []string{"Network", "Cookies", "Cookies-journal", "Local Storage", "Session Storage", "IndexedDB",
	"Service Worker", "Login Data", "Login Data-journal", "Login Data For Account", "Login Data For Account-journal",
	"Web Data", "Web Data-journal", "Shared Storage", "WebStorage", "File System"}

// codeAuthKeys 是 ~/.claude.json 里的登录字段，删掉它们，其余设置保留。
var codeAuthKeys = []string{"oauthAccount", "primaryApiKey"}

func size(p string) (int64, int) {
	var total int64
	n := 0
	_ = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
			n++
		}
		return nil
	})
	return total, n
}

func item(p, note string) (model.CleanItem, bool) {
	st, err := os.Lstat(p)
	if err != nil {
		return model.CleanItem{}, false
	}
	it := model.CleanItem{Path: p, IsDir: st.IsDir(), Action: "delete", Note: note}
	if st.IsDir() {
		it.Bytes, it.Files = size(p)
	} else {
		it.Bytes, it.Files = st.Size(), 1
	}
	return it, true
}

func total(items []model.CleanItem) float64 {
	var t int64
	for _, i := range items {
		t += i.Bytes
	}
	return float64(t) / 1024 / 1024
}

// Plan 生成清理清单。browser 只在 kind=browser 时使用。
func Plan(sys platform.OS, kind string, f *facts.Facts) model.CleanPlan {
	p := sys.Paths()
	switch kind {
	case KindCode:
		cp := model.CleanPlan{Kind: kind, Title: "清理 Claude Code 登录",
			Warning: "删除后需要重新登录 Claude Code；凭据不留备份，删了就找不回。设置、对话记录、MCP 配置都保留。"}
		dir := filepath.Join(p.Home, ".claude")
		if it, ok := item(filepath.Join(dir, ".credentials.json"), "登录凭据"); ok {
			cp.Items = append(cp.Items, it)
		}
		if matches, _ := filepath.Glob(filepath.Join(dir, ".credentials.json.bak*")); len(matches) > 0 {
			sort.Strings(matches)
			for _, m := range matches {
				if it, ok := item(m, "旧凭据备份"); ok {
					cp.Items = append(cp.Items, it)
				}
			}
		}
		cj := filepath.Join(p.Home, ".claude.json")
		if b, err := os.ReadFile(cj); err == nil {
			if obj, err := jsonx.ParseObject(b); err == nil {
				var keys []string
				for _, k := range codeAuthKeys {
					if _, ok := obj.Get(k); ok {
						keys = append(keys, k)
					}
				}
				if len(keys) > 0 {
					st, _ := os.Stat(cj)
					cp.Items = append(cp.Items, model.CleanItem{Path: cj, Bytes: st.Size(), Files: 1, Action: "edit",
						Note: "只删其中的登录字段 " + strings.Join(keys, "、") + "，其他设置原样保留"})
				}
			}
		}
		if running := runningCLI(f); running > 0 {
			cp.Blocked = fmt.Sprintf("有 %d 个 Claude Code 进程在运行，请先全部退出（包括 VS Code 里的 Claude Code）。", running)
		}
		cp.TotalMB = total(cp.Items)
		return cp
	case KindDesktop:
		cp := model.CleanPlan{Kind: kind, Title: "清理 Claude Desktop 登录",
			Warning: "删除后 Desktop 需要重新登录；登录数据不留备份。MCP 配置（claude_desktop_config.json）和扩展保留。"}
		for _, root := range []string{filepath.Join(p.AppData, "Claude"), filepath.Join(p.LocalAppData, "Claude-3p")} {
			for _, name := range DesktopAllowlist {
				if it, ok := item(filepath.Join(root, name), ""); ok {
					cp.Items = append(cp.Items, it)
				}
			}
		}
		if f != nil && f.Desktop.Running {
			cp.Blocked = "Claude Desktop 正在运行，请先完全退出（包括右下角托盘图标）。"
		}
		cp.TotalMB = total(cp.Items)
		return cp
	case KindBrowser:
		cp := model.CleanPlan{Kind: kind}
		if f == nil || f.Selected == nil {
			cp.Blocked = "没有选中的专用浏览器。"
			return cp
		}
		b := f.Selected
		cp.Title = "清理专用浏览器「" + b.Name + "」的登录"
		cp.Warning = "这个专用浏览器里的所有网站都会退出登录，保存的密码也会删除，不留备份。书签、扩展、浏览器设置保留。"
		profiles, _ := filepath.Glob(filepath.Join(b.UserDataDir, "Profile *"))
		profiles = append([]string{filepath.Join(b.UserDataDir, "Default")}, profiles...)
		for _, prof := range profiles {
			for _, name := range BrowserAllowlist {
				if it, ok := item(filepath.Join(prof, name), ""); ok {
					cp.Items = append(cp.Items, it)
				}
			}
		}
		if len(b.Running) > 0 {
			cp.Blocked = "这个专用浏览器正在运行，请先关掉它的全部窗口。"
		}
		cp.TotalMB = total(cp.Items)
		return cp
	}
	return model.CleanPlan{Kind: kind, Blocked: "未知的清理类型：" + kind}
}

func runningCLI(f *facts.Facts) int {
	if f == nil {
		return 0
	}
	return f.CLIRunning
}

// allowed 再核一遍：执行时只接受计划里且在白名单范围内的路径，防止清单被篡改或路径拼错删到别处。
func allowed(sys platform.OS, kind string, f *facts.Facts, path string) bool {
	p := sys.Paths()
	clean := strings.ToLower(filepath.Clean(path))
	under := func(root string, names []string) bool {
		for _, n := range names {
			if clean == strings.ToLower(filepath.Join(root, n)) {
				return true
			}
		}
		return false
	}
	switch kind {
	case KindCode:
		dir := filepath.Join(p.Home, ".claude")
		base := filepath.Base(path)
		if strings.EqualFold(filepath.Dir(path), dir) && (base == ".credentials.json" || strings.HasPrefix(base, ".credentials.json.bak")) {
			return true
		}
		return clean == strings.ToLower(filepath.Join(p.Home, ".claude.json"))
	case KindDesktop:
		return under(filepath.Join(p.AppData, "Claude"), DesktopAllowlist) || under(filepath.Join(p.LocalAppData, "Claude-3p"), DesktopAllowlist)
	case KindBrowser:
		if f == nil || f.Selected == nil {
			return false
		}
		dir := filepath.Dir(path)
		if !strings.EqualFold(filepath.Dir(dir), filepath.Clean(f.Selected.UserDataDir)) {
			return false
		}
		prof := filepath.Base(dir)
		if prof != "Default" && !strings.HasPrefix(prof, "Profile ") {
			return false
		}
		return under(dir, BrowserAllowlist)
	}
	return false
}

// Execute 按计划执行清理。调用前必须已经把 Plan 展示给用户并得到确认。
func Execute(sys platform.OS, plan model.CleanPlan, f *facts.Facts) model.CleanResult {
	res := model.CleanResult{}
	if plan.Blocked != "" {
		res.Message = "没有执行：" + plan.Blocked
		return res
	}
	// 执行前重新确认进程状态，防止确认期间用户又打开了程序
	fresh := Plan(sys, plan.Kind, f)
	if fresh.Blocked != "" {
		res.Message = "没有执行：" + fresh.Blocked
		return res
	}
	lg := Log{Kind: plan.Kind, Title: plan.Title, At: time.Now().Format("2006-01-02 15:04:05")}
	for _, it := range plan.Items {
		if !allowed(sys, plan.Kind, f, it.Path) {
			res.Errors = append(res.Errors, "不在允许范围内，跳过："+it.Path)
			continue
		}
		if it.Action == "edit" {
			dir, fields, err := editClaudeJSON(sys, it.Path)
			if err != nil {
				res.Errors = append(res.Errors, it.Path+"："+err.Error())
				continue
			}
			res.BackupDir = dir
			res.Removed = append(res.Removed, it.Path+"（已删登录字段）")
			lg.Edited = append(lg.Edited, Edit{Path: it.Path, Fields: fields})
			continue
		}
		if err := os.RemoveAll(it.Path); err != nil {
			res.Errors = append(res.Errors, it.Path+"："+friendly(err))
			continue
		}
		res.Removed = append(res.Removed, it.Path)
		lg.Deleted = append(lg.Deleted, Deleted{Path: it.Path, IsDir: it.IsDir, Bytes: it.Bytes, Files: it.Files})
	}
	res.OK = len(res.Errors) == 0
	if res.OK {
		res.Message = fmt.Sprintf("已清理 %d 项。", len(res.Removed))
	} else {
		res.Message = fmt.Sprintf("清理了 %d 项，%d 项失败。", len(res.Removed), len(res.Errors))
	}
	lg.Errors, lg.Message = res.Errors, res.Message
	if len(lg.Deleted)+len(lg.Edited)+len(lg.Errors) > 0 {
		if p, err := writeLog(sys.Paths().LocalAppData, lg); err == nil {
			res.LogPath = p
		} else {
			res.Message += "（留痕没写成：" + err.Error() + "）"
		}
	}
	return res
}

// ---------------------------------------------------------------- 留痕

// Log 是一次清理的留痕，写在 %LOCALAPPDATA%\ClaudeEnvGuard\记录\清理-<时间>.json。
// 只记删了哪些路径、改了哪些字段名，不含任何凭据内容：删了的东西本来就找不回，留痕是为了知道动过什么。
type Log struct {
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	At      string    `json:"at"`
	Deleted []Deleted `json:"deleted"`
	Edited  []Edit    `json:"edited"`
	Errors  []string  `json:"errors"`
	Message string    `json:"message"`
}

// Deleted 是删掉的一个文件或目录。
type Deleted struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
}

// Edit 是只删了部分字段的文件（~/.claude.json）。
type Edit struct {
	Path   string   `json:"path"`
	Fields []string `json:"fields"`
}

// LogDir 是记录目录。
func LogDir(localAppData string) string {
	return filepath.Join(appconfig.Dir(localAppData), "记录")
}

func writeLog(localAppData string, lg Log) (string, error) {
	base := filepath.Join(LogDir(localAppData), "清理-"+time.Now().Format("20060102-150405"))
	p := base + ".json"
	for i := 1; i < 50; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			break
		}
		p = fmt.Sprintf("%s-%d.json", base, i)
	}
	b, _ := json.MarshalIndent(lg, "", "  ")
	return p, appconfig.WriteFileAtomic(p, b)
}

// ListLogs 读所有清理留痕，新的在前；读不了的文件跳过。
func ListLogs(localAppData string) []LogFile {
	dir := LogDir(localAppData)
	matches, _ := filepath.Glob(filepath.Join(dir, "清理-*.json"))
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	var out []LogFile
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var lg Log
		if jsonx.Unmarshal(b, &lg) != nil {
			continue
		}
		out = append(out, LogFile{Path: m, Log: lg})
	}
	return out
}

// LogFile 是一份留痕和它的路径。
type LogFile struct {
	Path string
	Log
}

func friendly(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "没有权限或文件被占用（程序可能还在运行）"
	}
	s := err.Error()
	if strings.Contains(s, "being used by another process") || strings.Contains(s, "另一个程序正在使用") {
		return "文件被占用（程序可能还在运行）"
	}
	return s
}

// editClaudeJSON 只删登录字段，其余内容（键顺序、数字精度）原样保留；先写临时文件再替换，避免写坏。返回删掉的字段名。
func editClaudeJSON(sys platform.OS, path string) (string, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	obj, err := jsonx.ParseObject(b)
	if err != nil {
		return "", nil, fmt.Errorf("格式有误，没有修改：%w", err)
	}
	var fields []string
	for _, k := range codeAuthKeys {
		if _, ok := obj.Get(k); ok {
			fields = append(fields, k)
		}
		obj.Delete(k)
	}
	return "", fields, appconfig.WriteFileAtomic(path, obj.Marshal())
}
