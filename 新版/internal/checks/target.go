// Package checks 把现场事实判定成检查结果。这里全是纯函数：同样的事实永远得到同样的结论，
// 便于离线测试。目标值（时区、语言等）由 ResolveTarget 按出口 IP 或用户手选推出。
package checks

import (
	"ClaudeEnvGuard/internal/facts"
	"ClaudeEnvGuard/internal/geo"
	"ClaudeEnvGuard/internal/model"
)

// ResolveTarget 推出目标环境：用户手选优先；否则按出口 IP（浏览器入口优先）。
func ResolveTarget(f *facts.Facts) model.Target {
	if id := f.Settings.RegionOverride; id != "" {
		if ch, ok := geo.ChoiceByID(id); ok {
			c := geo.LookupCountry(ch.CountryCode, "")
			return fill(model.Target{Source: "manual", SourceText: "手选：" + ch.Label, CountryCode: c.Code, Country: c.Name, IANA: ch.IANA}, c)
		}
	}
	for _, e := range []model.Entry{model.EntryBrowser, model.EntryDesktop, model.EntryCLI} {
		ex := f.Exits[e]
		if ex == nil || ex.CountryCode == "" {
			continue
		}
		c := geo.LookupCountry(ex.CountryCode, "")
		iana := ex.TimeZone
		if iana == "" {
			iana = c.Primary
		}
		text := "跟随出口 IP（" + model.EntryLabel(e) + "：" + c.Name
		if ex.City != "" {
			text += " · " + ex.City
		}
		text += "）"
		return fill(model.Target{Source: "auto", SourceText: text, CountryCode: c.Code, Country: c.Name, IANA: iana}, c)
	}
	if f.Offline {
		return model.Target{Source: "none", SourceText: "离线模式：没测出口，可在设置里手选目标地区"}
	}
	if !f.NetworkDone {
		return model.Target{Source: "none", SourceText: "正在测出口…"}
	}
	return model.Target{Source: "none", SourceText: "没测到出口：随 IP 的项暂时无法判断，可手选目标地区"}
}

func fill(t model.Target, c geo.Country) model.Target {
	t.WindowsTZ = geo.WindowsZoneFor(t.IANA)
	t.Lang = c.Lang
	t.AcceptLang = geo.AcceptLangFor(c.Lang)
	t.Locale = c.Lang
	return t
}
