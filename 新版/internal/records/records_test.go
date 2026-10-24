package records

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ClaudeEnvGuard/internal/cleanup"
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/platform"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(rs []model.LogRecord, kind string) []model.LogRecord {
	var out []model.LogRecord
	for _, r := range rs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// 四类记录汇总：修复按项还原后出现「部分还原」和一条还原记录；更新只收动过东西的；清理不可恢复。
func TestListAllKinds(t *testing.T) {
	fk := platform.NewFake(t.TempDir())
	fk.Env["SystemRoot"] = `C:\Windows`
	fk.Admin = true
	fk.SetReg(platform.HKLM, facts.KeyTZ, "TimeZoneKeyName", platform.RegValue{Type: "sz", Str: "China Standard Time"})
	fk.RunFunc = func(string, ...string) (string, int, error) { return "", 0, nil }
	settings := filepath.Join(fk.P.Home, ".claude", "settings.json")
	plan := model.FixPlan{Actions: []model.FixAction{
		{ID: "cli.proxy", Title: "Claude Code 走专用端口", Changes: []model.Change{{What: "HTTPS_PROXY", From: "未设置", To: "http://127.0.0.1:43865"}},
			Steps: []model.Step{{Op: "json_env", Params: map[string]string{"path": settings, "set": `{"HTTPS_PROXY":"http://127.0.0.1:43865"}`}}}},
		{ID: "sys.timezone", Title: "系统时区跟随出口", Changes: []model.Change{{What: "系统时区", From: "China Standard Time", To: "Eastern Standard Time"}},
			Steps: []model.Step{{Op: "tz_set", Params: map[string]string{"id": "Eastern Standard Time"}}}},
	}}
	res := fixes.Apply(fk, plan, []string{"cli.proxy", "sys.timezone"}, "")
	if !res.OK {
		t.Fatalf("修复失败：%+v", res)
	}
	if rr := fixes.RestoreItems(fk, res.BackupID, []string{"sys.timezone"}, ""); !rr.OK {
		t.Fatalf("按项还原失败：%+v", rr)
	}

	root := filepath.Join(fk.P.LocalAppData, "ClaudeNetworkGuard")
	write(t, filepath.Join(root, "更新记录-20261024-094215", "记录.json"), `{"Status":"passed","Products":[{"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.310"}],
		"CliInstall":{"From":"2.1.309","To":"2.1.310"},"After":{"Cli":"2.1.310"},"Maintenance":{"Status":"passed","HashCount":4,"FiltersCount":8},"Timing":{"查版本":6.8,"完成":99.8}}`)
	write(t, filepath.Join(root, "更新记录-20261024-142740", "记录.json"), `{"Status":"up_to_date","Products":[{"Product":"Claude Code CLI","Installed":"2.1.310","Latest":"2.1.310"}]}`)

	dir := filepath.Join(fk.P.Home, ".claude")
	write(t, filepath.Join(dir, ".credentials.json"), `{"token":"sk-SECRET"}`)
	f := &facts.Facts{}
	if cr := cleanup.Execute(fk, cleanup.Plan(fk, cleanup.KindCode, f), f); !cr.OK {
		t.Fatalf("清理失败：%+v", cr)
	}

	rs := List(fk.P.LocalAppData, root)
	for i := 1; i < len(rs); i++ {
		if rs[i-1].At < rs[i].At {
			t.Errorf("应新的在前：%s 在 %s 前面", rs[i-1].At, rs[i].At)
		}
	}
	fix := find(rs, "fix")
	if len(fix) != 1 || fix[0].State != "partial" || fix[0].BackupID != res.BackupID || len(fix[0].Diffs) != 2 {
		t.Fatalf("修复记录不对：%+v", fix)
	}
	for _, d := range fix[0].Diffs {
		want := d.ItemID == "cli.proxy"
		if d.Restorable != want || (d.RestoredAt != "") == want {
			t.Errorf("只有没还原的项可勾：%+v", d)
		}
	}
	rst := find(rs, "restore")
	if len(rst) != 1 || !strings.Contains(rst[0].Title, "中的 1 项") || len(rst[0].Diffs) != 1 ||
		rst[0].Diffs[0].From != "Eastern Standard Time" || rst[0].Diffs[0].To != "China Standard Time" {
		t.Fatalf("还原记录不对：%+v", rst)
	}
	upd := find(rs, "update")
	if len(upd) != 1 || upd[0].State != "done" || upd[0].Diffs[0].From != "2.1.309" || upd[0].Diffs[0].To != "2.1.310" {
		t.Fatalf("更新记录只收动过东西的，CLI 装之前的版本取 CliInstall.From：%+v", upd)
	}
	cl := find(rs, "clean")
	if len(cl) != 1 || cl[0].State != "irreversible" || len(cl[0].Diffs) != 1 || !strings.HasSuffix(cl[0].Diffs[0].From, ".credentials.json") {
		t.Fatalf("清理记录不对：%+v", cl)
	}
}

// v2.2 之前整次还原过的备份：补一条还原记录，修复记录显示已还原。
func TestOldRestoredBackup(t *testing.T) {
	b := model.BackupInfo{ID: "20261011-125030", CreatedAt: "2026-10-11 12:50:33", Actions: []string{"区域格式与国家改为 en-US / 美国"}, Restored: true,
		RestoredAt: "2026-10-11 12:50:43", Items: []model.BackupItem{{ID: "sys.region", Title: "区域格式与国家改为 en-US / 美国", OK: true, RestoredAt: "2026-10-11 12:50:43",
			Changes: []model.Change{{What: "区域格式", From: "zh-CN", To: "en-US"}}}}}
	rs := fixRecords(b)
	if len(rs) != 2 || rs[0].State != "restored" || rs[1].Kind != "restore" || rs[1].At != "2026-10-11 12:50:43" || strings.Contains(rs[1].Title, "中的") {
		t.Fatalf("旧备份的记录不对：%+v", rs)
	}
}
