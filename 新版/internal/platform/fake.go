package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Fake 是测试用的内存系统。目录指向临时文件夹，注册表和快捷方式存在内存里。
type Fake struct {
	mu          sync.Mutex
	P           Paths
	Env         map[string]string
	Reg         map[string]RegValue // 键：根\路径\名称（小写）
	DeniedKeys  map[string]bool     // 写这些键（根\路径，小写）时返回权限不足
	Procs       []Process
	Ports       map[int]int // 端口 → PID
	Pkg         map[string][2]string
	Admin       bool
	UILang      string
	IPv6        []string
	RunLog      []string
	RunFunc     func(name string, args ...string) (string, int, error)
	ElevateFunc func(exe string, args []string) (int, error)
}

// NewFake 在 root 下建好常用目录。
func NewFake(root string) *Fake {
	p := Paths{
		Home:            filepath.Join(root, "home"),
		LocalAppData:    filepath.Join(root, "home", "AppData", "Local"),
		AppData:         filepath.Join(root, "home", "AppData", "Roaming"),
		ProgramFiles:    filepath.Join(root, "Program Files"),
		ProgramFilesX86: filepath.Join(root, "Program Files (x86)"),
		Desktop:         filepath.Join(root, "home", "Desktop"),
		PublicDesktop:   filepath.Join(root, "Public", "Desktop"),
		StartMenu:       filepath.Join(root, "home", "StartMenu"),
		CommonStartMenu: filepath.Join(root, "ProgramData", "StartMenu"),
	}
	for _, d := range []string{p.Home, p.LocalAppData, p.AppData, p.ProgramFiles, p.Desktop, p.StartMenu} {
		_ = os.MkdirAll(d, 0o755)
	}
	return &Fake{P: p, Env: map[string]string{}, Reg: map[string]RegValue{}, DeniedKeys: map[string]bool{},
		Ports: map[int]int{}, Pkg: map[string][2]string{}}
}

func regKey(root RegRoot, key, name string) string {
	return strings.ToLower(string(root) + `\` + key + `\` + name)
}

// SetReg 是测试辅助：直接写入注册表值。
func (f *Fake) SetReg(root RegRoot, key, name string, v RegValue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v.Exists = true
	f.Reg[regKey(root, key, name)] = v
}

func (f *Fake) Paths() Paths             { return f.P }
func (f *Fake) Getenv(key string) string { return f.Env[key] }

func (f *Fake) RegRead(root RegRoot, key, name string) (RegValue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Reg[regKey(root, key, name)], nil
}

func (f *Fake) RegWrite(root RegRoot, key, name string, v RegValue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeniedKeys[strings.ToLower(string(root)+`\`+key)] {
		return ErrAccessDenied
	}
	v.Exists = true
	f.Reg[regKey(root, key, name)] = v
	return nil
}

func (f *Fake) RegDelete(root RegRoot, key, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeniedKeys[strings.ToLower(string(root)+`\`+key)] {
		return ErrAccessDenied
	}
	delete(f.Reg, regKey(root, key, name))
	return nil
}

func (f *Fake) RegValueNames(root RegRoot, key string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := strings.ToLower(string(root) + `\` + key + `\`)
	var out []string
	for k := range f.Reg {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k[len(prefix):])
		}
	}
	return out, nil
}

// ReadShortcut 从磁盘上的占位文件读回快捷方式（WriteShortcut 写的格式），还原备份后无需额外同步。
func (f *Fake) ReadShortcut(path string) (Shortcut, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Shortcut{}, err
	}
	parts := strings.Split(string(b), "\n")
	if len(parts) < 6 || parts[0] != "FAKE-LNK" {
		return Shortcut{}, fmt.Errorf("不是有效的快捷方式")
	}
	return Shortcut{Path: path, Target: parts[1], Args: parts[2], WorkDir: parts[3], Icon: parts[4], Description: parts[5]}, nil
}

// WriteShortcut 在磁盘上写一个占位文件。
func (f *Fake) WriteShortcut(s Shortcut) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data := fmt.Sprintf("FAKE-LNK\n%s\n%s\n%s\n%s\n%s\n", s.Target, s.Args, s.WorkDir, s.Icon, s.Description)
	return os.WriteFile(s.Path, []byte(data), 0o644)
}

func (f *Fake) Processes(withCmdLine ...string) ([]Process, error) { return f.Procs, nil }
func (f *Fake) PortOwner(port int) (int, error)                    { return f.Ports[port], nil }
func (f *Fake) PackageByFamily(family string) (string, string, error) {
	v := f.Pkg[family]
	return v[0], v[1], nil
}

func (f *Fake) Run(ctx context.Context, name string, args ...string) (string, int, error) {
	f.mu.Lock()
	f.RunLog = append(f.RunLog, name+" "+strings.Join(args, " "))
	fn := f.RunFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(name, args...)
	}
	return "", 0, nil
}

// RunElevated 在测试里交给 ElevateFunc；没设置时视为用户取消。
func (f *Fake) RunElevated(exe string, args []string) (int, error) {
	f.mu.Lock()
	fn := f.ElevateFunc
	f.mu.Unlock()
	if fn == nil {
		return 0, ErrElevationCancelled
	}
	return fn(exe, args)
}

func (f *Fake) IsAdmin() bool        { return f.Admin }
func (f *Fake) UILanguage() string   { return f.UILang }
func (f *Fake) BroadcastEnvChange()  {}
func (f *Fake) GlobalIPv6() []string { return f.IPv6 }
