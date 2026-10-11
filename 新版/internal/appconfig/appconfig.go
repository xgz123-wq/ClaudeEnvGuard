// Package appconfig 管理工具自己的设置，存在 %LOCALAPPDATA%\ClaudeEnvGuard\设置.json。
package appconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 分享包约定的默认端口。
const (
	DefaultPortCLI     = 43865
	DefaultPortBrowser = 43866
	DefaultPortDesktop = 43867
	DefaultStartURL    = "https://ip.net.coffee/claude/"
)

// Settings 是用户可改的设置。
type Settings struct {
	PortCLI          int    `json:"portCli"`
	PortBrowser      int    `json:"portBrowser"`
	PortDesktop      int    `json:"portDesktop"`
	SelectedBrowser  string `json:"selectedBrowser"`
	RegionOverride   string `json:"regionOverride"` // 空 = 跟随出口 IP
	SharePackageRoot string `json:"sharePackageRoot"`
	StartURL         string `json:"startUrl"`
}

// Defaults 返回默认设置。
func Defaults() Settings {
	return Settings{PortCLI: DefaultPortCLI, PortBrowser: DefaultPortBrowser, PortDesktop: DefaultPortDesktop, StartURL: DefaultStartURL}
}

// Dir 是工具的数据目录。
func Dir(localAppData string) string {
	return filepath.Join(localAppData, "ClaudeEnvGuard")
}

func path(localAppData string) string {
	return filepath.Join(Dir(localAppData), "设置.json")
}

// Validate 校验端口；返回第一处问题。
func (s Settings) Validate() error {
	ports := map[int]string{}
	for _, p := range []struct {
		v    int
		name string
	}{{s.PortCLI, "CLI 端口"}, {s.PortBrowser, "浏览器端口"}, {s.PortDesktop, "Desktop 端口"}} {
		if p.v < 1 || p.v > 65535 {
			return fmt.Errorf("%s 必须在 1–65535 之间", p.name)
		}
		if other, ok := ports[p.v]; ok {
			return fmt.Errorf("%s 和 %s 不能相同", p.name, other)
		}
		ports[p.v] = p.name
	}
	if s.StartURL != "" && !strings.HasPrefix(s.StartURL, "https://") && !strings.HasPrefix(s.StartURL, "http://") {
		return errors.New("首页地址必须以 http:// 或 https:// 开头")
	}
	return nil
}

// Load 读设置；文件不存在或损坏时用默认值，并在 note 里说明。
func Load(localAppData string) (Settings, string) {
	s := Defaults()
	b, err := os.ReadFile(path(localAppData))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, ""
		}
		return s, "读取设置失败，已使用默认值：" + err.Error()
	}
	var loaded Settings
	if err := json.Unmarshal(trimBOM(b), &loaded); err != nil {
		return s, "设置文件已损坏，已使用默认值"
	}
	if loaded.PortCLI != 0 {
		s.PortCLI = loaded.PortCLI
	}
	if loaded.PortBrowser != 0 {
		s.PortBrowser = loaded.PortBrowser
	}
	if loaded.PortDesktop != 0 {
		s.PortDesktop = loaded.PortDesktop
	}
	if loaded.StartURL != "" {
		s.StartURL = loaded.StartURL
	}
	s.SelectedBrowser = loaded.SelectedBrowser
	s.RegionOverride = loaded.RegionOverride
	s.SharePackageRoot = loaded.SharePackageRoot
	if err := s.Validate(); err != nil {
		d := Defaults()
		d.SelectedBrowser, d.RegionOverride, d.SharePackageRoot = s.SelectedBrowser, s.RegionOverride, s.SharePackageRoot
		return d, "设置里的端口无效（" + err.Error() + "），已恢复默认端口"
	}
	return s, ""
}

// Save 原子写入设置。
func Save(localAppData string, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return WriteFileAtomic(path(localAppData), b)
}

// WriteFileAtomic 先写临时文件再改名，避免写一半断电损坏。
func WriteFileAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp-ceg"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}
