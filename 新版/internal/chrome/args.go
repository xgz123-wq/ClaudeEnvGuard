// Package chrome 解析和生成专用浏览器快捷方式的启动参数。
package chrome

import (
	"path/filepath"
	"strings"
)

// 分享包要求的防旁路参数名。
const (
	FlagUserDataDir   = "--user-data-dir"
	FlagProxyServer   = "--proxy-server"
	FlagHostResolver  = "--host-resolver-rules"
	FlagLang          = "--lang"
	FlagAcceptLang    = "--accept-lang"
	FlagWebRTCPolicy  = "--force-webrtc-ip-handling-policy"
	WebRTCSafePolicy  = "disable_non_proxied_udp"
	HostResolverRules = "MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost"
)

var managed = []string{FlagUserDataDir, FlagProxyServer, FlagHostResolver, FlagLang, FlagAcceptLang, FlagWebRTCPolicy}

// Tokenize 按 Windows 命令行规则切分：空白分隔，引号内的空白不切，引号保留在片段里。
func Tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
			cur.WriteRune(r)
		case (r == ' ' || r == '\t') && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// Flags 是解析后的参数。
type Flags struct {
	Values map[string]string // 参数名（小写）→ 去引号后的值；无值参数为空串
	Extras []string          // 非托管参数，原样保留
	URLs   []string          // 末尾的网址
}

// Has 判断是否带某参数。
func (f Flags) Has(name string) bool {
	_, ok := f.Values[strings.ToLower(name)]
	return ok
}

// Get 取参数值。
func (f Flags) Get(name string) string {
	return f.Values[strings.ToLower(name)]
}

func unquote(s string) string {
	return strings.ReplaceAll(s, `"`, "")
}

// Parse 解析参数串。
func Parse(args string) Flags {
	f := Flags{Values: map[string]string{}}
	for _, tok := range Tokenize(args) {
		if strings.HasPrefix(tok, "-") {
			name, val, _ := strings.Cut(tok, "=")
			f.Values[strings.ToLower(unquote(name))] = unquote(val)
			if !isManaged(name) {
				f.Extras = append(f.Extras, tok)
			}
			continue
		}
		f.URLs = append(f.URLs, unquote(tok))
	}
	return f
}

func isManaged(name string) bool {
	name = strings.ToLower(unquote(name))
	for _, m := range managed {
		if name == m {
			return true
		}
	}
	return false
}

// Want 是期望的托管参数值。
type Want struct {
	UserDataDir string
	SocksPort   int
	Lang        string
	AcceptLang  string
	StartURL    string
}

// ProxyValue 返回期望的 --proxy-server 值。
func ProxyValue(port int) string {
	return "socks5://127.0.0.1:" + itoa(port)
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

// Build 生成新参数串：托管参数按分享包顺序重写，用户自己加的其他参数和网址保留。
func Build(old string, w Want) string {
	f := Parse(old)
	parts := []string{
		FlagUserDataDir + `="` + w.UserDataDir + `"`,
		FlagProxyServer + `="` + ProxyValue(w.SocksPort) + `"`,
		FlagHostResolver + `="` + HostResolverRules + `"`,
		FlagLang + "=" + w.Lang,
		FlagAcceptLang + `="` + w.AcceptLang + `"`,
		FlagWebRTCPolicy + "=" + WebRTCSafePolicy,
	}
	parts = append(parts, f.Extras...)
	if len(f.URLs) > 0 {
		for _, u := range f.URLs {
			parts = append(parts, u)
		}
	} else if w.StartURL != "" {
		parts = append(parts, w.StartURL)
	}
	return strings.Join(parts, " ")
}

// NormDir 归一化目录用于比较。
func NormDir(p string) string {
	p = strings.TrimSpace(unquote(p))
	if p == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(p))
}

// ProxyOK 判断 --proxy-server 是否指向期望的 SOCKS5 端口。
func ProxyOK(val string, port int) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	return v == ProxyValue(port) || v == "socks5h://127.0.0.1:"+itoa(port) || v == "socks5://localhost:"+itoa(port)
}

// ResolverOK 判断 --host-resolver-rules 是否把所有域名交给代理解析。
func ResolverOK(val string) bool {
	v := strings.ToUpper(strings.ReplaceAll(val, " ", ""))
	return strings.Contains(v, "MAP*~NOTFOUND")
}
