// Package manifest 读分享包脚本写的部署清单 %LOCALAPPDATA%\ClaudeNetworkGuard\部署清单.json。
// 清单是这套环境的「应该是什么」：三个端口（从 Clash 抄来）、脚本根目录、登记过的专用浏览器。
// 由一键总检每次从 Clash 运行配置刷新、创建账号浏览器建完登记；卫士只读不写，读到的每一项都还要实际核对。
package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ClaudeEnvGuard/internal/jsonx"
)

// Ports 是清单里的三个端口；名字和分享包 读部署清单.ps1 的 Get-DeployPort 一致。
type Ports struct {
	CLI     int `json:"通用"`      // Clash 混合端口：Claude Code、VS Code 插件
	Browser int `json:"浏览器"`     // 专用 Chrome 的 SOCKS
	Desktop int `json:"Desktop"` // Claude Desktop 的 HTTP
}

// Browser 是登记过的一个专用浏览器。
type Browser struct {
	Alias       string `json:"别名"`
	UserDataDir string `json:"数据目录"`
	Shortcut    string `json:"快捷方式"`
}

// Manifest 是部署清单的内容。
type Manifest struct {
	FormatVersion int       `json:"格式版本"`
	UpdatedAt     string    `json:"更新时间"`
	Ports         Ports     `json:"端口"`
	ScriptRoot    string    `json:"脚本根目录"`
	Browsers      []Browser `json:"专用浏览器"`
}

// Path 返回清单位置。
func Path(localAppData string) string {
	return filepath.Join(localAppData, "ClaudeNetworkGuard", "部署清单.json")
}

// Load 读清单。文件不存在返回 (nil, nil)；读不了或格式坏返回错误，调用方按没有清单处理。
func Load(localAppData string) (*Manifest, error) {
	b, err := os.ReadFile(Path(localAppData))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var m Manifest
	if err := jsonx.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("部署清单格式错误：%w", err)
	}
	return &m, nil
}

// PortsValid 判断三个端口都在 1–65535 且互不相同；不满足时卫士不用清单里的端口。
func (m *Manifest) PortsValid() bool {
	if m == nil {
		return false
	}
	p := m.Ports
	for _, v := range []int{p.CLI, p.Browser, p.Desktop} {
		if v < 1 || v > 65535 {
			return false
		}
	}
	return p.CLI != p.Browser && p.CLI != p.Desktop && p.Browser != p.Desktop
}
