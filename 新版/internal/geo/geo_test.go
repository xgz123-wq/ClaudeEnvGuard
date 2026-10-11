package geo

import (
	"testing"
	"time"
)

func TestWindowsZoneMapping(t *testing.T) {
	cases := map[string]string{
		"America/New_York":    "Eastern Standard Time",
		"America/Los_Angeles": "Pacific Standard Time",
		"Asia/Tokyo":          "Tokyo Standard Time",
		"Asia/Shanghai":       "China Standard Time",
		"Asia/Kolkata":        "India Standard Time", // 新名，CLDR 里是 Asia/Calcutta
		"Europe/Kyiv":         "FLE Standard Time",
		"Nowhere/City":        "",
	}
	for iana, want := range cases {
		if got := WindowsZoneFor(iana); got != want {
			t.Errorf("WindowsZoneFor(%s) = %q，期望 %q", iana, got, want)
		}
	}
	if IANAForWindows("Eastern Standard Time") != "America/New_York" {
		t.Errorf("反查不对")
	}
}

func TestOffsets(t *testing.T) {
	summer := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if off, ok := OffsetMinutes("America/New_York", summer); !ok || off != -240 {
		t.Errorf("纽约夏令时应为 -240，得到 %d", off)
	}
	if off, _ := OffsetMinutes("Asia/Kolkata", summer); off != 330 {
		t.Errorf("印度应为 +330，得到 %d", off)
	}
	if FormatOffset(330) != "UTC+5:30" || FormatOffset(-240) != "UTC-4" || FormatOffset(0) != "UTC+0" {
		t.Errorf("格式化不对：%s %s %s", FormatOffset(330), FormatOffset(-240), FormatOffset(0))
	}
	if _, ok := OffsetMinutes("", summer); ok {
		t.Errorf("空时区应返回 false")
	}
}

func TestCountryAndLang(t *testing.T) {
	if c := LookupCountry("jp", ""); c.Lang != "ja-JP" || c.Name != "日本" {
		t.Errorf("日本信息不对：%+v", c)
	}
	if c := LookupCountry("ZZ", "Atlantis"); c.Lang != "en-US" || c.Name != "Atlantis" {
		t.Errorf("未收录国家应英语兜底：%+v", c)
	}
	if AcceptLangFor("en-US") != "en-US,en" || AcceptLangFor("ja-JP") != "ja-JP,ja,en-US,en" || AcceptLangFor("en-GB") != "en-GB,en-US,en" {
		t.Errorf("accept-lang 不对")
	}
	if ClaudeSupported("CN") || ClaudeSupported("hk") || !ClaudeSupported("US") {
		t.Errorf("支持地区判断不对")
	}
	if _, ok := ChoiceByID("us-west"); !ok {
		t.Errorf("手选地区缺失")
	}
	for _, c := range Choices {
		if WindowsZoneFor(c.IANA) == "" {
			t.Errorf("手选地区 %s 的时区 %s 找不到 Windows 对应", c.ID, c.IANA)
		}
	}
}
