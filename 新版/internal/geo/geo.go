// Package geo 负责「随 IP」的映射：IANA 时区 ↔ Windows 时区、国家 → 语言与区域、Claude 支持地区。
package geo

import (
	_ "embed"
	"encoding/xml"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // 用户电脑上没有 Go 的时区库，必须内置
)

//go:embed windowsZones.xml
var windowsZonesXML []byte

type mapZone struct {
	Other     string `xml:"other,attr"`
	Territory string `xml:"territory,attr"`
	Type      string `xml:"type,attr"`
}

var (
	zonesOnce   sync.Once
	ianaToWin   map[string]string // IANA → Windows ID
	winToIANA   map[string]string // Windows ID → 代表性 IANA（territory=001）
	zonesLoaded bool
)

// 新旧 IANA 名称别名：接口常返回新名，CLDR 表里还是旧名。
var ianaAliases = map[string]string{
	"Asia/Kolkata":                   "Asia/Calcutta",
	"Asia/Ho_Chi_Minh":               "Asia/Saigon",
	"Asia/Kathmandu":                 "Asia/Katmandu",
	"Asia/Yangon":                    "Asia/Rangoon",
	"Europe/Kyiv":                    "Europe/Kiev",
	"America/Nuuk":                   "America/Godthab",
	"Atlantic/Faroe":                 "Atlantic/Faeroe",
	"Pacific/Chuuk":                  "Pacific/Truk",
	"Pacific/Pohnpei":                "Pacific/Ponape",
	"Africa/Asmara":                  "Africa/Asmera",
	"America/Atikokan":               "America/Coral_Harbour",
	"America/Argentina/Buenos_Aires": "America/Buenos_Aires",
	"America/Indiana/Indianapolis":   "America/Indianapolis",
	"America/Kentucky/Louisville":    "America/Louisville",
	"Asia/Macau":                     "Asia/Macau",
}

func loadZones() {
	zonesOnce.Do(func() {
		ianaToWin = map[string]string{}
		winToIANA = map[string]string{}
		var doc struct {
			Zones []mapZone `xml:"windowsZones>mapTimezones>mapZone"`
		}
		if err := xml.Unmarshal(windowsZonesXML, &doc); err != nil {
			return
		}
		for _, z := range doc.Zones {
			for _, id := range strings.Fields(z.Type) {
				if _, ok := ianaToWin[id]; !ok || z.Territory != "001" {
					ianaToWin[id] = z.Other
				}
			}
			if z.Territory == "001" {
				winToIANA[z.Other] = strings.Fields(z.Type)[0]
			}
		}
		zonesLoaded = len(ianaToWin) > 0
	})
}

// WindowsZoneFor 把 IANA 时区转成 Windows 时区 ID；查不到返回空。
func WindowsZoneFor(iana string) string {
	loadZones()
	if iana == "" {
		return ""
	}
	if w, ok := ianaToWin[iana]; ok {
		return w
	}
	if a, ok := ianaAliases[iana]; ok {
		if w, ok := ianaToWin[a]; ok {
			return w
		}
	}
	return ""
}

// IANAForWindows 返回 Windows 时区对应的代表性 IANA 时区；查不到返回空。
func IANAForWindows(win string) string {
	loadZones()
	return winToIANA[win]
}

// OffsetMinutes 返回某 IANA 时区在 at 时刻相对 UTC 的分钟数。
func OffsetMinutes(iana string, at time.Time) (int, bool) {
	if iana == "" {
		return 0, false
	}
	loc, err := time.LoadLocation(iana)
	if err != nil {
		if a, ok := ianaAliases[iana]; ok {
			loc, err = time.LoadLocation(a)
		}
		if err != nil {
			return 0, false
		}
	}
	_, off := at.In(loc).Zone()
	return off / 60, true
}

// FormatOffset 把分钟数格式化成 UTC+8 / UTC-4 / UTC+5:30。
func FormatOffset(min int) string {
	sign := "+"
	if min < 0 {
		sign = "-"
		min = -min
	}
	h, m := min/60, min%60
	if m == 0 {
		return "UTC" + sign + itoa(h)
	}
	return "UTC" + sign + itoa(h) + ":" + pad2(m)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// Country 是一个国家的本地化信息。
type Country struct {
	Code    string // ISO 两位大写
	Name    string // 中文名
	Lang    string // 浏览器首选语言
	English bool   // 英语是否官方/通行语言
	Primary string // 默认 IANA 时区（接口没给时区时用）
}

var countries = map[string]Country{
	"US": {"US", "美国", "en-US", true, "America/New_York"},
	"CA": {"CA", "加拿大", "en-CA", true, "America/Toronto"},
	"GB": {"GB", "英国", "en-GB", true, "Europe/London"},
	"IE": {"IE", "爱尔兰", "en-IE", true, "Europe/Dublin"},
	"AU": {"AU", "澳大利亚", "en-AU", true, "Australia/Sydney"},
	"NZ": {"NZ", "新西兰", "en-NZ", true, "Pacific/Auckland"},
	"SG": {"SG", "新加坡", "en-SG", true, "Asia/Singapore"},
	"IN": {"IN", "印度", "en-IN", true, "Asia/Kolkata"},
	"PH": {"PH", "菲律宾", "en-PH", true, "Asia/Manila"},
	"MY": {"MY", "马来西亚", "en-MY", true, "Asia/Kuala_Lumpur"},
	"ZA": {"ZA", "南非", "en-ZA", true, "Africa/Johannesburg"},
	"NG": {"NG", "尼日利亚", "en-NG", true, "Africa/Lagos"},
	"JP": {"JP", "日本", "ja-JP", false, "Asia/Tokyo"},
	"KR": {"KR", "韩国", "ko-KR", false, "Asia/Seoul"},
	"TW": {"TW", "台湾", "zh-TW", false, "Asia/Taipei"},
	"HK": {"HK", "香港", "zh-HK", true, "Asia/Hong_Kong"},
	"MO": {"MO", "澳门", "zh-MO", false, "Asia/Macau"},
	"CN": {"CN", "中国大陆", "zh-CN", false, "Asia/Shanghai"},
	"TH": {"TH", "泰国", "th-TH", false, "Asia/Bangkok"},
	"VN": {"VN", "越南", "vi-VN", false, "Asia/Ho_Chi_Minh"},
	"ID": {"ID", "印度尼西亚", "id-ID", false, "Asia/Jakarta"},
	"DE": {"DE", "德国", "de-DE", false, "Europe/Berlin"},
	"FR": {"FR", "法国", "fr-FR", false, "Europe/Paris"},
	"NL": {"NL", "荷兰", "nl-NL", false, "Europe/Amsterdam"},
	"BE": {"BE", "比利时", "nl-BE", false, "Europe/Brussels"},
	"LU": {"LU", "卢森堡", "fr-LU", false, "Europe/Luxembourg"},
	"CH": {"CH", "瑞士", "de-CH", false, "Europe/Zurich"},
	"AT": {"AT", "奥地利", "de-AT", false, "Europe/Vienna"},
	"ES": {"ES", "西班牙", "es-ES", false, "Europe/Madrid"},
	"PT": {"PT", "葡萄牙", "pt-PT", false, "Europe/Lisbon"},
	"IT": {"IT", "意大利", "it-IT", false, "Europe/Rome"},
	"SE": {"SE", "瑞典", "sv-SE", false, "Europe/Stockholm"},
	"NO": {"NO", "挪威", "nb-NO", false, "Europe/Oslo"},
	"DK": {"DK", "丹麦", "da-DK", false, "Europe/Copenhagen"},
	"FI": {"FI", "芬兰", "fi-FI", false, "Europe/Helsinki"},
	"IS": {"IS", "冰岛", "is-IS", false, "Atlantic/Reykjavik"},
	"PL": {"PL", "波兰", "pl-PL", false, "Europe/Warsaw"},
	"CZ": {"CZ", "捷克", "cs-CZ", false, "Europe/Prague"},
	"HU": {"HU", "匈牙利", "hu-HU", false, "Europe/Budapest"},
	"RO": {"RO", "罗马尼亚", "ro-RO", false, "Europe/Bucharest"},
	"BG": {"BG", "保加利亚", "bg-BG", false, "Europe/Sofia"},
	"GR": {"GR", "希腊", "el-GR", false, "Europe/Athens"},
	"UA": {"UA", "乌克兰", "uk-UA", false, "Europe/Kyiv"},
	"EE": {"EE", "爱沙尼亚", "et-EE", false, "Europe/Tallinn"},
	"LV": {"LV", "拉脱维亚", "lv-LV", false, "Europe/Riga"},
	"LT": {"LT", "立陶宛", "lt-LT", false, "Europe/Vilnius"},
	"TR": {"TR", "土耳其", "tr-TR", false, "Europe/Istanbul"},
	"IL": {"IL", "以色列", "he-IL", false, "Asia/Jerusalem"},
	"AE": {"AE", "阿联酋", "ar-AE", false, "Asia/Dubai"},
	"SA": {"SA", "沙特阿拉伯", "ar-SA", false, "Asia/Riyadh"},
	"BR": {"BR", "巴西", "pt-BR", false, "America/Sao_Paulo"},
	"MX": {"MX", "墨西哥", "es-MX", false, "America/Mexico_City"},
	"AR": {"AR", "阿根廷", "es-AR", false, "America/Argentina/Buenos_Aires"},
	"CL": {"CL", "智利", "es-CL", false, "America/Santiago"},
	"CO": {"CO", "哥伦比亚", "es-CO", false, "America/Bogota"},
	"PE": {"PE", "秘鲁", "es-PE", false, "America/Lima"},
	"RU": {"RU", "俄罗斯", "ru-RU", false, "Europe/Moscow"},
	"BY": {"BY", "白俄罗斯", "be-BY", false, "Europe/Minsk"},
	"IR": {"IR", "伊朗", "fa-IR", false, "Asia/Tehran"},
	"KP": {"KP", "朝鲜", "ko-KP", false, "Asia/Pyongyang"},
	"CU": {"CU", "古巴", "es-CU", false, "America/Havana"},
	"SY": {"SY", "叙利亚", "ar-SY", false, "Asia/Damascus"},
}

// LookupCountry 查国家信息；未收录的国家用英语兜底。
func LookupCountry(code, fallbackName string) Country {
	code = strings.ToUpper(strings.TrimSpace(code))
	if c, ok := countries[code]; ok {
		return c
	}
	name := fallbackName
	if name == "" {
		name = code
	}
	return Country{Code: code, Name: name, Lang: "en-US", English: true}
}

// unsupported 是 Claude 明确不提供服务的地区（保守列表，只收公开确认的）。
var unsupported = map[string]bool{"CN": true, "HK": true, "MO": true, "RU": true, "BY": true, "IR": true, "KP": true, "CU": true, "SY": true}

// ClaudeSupported 判断出口地区是否在 Claude 服务范围内。
func ClaudeSupported(code string) bool {
	return !unsupported[strings.ToUpper(code)]
}

// AcceptLangFor 生成 --accept-lang 参数值。
func AcceptLangFor(lang string) string {
	base := strings.SplitN(lang, "-", 2)[0]
	if base == "en" {
		if lang == "en-US" {
			return "en-US,en"
		}
		return lang + ",en-US,en"
	}
	return lang + "," + base + ",en-US,en"
}

// LangBase 返回语言主标签，如 zh-CN → zh。
func LangBase(lang string) string {
	return strings.ToLower(strings.SplitN(strings.TrimSpace(lang), "-", 2)[0])
}

// RegionChoice 是手选目标地区的选项。
type RegionChoice struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	CountryCode string `json:"countryCode"`
	IANA        string `json:"iana"`
}

// Choices 是常用手选地区。
var Choices = []RegionChoice{
	{"us-west", "美国西部（洛杉矶）", "US", "America/Los_Angeles"},
	{"us-mountain", "美国山地（丹佛）", "US", "America/Denver"},
	{"us-central", "美国中部（芝加哥）", "US", "America/Chicago"},
	{"us-east", "美国东部（纽约）", "US", "America/New_York"},
	{"ca-east", "加拿大东部（多伦多）", "CA", "America/Toronto"},
	{"gb", "英国（伦敦）", "GB", "Europe/London"},
	{"de", "德国（柏林）", "DE", "Europe/Berlin"},
	{"fr", "法国（巴黎）", "FR", "Europe/Paris"},
	{"nl", "荷兰（阿姆斯特丹）", "NL", "Europe/Amsterdam"},
	{"jp", "日本（东京）", "JP", "Asia/Tokyo"},
	{"kr", "韩国（首尔）", "KR", "Asia/Seoul"},
	{"sg", "新加坡", "SG", "Asia/Singapore"},
	{"tw", "台湾（台北）", "TW", "Asia/Taipei"},
	{"au", "澳大利亚（悉尼）", "AU", "Australia/Sydney"},
}

// ChoiceByID 按 ID 查手选地区。
func ChoiceByID(id string) (RegionChoice, bool) {
	for _, c := range Choices {
		if c.ID == id {
			return c, true
		}
	}
	return RegionChoice{}, false
}
