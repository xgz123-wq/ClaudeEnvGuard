// Package scriptroot 找分享包（脚本根目录）。更新页、深度总检、英文启动器、直连防护状态（D3）共用这一个找法，按顺序取第一个是分享包的目录：
//  1. 设置里手填的分享包目录；
//  2. 部署清单的「脚本根目录」（一键总检每次刷新，写的是跑总检的那一份）；
//  3. 桌面\AI应用开发、桌面\Claude环境分享版；
//  4. 桌面和「文档」下的一级子文件夹，有直连防护状态文件的优先。
//
// 认分享包的标志是 docs\配置\Claude环境\README.md。本机的活动源是 AI应用开发，分享包里的副本只给别的电脑用。
package scriptroot

import (
	"os"
	"path/filepath"

	"ClaudeEnvGuard/internal/manifest"
	"ClaudeEnvGuard/internal/platform"
)

// DocDir 返回分享包里 Claude环境 文档目录。
func DocDir(root string) string {
	return filepath.Join(root, "docs", "配置", "Claude环境")
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// IsShareRoot 判断目录是不是分享包。
func IsShareRoot(dir string) bool {
	return dir != "" && exists(filepath.Join(DocDir(dir), "README.md"))
}

// Fixed 按顺序返回第 1–3 步的候选目录（不看是否存在）。shareRoot 是设置里的分享包目录（可为空）。
func Fixed(p platform.Paths, shareRoot string) []string {
	var roots []string
	if shareRoot != "" {
		roots = append(roots, shareRoot)
	}
	if m, err := manifest.Load(p.LocalAppData); err == nil && m != nil && m.ScriptRoot != "" {
		roots = append(roots, m.ScriptRoot)
	}
	return append(roots, filepath.Join(p.Desktop, "AI应用开发"), filepath.Join(p.Desktop, "Claude环境分享版"))
}

// Root 返回找到的分享包根目录；都不是时返回空串。
func Root(p platform.Paths, shareRoot string) string {
	for _, r := range Fixed(p, shareRoot) {
		if IsShareRoot(r) {
			return r
		}
	}
	first := ""
	for _, base := range []string{p.Desktop, filepath.Join(p.Home, "Documents")} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			c := filepath.Join(base, e.Name())
			if !e.IsDir() || !IsShareRoot(c) {
				continue
			}
			if exists(filepath.Join(DocDir(c), "Desktop防护", "应用直连防护状态.json")) {
				return c
			}
			if first == "" {
				first = c
			}
		}
	}
	return first
}

// Find 返回 分享包\rel；找不到分享包时返回第一个固定候选下的位置，报错时显示它。
func Find(p platform.Paths, shareRoot, rel string) string {
	if r := Root(p, shareRoot); r != "" {
		return filepath.Join(r, rel)
	}
	return filepath.Join(Fixed(p, shareRoot)[0], rel)
}
