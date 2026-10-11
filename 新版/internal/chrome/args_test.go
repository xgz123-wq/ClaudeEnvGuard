package chrome

import (
	"strings"
	"testing"
)

func TestParseAndBuild(t *testing.T) {
	old := `--user-data-dir="C:\Users\张三\AppData\Local\ClaudeAccountBrowsers\KK\UserData" --proxy-server="http://127.0.0.1:7890" --disable-quic --lang=zh-CN https://claude.ai/`
	f := Parse(old)
	if f.Get(FlagUserDataDir) != `C:\Users\张三\AppData\Local\ClaudeAccountBrowsers\KK\UserData` {
		t.Errorf("中文路径解析不对：%q", f.Get(FlagUserDataDir))
	}
	if len(f.Extras) != 1 || f.Extras[0] != "--disable-quic" || len(f.URLs) != 1 {
		t.Errorf("额外参数或网址不对：%+v", f)
	}
	nb := Build(old, Want{UserDataDir: `C:\X\UserData`, SocksPort: 43866, Lang: "en-US", AcceptLang: "en-US,en", StartURL: "https://ip.net.coffee/claude/"})
	nf := Parse(nb)
	if !ProxyOK(nf.Get(FlagProxyServer), 43866) || !ResolverOK(nf.Get(FlagHostResolver)) || nf.Get(FlagWebRTCPolicy) != WebRTCSafePolicy {
		t.Errorf("防护参数不全：%s", nb)
	}
	if !strings.Contains(nb, "--disable-quic") || !strings.HasSuffix(nb, "https://claude.ai/") || strings.Contains(nb, "ip.net.coffee") {
		t.Errorf("应保留用户原有参数和网址：%s", nb)
	}
	if strings.Count(nb, "--lang=") != 1 || strings.Contains(nb, "zh-CN") {
		t.Errorf("语言参数应被替换且不重复：%s", nb)
	}
	empty := Build("", Want{UserDataDir: `C:\Y`, SocksPort: 1080, Lang: "ja-JP", AcceptLang: "ja-JP,ja", StartURL: "https://x/"})
	if !strings.HasSuffix(empty, " https://x/") {
		t.Errorf("没有网址时应加首页：%s", empty)
	}
}

func TestProxyAndResolver(t *testing.T) {
	if !ProxyOK("socks5://127.0.0.1:43866", 43866) || !ProxyOK("SOCKS5H://127.0.0.1:43866", 43866) {
		t.Errorf("合法代理被拒")
	}
	if ProxyOK("http://127.0.0.1:43866", 43866) || ProxyOK("socks5://127.0.0.1:43865", 43866) || ProxyOK("", 43866) {
		t.Errorf("非法代理被接受")
	}
	if !ResolverOK("MAP * ~NOTFOUND , EXCLUDE 127.0.0.1") || ResolverOK("MAP example.com 1.2.3.4") {
		t.Errorf("远端解析判断不对")
	}
	if NormDir(`"C:\A\B\"`) != NormDir(`c:\a\b`) {
		t.Errorf("目录归一化不对")
	}
}
