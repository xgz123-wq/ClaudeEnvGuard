// Package platform 把所有 Windows 系统调用收拢到一个接口后面。
// 检查和修复逻辑只依赖 OS 接口，测试时换成 Fake，不碰真实系统。
// 文件读写直接用 os 包，测试通过 Paths 把根目录指向临时目录。
package platform

import (
	"context"
	"errors"
	"strings"
)

// Paths 是常用目录。全部来自系统已知文件夹，兼容桌面被 OneDrive 重定向的情况。
type Paths struct {
	Home            string `json:"home"`
	LocalAppData    string `json:"localAppData"`
	AppData         string `json:"appData"`
	ProgramFiles    string `json:"programFiles"`
	ProgramFilesX86 string `json:"programFilesX86"`
	Desktop         string `json:"desktop"`
	PublicDesktop   string `json:"publicDesktop"`
	StartMenu       string `json:"startMenu"`
	CommonStartMenu string `json:"commonStartMenu"`
}

// Shortcut 是一个 .lnk 文件的关键字段。
type Shortcut struct {
	Path        string `json:"path"`
	Target      string `json:"target"`
	Args        string `json:"args"`
	WorkDir     string `json:"workDir"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
}

// Process 是一个正在运行的进程。Exe 和 CmdLine 读不到时为空。
type Process struct {
	PID     int    `json:"pid"`
	Name    string `json:"name"`
	Exe     string `json:"exe,omitempty"`
	CmdLine string `json:"cmdLine,omitempty"`
}

// RegRoot 是注册表根键。
type RegRoot string

const (
	HKCU RegRoot = "HKCU"
	HKLM RegRoot = "HKLM"
)

// RegValue 是注册表值。Exists=false 表示值不存在（不是错误）。
type RegValue struct {
	Exists bool     `json:"exists"`
	Type   string   `json:"type,omitempty"` // sz / expand_sz / dword / multi_sz
	Str    string   `json:"str,omitempty"`
	DWord  uint32   `json:"dword,omitempty"`
	Multi  []string `json:"multi,omitempty"`
}

// Text 把任意类型的值转成字符串，便于展示和比较。
func (v RegValue) Text() string {
	if !v.Exists {
		return ""
	}
	switch v.Type {
	case "dword":
		return itoa(int(v.DWord))
	case "multi_sz":
		return strings.Join(v.Multi, ",")
	}
	return v.Str
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Signature 是文件的 Authenticode 签名。Valid 表示签名和证书链都校验通过；
// Org 是签名证书主题里的组织名（O=），读不到时为空。
type Signature struct {
	Valid   bool   `json:"valid"`
	Status  string `json:"status,omitempty"` // 校验不通过时的原因
	Subject string `json:"subject,omitempty"`
	Org     string `json:"org,omitempty"`
}

// ErrAccessDenied 表示需要管理员权限。
var ErrAccessDenied = errors.New("权限不足（需要管理员）")

// ErrElevationCancelled 表示用户在 UAC 弹窗里点了「否」。
var ErrElevationCancelled = errors.New("没有授予管理员权限（UAC 已取消）")

// OS 是检查和修复用到的全部系统能力。
type OS interface {
	Paths() Paths
	Getenv(key string) string

	RegRead(root RegRoot, key, name string) (RegValue, error)
	RegWrite(root RegRoot, key, name string, v RegValue) error
	RegDelete(root RegRoot, key, name string) error
	RegValueNames(root RegRoot, key string) ([]string, error)

	ReadShortcut(path string) (Shortcut, error)
	WriteShortcut(s Shortcut) error

	// Processes 列出进程；withCmdLine 里的进程名（小写，如 chrome.exe）会额外读取命令行。
	Processes(withCmdLine ...string) ([]Process, error)
	// PortOwner 返回在 127.0.0.1 / 0.0.0.0 上监听该 TCP 端口的进程 PID；没人监听返回 0。
	PortOwner(port int) (int, error)
	// PackageByFamily 查 MSIX 包；没装返回空字符串且 err 为 nil。
	PackageByFamily(family string) (fullName, installPath string, err error)

	Run(ctx context.Context, name string, args ...string) (output string, exitCode int, err error)
	// RunElevated 以管理员身份运行程序（弹 UAC）并等待结束；用户取消返回 ErrElevationCancelled。
	RunElevated(exe string, args []string) (exitCode int, err error)
	// StartDetached 按原样命令行启动程序，不等待（用于按快捷方式参数打开专用浏览器）。
	StartDetached(exe, rawArgs string) error
	// OpenPath 用资源管理器打开目录或文件所在位置。
	OpenPath(path string) error
	IsAdmin() bool
	UILanguage() string
	BroadcastEnvChange()
	GlobalIPv6() []string
	// FileSignature 校验文件的 Authenticode 签名（不联网查吊销）。
	FileSignature(path string) (Signature, error)
}
