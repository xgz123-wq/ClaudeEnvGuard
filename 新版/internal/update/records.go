package update

import (
	"fmt"
	"path/filepath"

	"ClaudeEnvGuard/internal/model"
)

// Records 把 更新记录-*\记录.json 转成「记录」页的条目，新的在前。
// 只收真正动过东西或失败的：「已是最新」「Claude 在运行，停下」这类没改任何东西，放进记录页只是噪音。
func Records(root string) []model.LogRecord {
	var out []model.LogRecord
	for _, d := range runDirs(root) {
		var rec scriptRecord
		if readJSON(filepath.Join(d, "记录.json"), &rec) != nil {
			continue
		}
		if lr, ok := logRecord(d, &rec); ok {
			out = append(out, lr)
		}
	}
	return out
}

func logRecord(dir string, r *scriptRecord) (model.LogRecord, bool) {
	h := summarize(dir, r)
	diffs := productDiffs(r)
	if r.Maintenance != nil && r.Maintenance.Status == "passed" {
		diffs = append(diffs, model.LogDiff{What: "直连防护", From: "旧版本路径", To: fmt.Sprintf("已刷新（%d 个指纹，%d 条规则）", r.Maintenance.HashCount, r.Maintenance.FiltersCount)})
	}
	lr := model.LogRecord{ID: "update:" + filepath.Base(dir), Kind: "update", Title: h.What, Dir: dir, Diffs: diffs}
	if t := stampOf(dir); !t.IsZero() {
		lr.At = t.Format("2006-01-02 15:04:05") // records.List 排序后截到分钟
	}
	lr.Meta = fmt.Sprintf("用时 %.1f 秒 · 结果 %s", h.Seconds, r.Status)
	switch {
	case r.Status == "failed":
		lr.State = "failed"
		if len(lr.Diffs) == 0 {
			lr.Diffs = []model.LogDiff{{What: "出错步骤", From: "", To: firstClause(r.Error)}}
		}
	case len(diffs) > 0:
		lr.State = "done"
	default:
		return lr, false
	}
	return lr, true
}

// productDiffs 是装之前 → 装之后的版本。装之后的版本以 After 为准（脚本核验过），没有时用 CliInstall / Desktop 包的版本。
func productDiffs(r *scriptRecord) []model.LogDiff {
	var out []model.LogDiff
	after := map[string]string{}
	if r.After != nil {
		after["Claude Desktop"], after["Claude Code CLI"], after["VS Code Claude Code 插件"] = r.After.Desktop, r.After.Cli, r.After.Plugin
	}
	if r.CliInstall != nil && after["Claude Code CLI"] == "" {
		after["Claude Code CLI"] = r.CliInstall.To
	}
	if r.Desktop != nil && after["Claude Desktop"] == "" {
		after["Claude Desktop"] = r.Desktop.NewVersion
	}
	for _, p := range r.Products {
		from, to := p.Installed, after[p.Product]
		// CLI 装完后脚本会重读本机版本，Products 里已经是新版本；装之前的版本只在 CliInstall.From
		if p.Product == "Claude Code CLI" && r.CliInstall != nil && r.CliInstall.From != "" {
			from = r.CliInstall.From
		}
		if to == "" || to == from {
			continue
		}
		out = append(out, model.LogDiff{What: p.Product, From: from, To: to})
	}
	return out
}
