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
	// requestRestore 是按项还原时交给提权进程的动作 id 列表；没有这个文件表示整次还原。
	requestRestore = "request-restore.json"
)

// Manifest 是一次修复的备份清单。
type Manifest struct {
	ID         string               `json:"id"`
	CreatedAt  string               `json:"createdAt"`
	Actions    []string             `json:"actions"`
	Items      []model.BackupItem   `json:"items,omitempty"` // v2.2 起：每个动作的标题和改动，按项还原用；更早的备份没有
	Records    []Record             `json:"records"`
	RestoredAt string               `json:"restoredAt,omitempty"`
	Restores   []model.RestoreEvent `json:"restores,omitempty"`
}

const timeLayout = "2006-01-02 15:04:05"

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
	var items []model.BackupItem
	for _, a := range plan.Actions {
		if !want[a.ID] {
			continue
		}
		titles = append(titles, a.Title)
		items = append(items, model.BackupItem{ID: a.ID, Title: a.Title, Changes: a.Changes, NeedsAdmin: a.NeedsAdmin})
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
	m := Manifest{ID: id, CreatedAt: time.Now().Format(timeLayout), Actions: titles, Items: items, Records: records}
	if err := writeJSON(filepath.Join(dir, manifestUser), m); err != nil {
		res.Outcomes = append(res.Outcomes, model.StepOutcome{Message: "写备份清单失败：" + err.Error()})
	}
	if len(admin) > 0 {
		res.Outcomes = append(res.Outcomes, runElevated(sys, dir, admin, selfExe, "--elevated-apply")...)
	}
	// 管理员部分跑完再记每项成没成：失败的动作已撤回，记录页不给它勾选框
	okByID := map[string]bool{}
	for _, o := range res.Outcomes {
		if o.ActionID != "" {
			okByID[o.ActionID] = o.OK
		}
	}
	for i := range m.Items {
		m.Items[i].OK = okByID[m.Items[i].ID]
	}
	_ = writeJSON(filepath.Join(dir, manifestUser), m)
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

// ListBackups 列出所有修复备份，新的在前。每次修复带上逐项的改动和还原状态，供「记录」页按项还原。
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
		var am Manifest
		_ = readJSON(filepath.Join(dir, manifestAdmin), &am)
		info := model.BackupInfo{ID: e.Name(), Dir: dir, CreatedAt: m.CreatedAt, Actions: m.Actions, Restored: m.RestoredAt != "",
			RestoredAt: m.RestoredAt, Items: itemsOf(m, am), Restores: m.Restores}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// itemsOf 返回一次修复的逐项清单。v2.2 之前的备份没有 Items，按备份记录现场拼出来。
func itemsOf(m, am Manifest) []model.BackupItem {
	all := append(append([]Record{}, m.Records...), am.Records...)
	if len(m.Items) > 0 {
		items := append([]model.BackupItem{}, m.Items...)
		for i := range items {
			if items[i].RestoredAt == "" && items[i].OK && !pendingFor(all, items[i].ID) && hasApplied(all, items[i].ID) {
				items[i].RestoredAt = firstNonEmpty(m.RestoredAt, "已还原")
			}
		}
		return items
	}
	var ids []string
	byID := map[string][]Record{}
	for _, r := range all {
		if _, ok := byID[r.ActionID]; !ok {
			ids = append(ids, r.ActionID)
		}
		byID[r.ActionID] = append(byID[r.ActionID], r)
	}
	var items []model.BackupItem
	for _, id := range ids {
		recs := byID[id]
		it := model.BackupItem{ID: id, Title: id, OK: hasApplied(recs, id)}
		if len(ids) == 1 && len(m.Actions) == 1 {
			it.Title = m.Actions[0]
		}
		for _, r := range recs {
			if r.Applied {
				it.Changes = append(it.Changes, describeRecord(r)...)
			}
		}
		if it.OK && !pendingFor(recs, id) {
			it.RestoredAt = firstNonEmpty(lastRestoredAt(recs), m.RestoredAt, "已还原")
		}
		items = append(items, it)
	}
	return items
}

func hasApplied(recs []Record, id string) bool {
	for _, r := range recs {
		if r.ActionID == id && r.Applied {
			return true
		}
	}
	return false
}

func pendingFor(recs []Record, id string) bool {
	for _, r := range recs {
		if r.ActionID == id && r.Applied && !r.Restored {
			return true
		}
	}
	return false
}

func lastRestoredAt(recs []Record) string {
	last := ""
	for _, r := range recs {
		if r.RestoredAt > last {
			last = r.RestoredAt
		}
	}
	return last
}

// describeRecord 把旧备份里的一步改动写成给人看的一行。不显示注册表和配置文件里的原值：可能是令牌。
func describeRecord(r Record) []model.Change {
	p, b := r.Params, r.Before
	switch r.Op {
	case "region_set":
		return []model.Change{{What: "区域格式", From: b["locale"], To: p["locale"]}, {What: "国家或地区", From: geoName(b["nation"]), To: p["cc"]}}
	case "tz_set":
		return []model.Change{{What: "系统时区", From: b["old"], To: p["id"]}}
	case "reg_set":
		return []model.Change{{What: p["name"], From: existedText(b["existed"]), To: "已设置"}}
	case "reg_delete":
		return []model.Change{{What: p["name"], From: existedText(b["existed"]), To: "已删除"}}
	case "json_env":
		return []model.Change{{What: filepath.Base(p["path"]) + " 的 env", From: "原文件已备份", To: "已修改"}}
	case "shortcut_write":
		return []model.Change{{What: filepath.Base(p["path"]), From: "原快捷方式已备份", To: "已改写"}}
	case "mkdir":
		return []model.Change{{What: p["path"], From: "没有", To: "已新建"}}
	case "firewall_add":
		return []model.Change{{What: "防火墙规则 " + p["name"], From: existedText(b["existed"]), To: "已添加"}}
	}
	return []model.Change{{What: r.Op, From: "", To: "已修改"}}
}

func existedText(s string) string {
	if s == "false" {
		return "没有"
	}
	return "原值已备份"
}

// geoName 把 Windows 的 GeoID 换成国家代码，认不出的原样显示。
func geoName(id string) string {
	switch id {
	case "45":
		return "CN"
	case "244":
		return "US"
	case "122":
		return "JP"
	case "242":
		return "GB"
	case "215":
		return "SG"
	case "94":
		return "DE"
	case "":
		return ""
	}
	return "GeoID " + id
}

// restoreRecords 倒序还原记录里 pick 选中的改动；成功的记下还原时间。
func restoreRecords(x *Executor, recs []Record, pick func(Record) bool) []model.StepOutcome {
	var outs []model.StepOutcome
	now := time.Now().Format(timeLayout)
	for i := len(recs) - 1; i >= 0; i-- {
		r := &recs[i]
		if !r.Applied || r.Restored || (pick != nil && !pick(*r)) {
			continue
		}
		what := r.Op + " " + firstNonEmpty(r.Params["path"], r.Params["name"], r.Params["id"], r.Params["locale"])
		if err := x.Restore(r); err != nil {
			outs = append(outs, model.StepOutcome{ActionID: r.ActionID, Op: r.Op, OK: false, Message: "还原失败：" + what + "：" + err.Error()})
			continue
		}
		r.RestoredAt = now
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

// pickIDs 返回只选中这些动作的过滤器；ids 为空表示全部。
func pickIDs(ids []string) func(Record) bool {
	if len(ids) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	return func(r Record) bool { return want[r.ActionID] }
}

// Restore 整次还原某次修复。
func Restore(sys platform.OS, id, selfExe string) model.ApplyResult {
	return RestoreItems(sys, id, nil, selfExe)
}

// RestoreItems 还原某次修复里的指定动作（items 为空 = 整次）：先还原管理员部分（必要时提权），再倒序还原普通部分。
// 没选中的动作保持现状；每条改动记下还原时间，全部还原后整次标记已还原。
func RestoreItems(sys platform.OS, id string, items []string, selfExe string) model.ApplyResult {
	if strings.ContainsAny(id, `\/:`) || id == "" || id == "." || id == ".." {
		return model.ApplyResult{Message: "备份编号不合法"}
	}
	dir := filepath.Join(BackupRoot(sys.Paths().LocalAppData), id)
	var m Manifest
	if err := readJSON(filepath.Join(dir, manifestUser), &m); err != nil {
		return model.ApplyResult{Message: "找不到这次修复的备份：" + err.Error()}
	}
	pick := pickIDs(items)
	res := model.ApplyResult{BackupID: id}
	var am Manifest
	hasAdmin := readJSON(filepath.Join(dir, manifestAdmin), &am) == nil
	if len(items) > 0 {
		known := map[string]bool{}
		for _, r := range append(append([]Record{}, m.Records...), am.Records...) {
			known[r.ActionID] = true
		}
		for _, it := range items {
			if !known[it] {
				return model.ApplyResult{BackupID: id, Message: "这次修复里没有「" + it + "」这一项"}
			}
		}
	}
	if hasAdmin && pendingPicked(am.Records, pick) {
		if sys.IsAdmin() {
			res.Outcomes = append(res.Outcomes, restoreAdminIn(sys, dir, items)...)
		} else {
			if err := writeJSON(filepath.Join(dir, requestRestore), items); err != nil {
				res.Outcomes = append(res.Outcomes, model.StepOutcome{Message: "写还原请求失败：" + err.Error()})
			} else {
				res.Outcomes = append(res.Outcomes, runElevated(sys, dir, []model.FixAction{{ID: "admin", Title: "管理员部分"}}, selfExe, "--elevated-restore")...)
			}
			_ = os.Remove(filepath.Join(dir, requestRestore))
		}
		_ = readJSON(filepath.Join(dir, manifestAdmin), &am) // 提权进程改过管理员清单，重读才知道还剩什么
	}
	x := &Executor{Sys: sys, Dir: dir}
	res.Outcomes = append(res.Outcomes, restoreRecords(x, m.Records, pick)...)
	res.OK = true
	done := map[string]bool{}
	var doneIDs []string
	for _, o := range res.Outcomes {
		if !o.OK {
			res.OK = false
		} else if o.ActionID != "" && o.ActionID != "admin" && !done[o.ActionID] {
			done[o.ActionID] = true
			doneIDs = append(doneIDs, o.ActionID)
		}
	}
	now := time.Now().Format(timeLayout)
	all := append(append([]Record{}, m.Records...), am.Records...)
	for i := range m.Items {
		if m.Items[i].RestoredAt == "" && hasApplied(all, m.Items[i].ID) && !pendingFor(all, m.Items[i].ID) {
			m.Items[i].RestoredAt = now
		}
	}
	full := !pendingRecords(all)
	switch {
	case len(res.Outcomes) == 0:
		res.Message = "这次修复没有需要还原的改动。"
	case !res.OK:
		res.Message = "部分项目没能还原，详见每一行的原因。"
	case full:
		res.Message = "已全部还原。"
	default:
		res.Message = fmt.Sprintf("已还原 %d 项，其余保持现状。", len(doneIDs))
	}
	if full && m.RestoredAt == "" && res.OK {
		m.RestoredAt = now
	}
	if len(res.Outcomes) > 0 {
		m.Restores = append(m.Restores, model.RestoreEvent{At: now, Items: doneIDs, OK: res.OK, Message: res.Message})
	}
	_ = writeJSON(filepath.Join(dir, manifestUser), m)
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

func pendingPicked(recs []Record, pick func(Record) bool) bool {
	for _, r := range recs {
		if r.Applied && !r.Restored && (pick == nil || pick(r)) {
			return true
		}
	}
	return false
}

// ElevatedRestore 是提权还原进程的入口；按项还原时动作 id 在 request-restore.json 里。
func ElevatedRestore(sys platform.OS, dir string) int {
	var items []string
	_ = readJSON(filepath.Join(dir, requestRestore), &items)
	outs := restoreAdminIn(sys, dir, items)
	_ = writeJSON(filepath.Join(dir, resultAdmin), outs)
	return 0
}

// ElevatedRestoreIn 在当前（管理员）进程里整次还原管理员部分。
func ElevatedRestoreIn(sys platform.OS, dir string) []model.StepOutcome {
	return restoreAdminIn(sys, dir, nil)
}

func restoreAdminIn(sys platform.OS, dir string, items []string) []model.StepOutcome {
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
	outs := restoreRecords(x, am.Records, pickIDs(items))
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
