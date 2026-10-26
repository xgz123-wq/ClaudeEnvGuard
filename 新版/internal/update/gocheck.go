package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ClaudeEnvGuard/internal/jsonx"
	"ClaudeEnvGuard/internal/netprobe"
	"ClaudeEnvGuard/internal/platform"
)

// 查版本改 Go（需求文档阶段 3）：做和脚本 -Action Check 一模一样的事，结果写成同样的 检查记录.json，
// 后面的状态判断不分来源。三个版本源同时查、只经 Desktop 专用端口；本机三项和 4 个程序指纹同时读。
// 来源、判定规则、出错原文都照 更新三端Claude.ps1 的同名函数，改那边时这里要跟着改。

// DesktopFamily 是 Claude Desktop 的 MSIX 包族名（和 facts.DesktopFamily 相同；这里不引 facts，免得更新页依赖检查包）。
const DesktopFamily = "Claude_pzs8sxrjxfjjc"

// Sources 是三个远端版本源，测试时换成本地假服务器。
type Sources struct {
	DesktopRedirect func(arch string) string // 官方最新版跳转入口：GET 读 307 的 Location
	NpmLatest       string                   // npm 官方源的 latest
	Marketplace     string                   // VS Code 官方市场查询接口
}

// DefaultSources 是固定来源，和脚本一致。
var DefaultSources = Sources{
	DesktopRedirect: func(arch string) string {
		return "https://claude.ai/api/desktop/win32/" + arch + "/msix/latest/redirect"
	},
	NpmLatest:   "https://registry.npmjs.org/@anthropic-ai/claude-code/latest",
	Marketplace: "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery",
}

const marketQuery = `{"filters":[{"criteria":[{"filterType":7,"value":"anthropic.claude-code"}]}],"flags":16}`

var reURLVersion = regexp.MustCompile(`/(\d+\.\d+\.\d+(\.\d+)?)/`)
var reSemver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var rePluginPath = regexp.MustCompile(`(?i)\\\.vscode\\extensions\\anthropic\.claude-code-`)

type desktopLatest struct{ Latest, URL string }

type pluginInstalled struct{ Version, Exe string }

// goCheck 返回和脚本 -Action Check 同样的记录；出错时记录里 Status=failed、Error 是出错原文。
func (r *Runner) goCheck(ctx context.Context) *scriptRecord {
	start := r.Now()
	rec := &scriptRecord{At: start.Format(time.RFC3339), Action: "Check", Status: "running",
		RecordPath: filepath.Join(r.CheckDir, "检查记录.json")}
	fail := func(err error) *scriptRecord {
		rec.Status, rec.Error = "failed", err.Error()
		return rec
	}
	arch, err := r.arch()
	if err != nil {
		return fail(err)
	}
	client := netprobe.Client(r.Proxy, 20*time.Second)
	noRedirect := netprobe.Client(r.Proxy, 20*time.Second)
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	var (
		wg                     sync.WaitGroup
		dl                     desktopLatest
		cliLatest, plugLatest  string
		dlErr, cliErr, plugErr error
	)
	wg.Add(3)
	go func() { defer wg.Done(); dl, dlErr = r.desktopLatest(ctx, noRedirect, arch) }()
	go func() { defer wg.Done(); cliLatest, cliErr = r.cliLatest(ctx, client) }()
	go func() { defer wg.Done(); plugLatest, plugErr = r.pluginLatest(ctx, client) }()

	// 本机三项在等网络的同时读；4 个程序的指纹也在等网络时算（要先知道插件现在的目录）。
	installed, instArch, instErr := r.installedDesktop()
	cliInst, cliInstErr := r.cliInstalled()
	plug, plugInstErr := r.pluginInstalled()
	var targets []scriptTarget
	var tgtErr error
	if plugInstErr == nil {
		wg.Add(1)
		go func() { defer wg.Done(); targets, tgtErr = r.targetStatus(ctx, plug.Exe) }()
	}
	wg.Wait()
	// 报错顺序和脚本一样：Desktop 已装 → Desktop 最新 → CLI 已装 → CLI 最新 → 插件已装 → 插件最新 → 指纹
	for _, e := range []error{instErr, archErr(instErr, instArch, arch), dlErr, cliInstErr, cliErr, plugInstErr, plugErr, tgtErr} {
		if e != nil {
			return fail(e)
		}
	}
	rec.Products = []scriptProduct{
		newProduct("Claude Desktop", installed, dl.Latest),
		newProduct("Claude Code CLI", cliInst, cliLatest),
		newProduct("VS Code Claude Code 插件", plug.Version, plugLatest),
	}
	// 查版本和算指纹同时进行，「查版本」记的是两者都完成的总秒数
	rec.Timing = orderedSeconds{Keys: []string{"查版本"}, Values: map[string]float64{"查版本": round1(r.Now().Sub(start).Seconds())}}
	rec.Targets = targets
	rec.Status = "checked"
	return rec
}

func archErr(instErr error, inst, sys string) error {
	if instErr != nil || inst == sys {
		return nil
	}
	return fmt.Errorf("已装包架构 %s 与系统架构 %s 不一致，停止。", inst, sys)
}

// arch 是系统架构，和脚本按 OSArchitecture 取的一致：x64 / arm64。32 位进程跑在 64 位系统上时看 PROCESSOR_ARCHITEW6432。
func (r *Runner) arch() (string, error) {
	a := r.Sys.Getenv("PROCESSOR_ARCHITEW6432")
	if a == "" {
		a = r.Sys.Getenv("PROCESSOR_ARCHITECTURE")
	}
	switch strings.ToUpper(a) {
	case "AMD64":
		return "x64", nil
	case "ARM64":
		return "arm64", nil
	}
	return "", fmt.Errorf("不支持的系统架构：%s", a)
}

// installedDesktop 读当前用户装的 Claude 包：包全名形如 Claude_3.6556.2.0_x64__pzs8sxrjxfjjc。
func (r *Runner) installedDesktop() (version, arch string, err error) {
	full, _, err := r.Sys.PackageByFamily(DesktopFamily)
	if err != nil || full == "" {
		return "", "", errors.New("当前用户 Claude 安装包不唯一或不存在；首次安装不走本流程。")
	}
	parts := strings.Split(full, "_")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("看不懂 Claude 包全名：%s", full)
	}
	return parts[1], parts[2], nil
}

func (r *Runner) desktopLatest(ctx context.Context, c *http.Client, arch string) (desktopLatest, error) {
	source := r.Src.DesktopRedirect(arch)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	resp, err := c.Do(req)
	if err != nil {
		return desktopLatest{}, fmt.Errorf("官方入口无响应；检查 Desktop 专用端口（%s）。（%v）", r.Proxy, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	switch resp.StatusCode {
	case 301, 302, 303, 307, 308:
	default:
		loc = ""
	}
	if loc == "" {
		return desktopLatest{}, fmt.Errorf("官方入口没有返回跳转，状态码：%d", resp.StatusCode)
	}
	base, _ := url.Parse(source)
	u, err := base.Parse(loc)
	if err != nil || u.Scheme != "https" || !(u.Hostname() == "claude.ai" || strings.HasSuffix(u.Hostname(), ".claude.ai")) {
		return desktopLatest{}, fmt.Errorf("跳转地址不在 claude.ai 的 HTTPS 下：%s", loc)
	}
	m := reURLVersion.FindStringSubmatch(u.Path)
	if m == nil {
		return desktopLatest{}, fmt.Errorf("跳转地址里没有版本号：%s", u.Path)
	}
	v := m[1]
	if strings.Count(v, ".") == 2 {
		v += ".0" // 地址里是三段、已装包是四段，补齐再比
	}
	return desktopLatest{Latest: v, URL: u.String()}, nil
}

// cliInstalled 读全局 CLI 的 package.json。
func (r *Runner) cliInstalled() (string, error) {
	p := filepath.Join(r.Sys.Paths().AppData, "npm", "node_modules", "@anthropic-ai", "claude-code", "package.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("没有找到全局 CLI：%s", p)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := jsonx.Unmarshal(b, &pkg); err != nil {
		return "", fmt.Errorf("全局 CLI 的 package.json 读不懂：%v", err)
	}
	return pkg.Version, nil
}

// cliLatest 等同 npm view @anthropic-ai/claude-code version --registry https://registry.npmjs.org，但只经 Desktop 端口。
func (r *Runner) cliLatest(ctx context.Context, c *http.Client) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.Src.NpmLatest, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("npm view 没有返回版本号：%v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body) != nil || !reSemver.MatchString(body.Version) {
		return "", fmt.Errorf("npm view 没有返回版本号：HTTP %d %s", resp.StatusCode, body.Version)
	}
	return body.Version, nil
}

// pluginInstalled 以 extensions.json 的当前登记为准（旧版本目录可能还在磁盘上）。
func (r *Runner) pluginInstalled() (pluginInstalled, error) {
	reg := filepath.Join(r.Sys.Paths().Home, ".vscode", "extensions", "extensions.json")
	b, err := os.ReadFile(reg)
	if err != nil {
		return pluginInstalled{}, fmt.Errorf("没有找到 %s", reg)
	}
	var list []struct {
		Identifier struct {
			ID string `json:"id"`
		} `json:"identifier"`
		Version  string `json:"version"`
		Location struct {
			Path string `json:"path"`
		} `json:"location"`
	}
	if err := jsonx.Unmarshal(b, &list); err != nil {
		return pluginInstalled{}, fmt.Errorf("extensions.json 读不懂：%v", err)
	}
	var hit []int
	for i, e := range list {
		if strings.EqualFold(e.Identifier.ID, "anthropic.claude-code") {
			hit = append(hit, i)
		}
	}
	if len(hit) != 1 {
		return pluginInstalled{}, fmt.Errorf("extensions.json 登记的 Claude Code 插件数量为 %d，预期 1", len(hit))
	}
	e := list[hit[0]]
	root := e.Location.Path
	if len(root) > 3 && root[0] == '/' && root[2] == ':' {
		root = root[1:]
	}
	root = strings.ReplaceAll(root, "/", `\`)
	return pluginInstalled{Version: e.Version, Exe: filepath.Join(root, "resources", "native-binary", "claude.exe")}, nil
}

// pluginLatest 只比正式版 win32-x64：预发布版不会推给正式通道，算进来会误报插件落后。
func (r *Runner) pluginLatest(ctx context.Context, c *http.Client) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, r.Src.Marketplace, strings.NewReader(marketQuery))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json;api-version=7.2-preview.1")
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("官方市场无响应：%v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Results []struct {
			Extensions []struct {
				Versions []struct {
					Version        string `json:"version"`
					TargetPlatform string `json:"targetPlatform"`
					Properties     []struct {
						Key   string `json:"key"`
						Value string `json:"value"`
					} `json:"properties"`
				} `json:"versions"`
			} `json:"extensions"`
		} `json:"results"`
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("官方市场无响应：HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("官方市场无响应：返回内容读不懂（%v）", err)
	}
	best := ""
	if len(body.Results) > 0 && len(body.Results[0].Extensions) > 0 {
		for _, v := range body.Results[0].Extensions[0].Versions {
			if v.TargetPlatform != "win32-x64" {
				continue
			}
			pre := false
			for _, p := range v.Properties {
				if p.Key == "Microsoft.VisualStudio.Code.PreRelease" && p.Value == "true" {
					pre = true
				}
			}
			if !pre && (best == "" || compareVersion(v.Version, best) > 0) {
				best = v.Version
			}
		}
	}
	if best == "" {
		return "", errors.New("官方市场没有返回 win32-x64 版本。")
	}
	return best, nil
}

// compareVersion 按 System.Version 的规则比较：逐段比数字，缺的段算 -1（2.1.310 < 2.1.310.0）。
func compareVersion(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := -1, -1
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func newProduct(name, installed, latest string) scriptProduct {
	st := "up_to_date"
	if compareVersion(latest, installed) > 0 {
		st = "update_available"
	}
	return scriptProduct{Product: name, Installed: installed, Latest: latest, Status: st}
}

// targetStatus 4 个受保护程序逐个对清单：unchanged / changed_signed / changed_unsigned / moved（插件换目录）/ missing。
// 4 个文件同时算哈希；哈希对不上才校验签名（WinVerifyTrust，不联网）。
func (r *Runner) targetStatus(ctx context.Context, pluginExe string) ([]scriptTarget, error) {
	path := filepath.Join(filepath.Dir(r.Script), "应用过滤目标.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到防护清单 %s：%v", path, err)
	}
	var m struct {
		Targets []struct {
			Name, Path, SHA256 string
		} `json:"Targets"`
	}
	if err := jsonx.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("防护清单 %s 读不懂：%v", path, err)
	}
	out := make([]scriptTarget, len(m.Targets))
	var wg sync.WaitGroup
	for i, t := range m.Targets {
		out[i] = scriptTarget{Name: t.Name, Path: t.Path}
		if rePluginPath.MatchString(t.Path) && !strings.EqualFold(t.Path, pluginExe) {
			out[i].State, out[i].NewPath = "moved", pluginExe
			continue
		}
		wg.Add(1)
		go func(row *scriptTarget, want string) {
			defer wg.Done()
			sum, err := fileSHA256(ctx, row.Path)
			if errors.Is(err, os.ErrNotExist) {
				row.State = "missing"
				return
			}
			if err != nil {
				row.State, row.Error = "missing", err.Error()
				return
			}
			row.SHA256 = sum
			switch {
			case strings.EqualFold(sum, want):
				row.State = "unchanged"
			case anthropicSigned(r.Sys, row.Path):
				row.State = "changed_signed"
			default:
				row.State = "changed_unsigned"
			}
		}(&out[i], t.SHA256)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("算程序指纹超时：%v", err)
	}
	return out, nil
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

// anthropicSigned 和脚本 Test-AnthropicSigned 一致：签名有效，且签名证书的组织是 Anthropic, PBC。
func anthropicSigned(sys platform.OS, path string) bool {
	sig, err := sys.FileSignature(path)
	return err == nil && sig.Valid && sig.Org == "Anthropic, PBC"
}
