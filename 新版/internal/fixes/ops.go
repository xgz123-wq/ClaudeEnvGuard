package fixes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/jsonx"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

// Record 是一步改动的备份记录，还原时按它恢复。
type Record struct {
	ActionID string            `json:"actionId"`
	Op       string            `json:"op"`
	Params   map[string]string `json:"params"`
	Before   map[string]string `json:"before"`
	Applied  bool              `json:"applied"`
	Message  string            `json:"message,omitempty"`
	Restored bool              `json:"restored,omitempty"`
}

// Executor 执行原子操作并记录备份。
type Executor struct {
	Sys platform.OS
	Dir string // 本次备份目录
	seq int
}

func (x *Executor) backupFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	x.seq++
	rel := filepath.Join("files", fmt.Sprintf("%03d-%s", x.seq, filepath.Base(path)))
	if err := os.MkdirAll(filepath.Join(x.Dir, "files"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(x.Dir, rel), b, 0o600); err != nil {
		return "", err
	}
	return rel, nil
}

func (x *Executor) restoreFile(path, rel string, existed bool) error {
	if !existed {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := os.ReadFile(filepath.Join(x.Dir, rel))
	if err != nil {
		return fmt.Errorf("备份文件丢失：%w", err)
	}
	return appconfig.WriteFileAtomic(path, b)
}

func systemExe(sys platform.OS, name string) string {
	root := sys.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	if name == "powershell.exe" {
		return filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", name)
	}
	return filepath.Join(root, "System32", name)
}

func (x *Executor) run(timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, code, err := x.Sys.Run(ctx, systemExe(x.Sys, name), args...)
	if err != nil {
		return err
	}
	if code != 0 {
		out = strings.TrimSpace(out)
		if len(out) > 300 {
			out = out[:300]
		}
		return fmt.Errorf("%s 退出码 %d：%s", name, code, out)
	}
	return nil
}

func ps(script string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script}
}

var (
	reLocale = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z0-9]{2,8}){0,2}$`)
	reCC     = regexp.MustCompile(`^[A-Z]{2}$`)
	reTZID   = regexp.MustCompile(`^[A-Za-z0-9 ().+\-]{2,64}$`)
	reGeoID  = regexp.MustCompile(`^\d{1,6}$`)
)

func regRoot(s string) platform.RegRoot {
	if strings.EqualFold(s, "HKLM") {
		return platform.HKLM
	}
	return platform.HKCU
}

func encodeReg(v platform.RegValue) map[string]string {
	if !v.Exists {
		return map[string]string{"existed": "false"}
	}
	m := map[string]string{"existed": "true", "type": v.Type}
	switch v.Type {
	case "dword":
		m["value"] = strconv.FormatUint(uint64(v.DWord), 10)
	case "multi_sz":
		b, _ := json.Marshal(v.Multi)
		m["value"] = string(b)
	default:
		m["value"] = v.Str
	}
	return m
}

func decodeReg(typ, value string) (platform.RegValue, error) {
	v := platform.RegValue{Exists: true, Type: typ}
	switch typ {
	case "dword":
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return v, fmt.Errorf("不是数字：%s", value)
		}
		v.DWord = uint32(n)
	case "multi_sz":
		if err := json.Unmarshal([]byte(value), &v.Multi); err != nil {
			return v, err
		}
	case "", "sz":
		v.Type, v.Str = "sz", value
	default:
		v.Str = value
	}
	return v, nil
}

func isEnvKey(root platform.RegRoot, key string) bool {
	return (root == platform.HKCU && strings.EqualFold(key, facts.KeyUserEnv)) || (root == platform.HKLM && strings.EqualFold(key, facts.KeyMachineEnv))
}

// Apply 执行一步并返回备份记录。出错时 Applied=false，现场未被改动（或已由本步自行恢复）。
func (x *Executor) Apply(actionID string, st model.Step) Record {
	r := Record{ActionID: actionID, Op: st.Op, Params: st.Params, Before: map[string]string{}}
	p := st.Params
	fail := func(err error) Record {
		r.Message = err.Error()
		return r
	}
	switch st.Op {
	case "mkdir":
		path := p["path"]
		r.Before["created"] = strconv.FormatBool(!facts.Exists(path))
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fail(err)
		}
	case "shortcut_write":
		path := p["path"]
		if facts.Exists(path) {
			rel, err := x.backupFile(path)
			if err != nil {
				return fail(fmt.Errorf("备份原快捷方式失败：%w", err))
			}
			r.Before["existed"], r.Before["backup"] = "true", rel
		} else {
			r.Before["existed"] = "false"
		}
		if err := x.Sys.WriteShortcut(platform.Shortcut{Path: path, Target: p["target"], Args: p["args"], WorkDir: p["workdir"],
			Icon: p["icon"], Description: p["desc"]}); err != nil {
			return fail(fmt.Errorf("写快捷方式失败：%w", err))
		}
	case "json_env":
		path := p["path"]
		var obj *jsonx.Object
		if facts.Exists(path) {
			b, err := os.ReadFile(path)
			if err != nil {
				return fail(err)
			}
			if obj, err = jsonx.ParseObject(b); err != nil {
				return fail(fmt.Errorf("%s 格式有误，为免覆盖你的配置没有修改：%v", filepath.Base(path), err))
			}
			rel, err := x.backupFile(path)
			if err != nil {
				return fail(fmt.Errorf("备份失败：%w", err))
			}
			r.Before["existed"], r.Before["backup"] = "true", rel
		} else {
			obj = jsonx.NewObject()
			r.Before["existed"] = "false"
		}
		env, err := obj.ChildObject("env", true)
		if err != nil {
			return fail(fmt.Errorf("%s 里的 env 不是对象，没有修改", filepath.Base(path)))
		}
		var set map[string]string
		if p["set"] != "" && p["set"] != "null" {
			if err := json.Unmarshal([]byte(p["set"]), &set); err != nil {
				return fail(err)
			}
		}
		for _, k := range sortedKeys(set) {
			env.Set(k, set[k])
		}
		for _, k := range strings.Split(p["delete"], ",") {
			if k = strings.TrimSpace(k); k != "" {
				env.Delete(k)
			}
		}
		if err := appconfig.WriteFileAtomic(path, obj.Marshal()); err != nil {
			return fail(err)
		}
	case "reg_set":
		root := regRoot(p["root"])
		old, err := x.Sys.RegRead(root, p["key"], p["name"])
		if err != nil {
			return fail(err)
		}
		r.Before = encodeReg(old)
		v, err := decodeReg(p["type"], p["value"])
		if err != nil {
			return fail(err)
		}
		if err := x.Sys.RegWrite(root, p["key"], p["name"], v); err != nil {
			return fail(err)
		}
		if isEnvKey(root, p["key"]) {
			x.Sys.BroadcastEnvChange()
		}
	case "reg_delete":
		root := regRoot(p["root"])
		old, err := x.Sys.RegRead(root, p["key"], p["name"])
		if err != nil {
			return fail(err)
		}
		r.Before = encodeReg(old)
		if old.Exists {
			if err := x.Sys.RegDelete(root, p["key"], p["name"]); err != nil {
				return fail(err)
			}
			if isEnvKey(root, p["key"]) {
				x.Sys.BroadcastEnvChange()
			}
		} else {
			r.Message = "本来就没有，跳过"
		}
	case "tz_set":
		if !reTZID.MatchString(p["id"]) {
			return fail(errors.New("时区名不合法"))
		}
		old, _ := x.Sys.RegRead(platform.HKLM, facts.KeyTZ, "TimeZoneKeyName")
		r.Before["old"] = strings.TrimSpace(old.Text())
		if err := x.run(20*time.Second, "tzutil.exe", "/s", p["id"]); err != nil {
			return fail(err)
		}
	case "region_set":
		if !reLocale.MatchString(p["locale"]) || !reCC.MatchString(p["cc"]) {
			return fail(errors.New("区域参数不合法"))
		}
		loc, _ := x.Sys.RegRead(platform.HKCU, facts.KeyIntl, "LocaleName")
		nation, _ := x.Sys.RegRead(platform.HKCU, facts.KeyIntlGeo, "Nation")
		r.Before["locale"], r.Before["nation"] = loc.Text(), nation.Text()
		script := fmt.Sprintf("Set-Culture -CultureInfo '%s'; Set-WinHomeLocation -GeoId ([System.Globalization.RegionInfo]::new('%s')).GeoId", p["locale"], p["cc"])
		if err := x.run(40*time.Second, "powershell.exe", ps(script)...); err != nil {
			return fail(err)
		}
	case "firewall_add":
		if p["name"] != facts.FirewallRuleID {
			return fail(errors.New("规则名不合法"))
		}
		old, _ := x.Sys.RegRead(platform.HKLM, facts.KeyFirewall, facts.FirewallRuleID)
		r.Before["existed"] = strconv.FormatBool(old.Exists)
		script := "Remove-NetFirewallRule -Name '" + facts.FirewallRuleID + "' -ErrorAction SilentlyContinue; " +
			"New-NetFirewallRule -Name '" + facts.FirewallRuleID + "' -DisplayName 'Claude/Anthropic 直连阻断 160.79.104.0/23' " +
			"-Description 'Block direct outbound connections to Anthropic API IPv4 range; local proxy remains available.' " +
			"-Direction Outbound -Action Block -RemoteAddress 160.79.104.0/23 -Profile Any -Enabled True | Out-Null"
		if err := x.run(60*time.Second, "powershell.exe", ps(script)...); err != nil {
			return fail(err)
		}
	default:
		return fail(fmt.Errorf("未知操作 %s", st.Op))
	}
	r.Applied = true
	return r
}

// Restore 按记录恢复一步。
func (x *Executor) Restore(r *Record) error {
	if !r.Applied || r.Restored {
		return nil
	}
	p, b := r.Params, r.Before
	var err error
	switch r.Op {
	case "mkdir":
		if b["created"] == "true" {
			if e := os.Remove(p["path"]); e != nil && !errors.Is(e, os.ErrNotExist) {
				r.Message = "目录里已有数据，保留没删：" + p["path"]
			} else {
				_ = os.Remove(filepath.Dir(p["path"])) // 别名目录空了也一并删除
			}
		}
	case "shortcut_write", "json_env":
		err = x.restoreFile(p["path"], b["backup"], b["existed"] == "true")
	case "reg_set", "reg_delete":
		root := regRoot(p["root"])
		if b["existed"] == "true" {
			v, e := decodeReg(b["type"], b["value"])
			if e != nil {
				return e
			}
			err = x.Sys.RegWrite(root, p["key"], p["name"], v)
		} else if r.Op == "reg_set" {
			err = x.Sys.RegDelete(root, p["key"], p["name"])
		}
		if err == nil && isEnvKey(root, p["key"]) {
			x.Sys.BroadcastEnvChange()
		}
	case "tz_set":
		if !reTZID.MatchString(b["old"]) {
			return errors.New("没有记录到原来的时区")
		}
		err = x.run(20*time.Second, "tzutil.exe", "/s", b["old"])
	case "region_set":
		var parts []string
		if reLocale.MatchString(b["locale"]) {
			parts = append(parts, "Set-Culture -CultureInfo '"+b["locale"]+"'")
		}
		if reGeoID.MatchString(b["nation"]) {
			parts = append(parts, "Set-WinHomeLocation -GeoId "+b["nation"])
		}
		if len(parts) == 0 {
			return errors.New("没有记录到原来的区域设置")
		}
		err = x.run(40*time.Second, "powershell.exe", ps(strings.Join(parts, "; "))...)
	case "firewall_add":
		if b["existed"] == "true" {
			r.Message = "原来就有同名规则，已替换为等价规则，保留不删"
		} else {
			err = x.run(60*time.Second, "powershell.exe", ps("Remove-NetFirewallRule -Name '"+facts.FirewallRuleID+"' -ErrorAction SilentlyContinue")...)
		}
	}
	if err != nil {
		return err
	}
	r.Restored = true
	return nil
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	for i := 1; i < len(ks); i++ {
		for j := i; j > 0 && ks[j] < ks[j-1]; j-- {
			ks[j], ks[j-1] = ks[j-1], ks[j]
		}
	}
	return ks
}
