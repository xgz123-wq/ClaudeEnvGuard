package fixes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"ClaudeEnvGuard/internal/appconfig"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/jsonx"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

// 备份目录里的文件名。
const (
	manifestUser  = "manifest.json"
	manifestAdmin = "manifest-admin.json"
	requestAdmin  = "request-admin.json"
	resultAdmin   = "result-admin.json"
)

// Manifest 是一次修复的备份清单。
type Manifest struct {
	ID         string   `json:"id"`
	CreatedAt  string   `json:"createdAt"`
	Actions    []string `json:"actions"`
	Records    []Record `json:"records"`
	RestoredAt string   `json:"restoredAt,omitempty"`
}

// BackupRoot 是所有修复备份的根目录。
func BackupRoot(localAppData string) string {
	return filepath.Join(appconfig.Dir(localAppData), "备份")
}

func writeJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	return appconfig.WriteFileAtomic(path, b)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return jsonx.Unmarshal(b, v)
}

// newBackupDir 建一个按时间命名的备份目录；同一秒内重复时加序号。
func newBackupDir(root string) (string, string, error) {
	base := time.Now().Format("20060102-150405")
	for i := 0; i < 50; i++ {
		id := base
		if i > 0 {
			id = fmt.Sprintf("%s-%d", base, i)
		}
		dir := filepath.Join(root, id)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return id, dir, os.MkdirAll(dir, 0o755)
		}
	}
	return "", "", errors.New("无法创建备份目录")
}

// runActions 逐个动作执行；某一步失败时撤回该动作已做的步骤，保证每个动作要么全做要么全不做。
func runActions(x *Executor, actions []model.FixAction) ([]Record, []model.StepOutcome) {
	var records []Record
	var outs []model.StepOutcome
	for _, a := range actions {
		var done []Record
		failed := ""
		for _, st := range a.Steps {
			r := x.Apply(a.ID, st)
			if !r.Applied {
				failed = r.Message
				break
			}
			done = append(done, r)
		}
		if failed != "" {
			for i := len(done) - 1; i >= 0; i-- {
				_ = x.Restore(&done[i])
			}
			outs = append(outs, model.StepOutcome{ActionID: a.ID, OK: false, Message: a.Title + "：失败，已撤回本项改动。原因：" + failed})
			records = append(records, done...)
			continue
		}
		records = append(records, done...)
		msg := a.Title + "：完成"
		if a.Note != "" {
			msg += "。" + a.Note
		}
		outs = append(outs, model.StepOutcome{ActionID: a.ID, OK: true, Message: msg})
	}
	return records, outs
}

// Apply 执行选中的修复动作。需要管理员的动作在非管理员进程里会通过 selfExe 提权执行（弹一次 UAC）。
func Apply(sys platform.OS, plan model.FixPlan, ids []string, selfExe string) model.ApplyResult {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var user, admin []model.FixAction
	var titles []string
	for _, a := range plan.Actions {
		if !want[a.ID] {
			continue
		}
		titles = append(titles, a.Title)
		if a.NeedsAdmin && !sys.IsAdmin() {
			admin = append(admin, a)
		} else {
			user = append(user, a)
		}
	}
	if len(titles) == 0 {
		return model.ApplyResult{OK: true, Message: "没有选中任何修复项。"}
	}
	id, dir, err := newBackupDir(BackupRoot(sys.Paths().LocalAppData))
	if err != nil {
		return model.ApplyResult{Message: "创建备份目录失败：" + err.Error()}
	}
	res := model.ApplyResult{BackupID: id}
	x := &Executor{Sys: sys, Dir: dir}
	records, outs := runActions(x, user)
	res.Outcomes = append(res.Outcomes, outs...)
	m := Manifest{ID: id, CreatedAt: time.Now().Format("2006-01-02 15:04:05"), Actions: titles, Records: records}
	if err := writeJSON(filepath.Join(dir, manifestUser), m); err != nil {
		res.Outcomes = append(res.Outcomes, model.StepOutcome{Message: "写备份清单失败：" + err.Error()})
	}
	if len(admin) > 0 {
		res.Outcomes = append(res.Outcomes, runElevated(sys, dir, admin, selfExe, "--elevated-apply")...)
	}
	res.OK = true
	ok := 0
	for _, o := range res.Outcomes {
		if !o.OK {
			res.OK = false
		} else {
			ok++
		}
	}
	res.Message = fmt.Sprintf("完成 %d 项，失败 %d 项。备份在 %s，可随时还原。", ok, len(res.Outcomes)-ok, dir)
	return res
}

func runElevated(sys platform.OS, dir string, admin []model.FixAction, selfExe, flag string) []model.StepOutcome {
	cancelled := func(msg string) []model.StepOutcome {
		var outs []model.StepOutcome
		for _, a := range admin {
			outs = append(outs, model.StepOutcome{ActionID: a.ID, OK: false, Message: a.Title + "：" + msg})
		}
		return outs
	}
	if flag == "--elevated-apply" {
		if err := writeJSON(filepath.Join(dir, requestAdmin), admin); err != nil {
			return cancelled("写提权请求失败：" + err.Error())
		}
	}
	_ = os.Remove(filepath.Join(dir, resultAdmin))
	code, err := sys.RunElevated(selfExe, []string{flag, dir})
	if err != nil {
		return cancelled(err.Error())
	}
	var outs []model.StepOutcome
	if rerr := readJSON(filepath.Join(dir, resultAdmin), &outs); rerr != nil {
		return cancelled(fmt.Sprintf("管理员进程没有返回结果（退出码 %d）", code))
	}
	return outs
}

// ElevatedApply 是提权进程的入口：读请求、校验、执行、写结果。返回进程退出码。
func ElevatedApply(sys platform.OS, dir string) int {
	var actions []model.FixAction
	if err := readJSON(filepath.Join(dir, requestAdmin), &actions); err != nil {
		_ = writeJSON(filepath.Join(dir, resultAdmin), []model.StepOutcome{{Message: "读不到提权请求：" + err.Error()}})
		return 2
	}
	var allowed []model.FixAction
	var outs []model.StepOutcome
	for _, a := range actions {
		if err := validateAction(a); err != nil {
			outs = append(outs, model.StepOutcome{ActionID: a.ID, Message: a.Title + "：拒绝执行（" + err.Error() + "）"})
			continue
		}
		allowed = append(allowed, a)
	}
	x := &Executor{Sys: sys, Dir: dir, seq: 500}
	records, o2 := runActions(x, allowed)
	outs = append(outs, o2...)
	_ = writeJSON(filepath.Join(dir, manifestAdmin), Manifest{ID: filepath.Base(dir), CreatedAt: time.Now().Format("2006-01-02 15:04:05"), Records: records})
	_ = writeJSON(filepath.Join(dir, resultAdmin), outs)
	return 0
}

// ---------------------------------------------------------------- 提权白名单

var reLocalProxy = regexp.MustCompile(`^http://127\.0\.0\.1:\d{1,5}$`)

// validateStep 限定提权进程只做本工具会生成的那几类改动，防止请求文件被同用户的其他程序篡改后借 UAC 提权。
func validateStep(st model.Step) error {
	p := st.Params
	switch st.Op {
	case "reg_set", "reg_delete":
		root, key, name := strings.ToUpper(p["root"]), p["key"], p["name"]
		ok := false
		switch {
		case strings.EqualFold(key, facts.KeyPolicy) && (name == "egressProxyUrl" || name == "disableAutoUpdates"):
			ok = root == "HKLM" || root == "HKCU"
			if st.Op == "reg_set" && name == "egressProxyUrl" && !reLocalProxy.MatchString(p["value"]) {
				return errors.New("代理地址必须是本机端口")
			}
		case root == "HKLM" && strings.EqualFold(key, facts.KeyTZAuto) && name == "Start":
			ok = st.Op == "reg_delete" || p["value"] == "3" || p["value"] == "4"
		case root == "HKLM" && strings.EqualFold(key, facts.KeyTcpip6) && name == "DisabledComponents":
			ok = true
		case root == "HKLM" && strings.EqualFold(key, facts.KeyMachineEnv):
			for _, k := range append(append([]string{}, facts.ProxyEnvKeys...), facts.ThirdPartyKeys...) {
				ok = ok || strings.EqualFold(name, k)
			}
		}
		if !ok {
			return fmt.Errorf("不允许改 %s\\%s\\%s", root, key, name)
		}
	case "tz_set":
		if !reTZID.MatchString(p["id"]) {
			return errors.New("时区名不合法")
		}
	case "firewall_add":
		if p["name"] != facts.FirewallRuleID {
			return errors.New("规则名不合法")
		}
	default:
		return fmt.Errorf("操作 %s 不需要也不允许提权执行", st.Op)
	}
	return nil
}

func validateAction(a model.FixAction) error {
	for _, st := range a.Steps {
		if err := validateStep(st); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- 还原

// ListBackups 列出所有修复备份，新的在前。
func ListBackups(localAppData string) []model.BackupInfo {
	root := BackupRoot(localAppData)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []model.BackupInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		var m Manifest
		if readJSON(filepath.Join(dir, manifestUser), &m) != nil {
			continue
		}
		info := model.BackupInfo{ID: e.Name(), Dir: dir, CreatedAt: m.CreatedAt, Actions: m.Actions, Restored: m.RestoredAt != ""}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func restoreRecords(x *Executor, recs []Record) []model.StepOutcome {
	var outs []model.StepOutcome
	for i := len(recs) - 1; i >= 0; i-- {
		r := &recs[i]
		if !r.Applied || r.Restored {
			continue
		}
		what := r.Op + " " + firstNonEmpty(r.Params["path"], r.Params["name"], r.Params["id"], r.Params["locale"])
		if err := x.Restore(r); err != nil {
			outs = append(outs, model.StepOutcome{ActionID: r.ActionID, Op: r.Op, OK: false, Message: "还原失败：" + what + "：" + err.Error()})
			continue
		}
		msg := "已还原：" + what
		if r.Message != "" && !strings.HasPrefix(r.Message, "本来就没有") {
			msg += "（" + r.Message + "）"
		}
		outs = append(outs, model.StepOutcome{ActionID: r.ActionID, Op: r.Op, OK: true, Message: msg})
	}
	return outs
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// Restore 还原某次修复：先还原管理员部分（必要时提权），再倒序还原普通部分。
func Restore(sys platform.OS, id, selfExe string) model.ApplyResult {
	if strings.ContainsAny(id, `\/:`) || id == "" || id == "." || id == ".." {
		return model.ApplyResult{Message: "备份编号不合法"}
	}
	dir := filepath.Join(BackupRoot(sys.Paths().LocalAppData), id)
	var m Manifest
	if err := readJSON(filepath.Join(dir, manifestUser), &m); err != nil {
		return model.ApplyResult{Message: "找不到这次修复的备份：" + err.Error()}
	}
	res := model.ApplyResult{BackupID: id}
	var am Manifest
	if readJSON(filepath.Join(dir, manifestAdmin), &am) == nil && pendingRecords(am.Records) {
		if sys.IsAdmin() {
			res.Outcomes = append(res.Outcomes, ElevatedRestoreIn(sys, dir)...)
		} else {
			res.Outcomes = append(res.Outcomes, runElevated(sys, dir, []model.FixAction{{ID: "admin", Title: "管理员部分"}}, selfExe, "--elevated-restore")...)
		}
	}
	x := &Executor{Sys: sys, Dir: dir}
	res.Outcomes = append(res.Outcomes, restoreRecords(x, m.Records)...)
	res.OK = true
	for _, o := range res.Outcomes {
		if !o.OK {
			res.OK = false
		}
	}
	if res.OK {
		m.RestoredAt = time.Now().Format("2006-01-02 15:04:05")
		res.Message = "已全部还原。"
	} else {
		res.Message = "部分项目没能还原，详见每一行的原因。"
	}
	_ = writeJSON(filepath.Join(dir, manifestUser), m)
	if len(res.Outcomes) == 0 {
		res.Message = "这次修复没有需要还原的改动。"
	}
	return res
}

func pendingRecords(recs []Record) bool {
	for _, r := range recs {
		if r.Applied && !r.Restored {
			return true
		}
	}
	return false
}

// ElevatedRestore 是提权还原进程的入口。
func ElevatedRestore(sys platform.OS, dir string) int {
	outs := ElevatedRestoreIn(sys, dir)
	_ = writeJSON(filepath.Join(dir, resultAdmin), outs)
	return 0
}

// ElevatedRestoreIn 在当前（管理员）进程里还原管理员部分。
func ElevatedRestoreIn(sys platform.OS, dir string) []model.StepOutcome {
	var am Manifest
	if err := readJSON(filepath.Join(dir, manifestAdmin), &am); err != nil {
		return []model.StepOutcome{{Message: "读不到管理员部分的备份：" + err.Error()}}
	}
	for _, r := range am.Records {
		// 还原写回的是记录里的旧值：同样只允许白名单内的位置
		if err := validateStep(model.Step{Op: restoreOp(r), Params: restoreParams(r)}); err != nil {
			return []model.StepOutcome{{Message: "备份记录不可信，拒绝还原：" + err.Error()}}
		}
	}
	x := &Executor{Sys: sys, Dir: dir}
	outs := restoreRecords(x, am.Records)
	_ = writeJSON(filepath.Join(dir, manifestAdmin), am)
	return outs
}

func restoreOp(r Record) string {
	if r.Op == "tz_set" || r.Op == "firewall_add" {
		return r.Op
	}
	return "reg_delete"
}

func restoreParams(r Record) map[string]string {
	p := map[string]string{}
	for k, v := range r.Params {
		p[k] = v
	}
	if r.Op == "tz_set" {
		p["id"] = r.Before["old"]
	}
	return p
}

// HandleElevatedArgs 处理提权子进程的内部参数；handled=false 表示不是内部调用。
func HandleElevatedArgs(sys platform.OS, args []string) (code int, handled bool) {
	if len(args) != 2 {
		return 0, false
	}
	if args[0] == "--elevated-apply" || args[0] == "--elevated-restore" {
		// 只接受本工具备份目录下的请求：…\ClaudeEnvGuard\备份\<编号>
		d := filepath.Clean(args[1])
		if filepath.Base(filepath.Dir(d)) != "备份" || filepath.Base(filepath.Dir(filepath.Dir(d))) != "ClaudeEnvGuard" {
			return 4, true
		}
	}
	switch args[0] {
	case "--elevated-apply":
		return ElevatedApply(sys, args[1]), true
	case "--elevated-restore":
		return ElevatedRestore(sys, args[1]), true
	}
	return 0, false
}
