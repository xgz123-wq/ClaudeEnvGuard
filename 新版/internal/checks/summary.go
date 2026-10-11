package checks

import (
	"fmt"

	"ClaudeEnvGuard/internal/model"
)

// 下一步的优先顺序：越靠前越是其他项的前提。
var priority = []string{"P1", "P2", "P4", "B1", "B2", "B3", "B4", "B5", "B7", "D2", "C2", "C3", "P3", "B6", "S1", "S4",
	"C4", "B8", "C6", "D4", "P5", "C5", "D3", "S2", "S3", "S7", "S6", "S5"}

// Summarize 汇总结果，给出一句话结论和最该先做的一步。
func Summarize(results []model.Result) model.Summary {
	var s model.Summary
	byID := map[string]model.Result{}
	mainWorst := model.NA
	optional := 0
	for _, r := range results {
		byID[r.ID] = r
		if r.Pending {
			continue
		}
		switch r.Status {
		case model.Pass:
			s.Pass++
		case model.Warn:
			s.Warn++
		case model.Fail:
			s.Fail++
		case model.Unknown:
			s.Unknown++
		case model.NA:
			s.NA++
		}
		if r.FixID != "" && (r.Status == model.Warn || r.Status == model.Fail) {
			s.Fixable++
		}
		if r.Advanced {
			if r.Status == model.Warn || r.Status == model.Fail {
				optional++
			}
			continue
		}
		if r.Status.Rank() > mainWorst.Rank() {
			mainWorst = r.Status
		}
	}
	s.Overall = mainWorst
	mainFail, mainWarn, mainUnknown := 0, 0, 0
	for _, r := range results {
		if r.Advanced || r.Pending {
			continue
		}
		switch r.Status {
		case model.Fail:
			mainFail++
		case model.Warn:
			mainWarn++
		case model.Unknown:
			mainUnknown++
		}
	}
	switch {
	case mainFail > 0:
		s.Headline = fmt.Sprintf("有 %d 项没配好，需要处理", mainFail)
		if mainWarn > 0 {
			s.Headline += fmt.Sprintf("，另有 %d 项建议处理", mainWarn)
		}
	case mainWarn > 0:
		s.Headline = fmt.Sprintf("基本可用，有 %d 项建议处理", mainWarn)
	case mainUnknown > 0:
		s.Headline = fmt.Sprintf("已检查的都没问题，有 %d 项没测出来", mainUnknown)
	default:
		s.Headline = "全部通过"
	}
	if optional > 0 {
		s.Headline += fmt.Sprintf("（可选优化 %d 项）", optional)
	}
	for _, want := range []model.Status{model.Fail, model.Warn} {
		for _, id := range priority {
			r, ok := byID[id]
			if !ok || r.Pending || r.Status != want || (r.Advanced && want == model.Warn) {
				continue
			}
			s.NextStep = r.Title + "："
			if r.FixID != "" {
				s.NextStep += "可以一键修复。"
			} else if r.Advice != "" {
				s.NextStep += r.Advice
			} else {
				s.NextStep += r.Detail
			}
			return s
		}
	}
	return s
}
