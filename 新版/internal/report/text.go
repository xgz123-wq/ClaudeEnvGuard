// Package report 把检查报告、修复计划等渲染成命令行文本。
package report

import (
	"fmt"
	"strings"

	"ClaudeEnvGuard/internal/model"
)

// Style 控制是否输出颜色。
type Style struct{ Color bool }

func (s Style) c(code, text string) string {
	if !s.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) status(st model.Status, pending bool) string {
	if pending {
		return s.c("36", "[检测中]")
	}
	switch st {
	case model.Pass:
		return s.c("32", "[通过]")
	case model.Warn:
		return s.c("33", "[警告]")
	case model.Fail:
		return s.c("31", "[失败]")
	case model.Unknown:
		return s.c("35", "[未知]")
	}
	return s.c("90", "[跳过]")
}

var groupTitle = map[model.Group]string{model.GroupOverview: "总览", model.GroupBrowser: "专用浏览器", model.GroupCLI: "Claude Code（CLI）",
	model.GroupDesktop: "Claude Desktop", model.GroupSystem: "系统"}

var kindLabel = map[model.Kind]string{model.KindFixed: "固定", model.KindIP: "随IP", model.KindInfo: "信息"}

// Report 渲染检查报告。
func Report(r model.Report, st Style, verbose bool) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("%s v%s · %s · 用时 %.1f 秒%s\n", st.c("1", "Claude 环境卫士"), r.Version, r.StartedAt.Format("2006-01-02 15:04"),
		float64(r.DurationMs)/1000, map[bool]string{true: " · 离线模式", false: ""}[r.Offline])
	w("结论：%s\n", st.c("1", r.Summary.Headline))
	if r.Summary.NextStep != "" {
		w("下一步：%s\n", r.Summary.NextStep)
	}
	w("目标：%s", r.Target.SourceText)
	if r.Target.IANA != "" {
		w("  时区 %s  语言 %s", r.Target.IANA, r.Target.Lang)
	}
	w("\n")
	for _, n := range r.Notes {
		w("提示：%s\n", n)
	}
	if r.SelectedBrowser != "" {
		var names []string
		for _, bi := range r.Browsers {
			names = append(names, bi.Name)
		}
		w("专用浏览器：%s（共 %d 个：%s）\n", r.SelectedBrowser, len(names), strings.Join(names, "、"))
	}
	if !r.Offline {
		w("\n%s\n", st.c("1", "出口"))
		for _, ex := range r.Exits {
			w("  %-14s %5d  %s\n", model.EntryLabel(ex.Entry), ex.Port, exitLine(ex))
		}
	}
	var last model.Group
	for _, res := range r.Results {
		if res.Group != last {
			w("\n%s\n", st.c("1", groupTitle[res.Group]))
			last = res.Group
		}
		tag := kindLabel[res.Kind]
		if res.Advanced {
			tag += "·可选"
		}
		w("  %s %-3s %s（%s）", st.status(res.Status, res.Pending), res.ID, res.Title, tag)
		if res.Current != "" {
			w("  %s", st.c("90", res.Current))
		}
		w("\n")
		show := verbose || res.Status == model.Warn || res.Status == model.Fail || res.Status == model.Unknown
		if !show {
			continue
		}
		if res.Target != "" {
			w("        目标：%s\n", res.Target)
		}
		if res.Detail != "" {
			w("        原因：%s\n", res.Detail)
		}
		if res.FixID != "" {
			w("        修复：可一键修复（%s）\n", res.FixID)
		} else if res.Advice != "" {
			w("        怎么修：%s\n", res.Advice)
		}
	}
	s := r.Summary
	w("\n合计：通过 %d · 警告 %d · 失败 %d · 未知 %d · 跳过 %d · 可一键修复 %d\n", s.Pass, s.Warn, s.Fail, s.Unknown, s.NA, s.Fixable)
	return b.String()
}

func exitLine(ex model.Exit) string {
	if !ex.Listening {
		if ex.Err != "" {
			return ex.Err
		}
		return "端口未监听"
	}
	if ex.IP == "" {
		if ex.Err != "" {
			return ex.Err
		}
		return "未测到"
	}
	parts := []string{ex.IP}
	loc := strings.TrimSpace(ex.Country + " " + ex.City)
	if loc != "" {
		parts = append(parts, loc)
	}
	if ex.TimeZone != "" {
		parts = append(parts, ex.TimeZone)
	}
	if ex.TrustScore != nil {
		q := fmt.Sprintf("评分 %d", *ex.TrustScore)
		if ex.Residential != nil {
			q += map[bool]string{true: " 住宅", false: " 非住宅"}[*ex.Residential]
		}
		parts = append(parts, q)
	}
	if ex.DNS != nil && ex.DNS.Tested && ex.DNS.Err == "" {
		var rs []string
		for _, r := range ex.DNS.Resolvers {
			rs = append(rs, r.IP+" "+r.CountryCode)
		}
		if len(rs) == 0 {
			parts = append(parts, "DNS 未暴露")
		} else {
			parts = append(parts, "DNS "+strings.Join(rs, "、"))
		}
	}
	return strings.Join(parts, " · ")
}

// Plan 渲染修复计划。
func Plan(p model.FixPlan, st Style) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("%s  目标：%s\n", st.c("1", "修复计划"), p.Target.SourceText)
	for _, n := range p.Notes {
		w("提示：%s\n", n)
	}
	for _, a := range p.Actions {
		mark := "[ ]"
		if a.DefaultOn {
			mark = "[x]"
		}
		admin := ""
		if a.NeedsAdmin {
			admin = st.c("33", " 需要管理员")
		}
		w("\n%s %s  %s%s\n", mark, st.c("1", a.ID), a.Title, admin)
		w("    对应检查：%s\n", strings.Join(a.CheckIDs, "、"))
		for _, c := range a.Changes {
			w("    · %s：%s → %s\n", c.What, c.From, c.To)
		}
		if a.Note != "" {
			w("    说明：%s\n", a.Note)
		}
	}
	if len(p.Actions) > 0 {
		w("\n[x] 表示默认勾选。执行：ceg fix --apply --items default 或 --items %s\n", p.Actions[0].ID)
	}
	return b.String()
}

// Apply 渲染执行结果。
func Apply(r model.ApplyResult, st Style) string {
	var b strings.Builder
	for _, o := range r.Outcomes {
		mark := st.c("32", "[成功]")
		if !o.OK {
			mark = st.c("31", "[失败]")
		}
		fmt.Fprintf(&b, "%s %s\n", mark, o.Message)
	}
	fmt.Fprintf(&b, "%s\n", r.Message)
	return b.String()
}

// Clean 渲染清理清单。
func Clean(p model.CleanPlan, st Style) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("%s\n", st.c("1", p.Title))
	if len(p.Items) == 0 {
		w("没有需要清理的内容。\n")
	}
	for _, it := range p.Items {
		kind := "文件"
		if it.IsDir {
			kind = "目录"
		}
		act := "删除"
		if it.Action == "edit" {
			act = "修改"
		}
		w("  %s %s  %s，%.1f MB，%d 个文件", act, it.Path, kind, float64(it.Bytes)/1024/1024, it.Files)
		if it.Note != "" {
			w("（%s）", it.Note)
		}
		w("\n")
	}
	w("合计 %.1f MB\n", p.TotalMB)
	if p.Warning != "" {
		w("注意：%s\n", p.Warning)
	}
	if p.Blocked != "" {
		w("%s%s\n", st.c("31", "不能执行："), p.Blocked)
	}
	return b.String()
}
