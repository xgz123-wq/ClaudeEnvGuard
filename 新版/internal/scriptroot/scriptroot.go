// Package scriptroot 找分享包里的 PowerShell 脚本。更新页、深度总检、英文启动器共用这一个找法：
// 桌面\AI应用开发 → 设置里的分享包目录 → 桌面\Claude环境分享版，取第一个存在的；
// 本机的活动源是 AI应用开发，分享包里的副本只给别的电脑用。
package scriptroot

import (
	"os"
	"path/filepath"

	"ClaudeEnvGuard/internal/platform"
)

// Roots 按查找顺序返回候选根目录。shareRoot 是设置里的分享包目录（可为空）。
func Roots(p platform.Paths, shareRoot string) []string {
	roots := []string{filepath.Join(p.Desktop, "AI应用开发")}
	if shareRoot != "" {
		roots = append(roots, shareRoot)
	}
	return append(roots, filepath.Join(p.Desktop, "Claude环境分享版"))
}

// Find 返回第一个存在的 根目录\rel；都不存在时返回第一个候选位置，报错时显示它。
func Find(p platform.Paths, shareRoot, rel string) string {
	roots := Roots(p, shareRoot)
	for _, root := range roots {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return filepath.Join(root, rel)
		}
	}
	return filepath.Join(roots[0], rel)
}
