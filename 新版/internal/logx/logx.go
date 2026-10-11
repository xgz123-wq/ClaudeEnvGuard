// Package logx 写运行日志到 %LOCALAPPDATA%\ClaudeEnvGuard\日志\yyyyMMdd.log，只保留最近 7 天。
// 日志只记结论和动作，不记 token 等敏感值（上游已打码）。
package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	mu  sync.Mutex
	dir string
	tag string
)

// Init 设置日志目录并清理 7 天前的日志文件（只清本工具自己的 *.log）。
func Init(appDir, who string) {
	mu.Lock()
	defer mu.Unlock()
	dir = filepath.Join(appDir, "日志")
	tag = who
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dir = ""
		return
	}
	cut := time.Now().AddDate(0, 0, -7)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") || len(e.Name()) != len("20060102.log") {
			continue
		}
		if t, err := time.ParseInLocation("20060102", strings.TrimSuffix(e.Name(), ".log"), time.Local); err == nil && t.Before(cut) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// Dir 返回日志目录（未初始化时为空）。
func Dir() string {
	mu.Lock()
	defer mu.Unlock()
	return dir
}

// Printf 追加一行日志；写失败静默忽略，绝不影响主流程。
func Printf(format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return
	}
	now := time.Now()
	f, err := os.OpenFile(filepath.Join(dir, now.Format("20060102")+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := strings.ReplaceAll(fmt.Sprintf(format, a...), "\n", " ⏎ ")
	fmt.Fprintf(f, "%s [%s] %s\n", now.Format("15:04:05.000"), tag, msg)
}
