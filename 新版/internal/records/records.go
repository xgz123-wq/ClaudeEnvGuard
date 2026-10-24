// Package records 汇总「记录」页的条目：修复、还原（备份清单）、更新（三端更新脚本的记录）、清理（留痕文件）。
// 只读，不改任何文件；还原本身由 fixes.RestoreItems 做。
package records

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"ClaudeEnvGuard/internal/cleanup"
	"ClaudeEnvGuard/internal/fixes"
	"ClaudeEnvGuard/internal/model"
	"ClaudeEnvGuard/internal/update"
)

// List 返回全部记录，新的在前。updateRoot 是 %LOCALAPPDATA%\ClaudeNetworkGuard。
func List(localAppData, updateRoot string) []model.LogRecord {
	out := []model.LogRecord{}
	for _, b := range fixes.ListBackups(localAppData) {
		out = append(out, fixRecords(b)...)
	}
	out = append(out, update.Records(updateRoot)...)
	for _, lf := range cleanup.ListLogs(localAppData) {
		out = append(out, cleanRecord(lf))
	}
	// 按到秒的时间排，再截到分钟显示：同一分钟里先修复后还原，还原要排在上面
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	for i := range out {
		out[i].At = minute(out[i].At)
	}
	return out
}

// minute 把 2006-01-02 15:04:05 截到分钟，各类记录的时间格式统一后才能排序。
func minute(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func fixTitle(b model.BackupInfo) string {
	if len(b.Actions) == 1 {
		return b.Actions[0]
	}
	var titles []string
	for _, it := range b.Items {
		titles = append(titles, it.Title)
	}
	if len(titles) == 0 {
		titles = b.Actions
	}
	if len(titles) > 2 {
		return fmt.Sprintf("%s 等 %d 项", strings.Join(titles[:2], "、"), len(titles))
	}
	return strings.Join(titles, "、")
}

// fixRecords 是一次修复的记录，外加对它做过的每次还原各一条。
func fixRecords(b model.BackupInfo) []model.LogRecord {
	title := fixTitle(b)
	fix := model.LogRecord{ID: "fix:" + b.ID, Kind: "fix", At: b.CreatedAt, Title: title, Dir: b.Dir, BackupID: b.ID,
		Meta: "改之前已备份，可以按项还原"}
	ok, restored := 0, 0
	byID := map[string]model.BackupItem{}
	for _, it := range b.Items {
		byID[it.ID] = it
		if it.OK {
			ok++
			if it.RestoredAt != "" {
				restored++
			}
		}
		for _, d := range itemDiffs(it) {
			fix.Diffs = append(fix.Diffs, d)
		}
	}
	switch {
	case ok == 0:
		fix.State, fix.Meta = "failed", "这次修复没有改成，已自动撤回"
	case restored == ok:
		fix.State = "restored"
	case restored > 0:
		fix.State = "partial"
	default:
		fix.State = "restorable"
	}
	out := []model.LogRecord{fix}

	events := b.Restores
	// v2.2 之前整次还原不记还原事件，只有 restoredAt：补一条，记录页才看得到那次还原
	if len(events) == 0 && b.RestoredAt != "" {
		var ids []string
		for _, it := range b.Items {
			if it.OK {
				ids = append(ids, it.ID)
			}
		}
		events = []model.RestoreEvent{{At: b.RestoredAt, Items: ids, OK: true}}
	}
	for i, ev := range events {
		r := model.LogRecord{ID: fmt.Sprintf("restore:%s:%d", b.ID, i), Kind: "restore", At: ev.At, Dir: b.Dir, State: "restored",
			Meta: "按 " + b.ID + " 里的备份还原，只动选中的项"}
		for _, id := range ev.Items {
			for _, d := range itemDiffs(byID[id]) {
				d.From, d.To = d.To, d.From
				d.Restorable, d.RestoredAt = false, ""
				r.Diffs = append(r.Diffs, d)
			}
		}
		r.Title = "还原「" + title + "」"
		if len(ev.Items) < ok {
			r.Title += fmt.Sprintf("中的 %d 项", len(ev.Items))
		}
		if !ev.OK {
			r.State, r.Meta = "failed", firstNonEmpty(ev.Message, "部分项目没能还原")
		}
		out = append(out, r)
	}
	return out
}

func itemDiffs(it model.BackupItem) []model.LogDiff {
	if it.ID == "" {
		return nil
	}
	restorable := it.OK && it.RestoredAt == ""
	if !it.OK {
		return []model.LogDiff{{ItemID: it.ID, Item: it.Title, What: it.Title, From: "", To: "没改成（已自动撤回）"}}
	}
	if len(it.Changes) == 0 {
		return []model.LogDiff{{ItemID: it.ID, Item: it.Title, What: it.Title, From: "", To: "已修改", Restorable: restorable, RestoredAt: it.RestoredAt}}
	}
	var out []model.LogDiff
	for _, c := range it.Changes {
		out = append(out, model.LogDiff{ItemID: it.ID, Item: it.Title, What: c.What, From: c.From, To: c.To, Restorable: restorable, RestoredAt: it.RestoredAt})
	}
	return out
}

func cleanRecord(lf cleanup.LogFile) model.LogRecord {
	r := model.LogRecord{ID: "clean:" + filepath.Base(lf.Path), Kind: "clean", At: lf.At, Title: lf.Title, State: "irreversible",
		Dir: filepath.Dir(lf.Path), Meta: lf.Message}
	if r.Title == "" {
		r.Title = "清理登录"
	}
	for _, d := range lf.Deleted {
		r.Diffs = append(r.Diffs, model.LogDiff{What: "删除", From: d.Path, To: "已删除"})
	}
	for _, e := range lf.Edited {
		r.Diffs = append(r.Diffs, model.LogDiff{What: "只删登录字段", From: e.Path, To: "删了 " + strings.Join(e.Fields, "、") + "，其他设置保留"})
	}
	for _, e := range lf.Errors {
		r.Diffs = append(r.Diffs, model.LogDiff{What: "没删掉", From: "", To: e})
	}
	return r
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
