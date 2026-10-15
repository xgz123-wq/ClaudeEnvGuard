package main

import (
	"unsafe"

	xwin "golang.org/x/sys/windows"
)

// fitWindow 把默认窗口尺寸压到主屏工作区（去掉任务栏）以内。
// 1920×1080 + 125% 缩放时工作区只有 1536×816，固定 860 高会把标题栏顶出屏幕。
// 返回的是逻辑像素（Wails 的单位）；读不到屏幕信息时原样返回。
func fitWindow(w, h, minW, minH int) (int, int, int, int) {
	user32 := xwin.NewLazySystemDLL("user32.dll")
	shcore := xwin.NewLazySystemDLL("shcore.dll")
	monitorFromPoint := user32.NewProc("MonitorFromPoint")
	getMonitorInfo := user32.NewProc("GetMonitorInfoW")
	getDpiForMonitor := shcore.NewProc("GetDpiForMonitor")

	const monitorDefaultToPrimary = 1
	mon, _, _ := monitorFromPoint.Call(0, monitorDefaultToPrimary)
	if mon == 0 {
		return w, h, minW, minH
	}
	var mi struct {
		Size    uint32
		Monitor xwin.Rect
		Work    xwin.Rect
		Flags   uint32
	}
	mi.Size = uint32(unsafe.Sizeof(mi))
	if r, _, _ := getMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return w, h, minW, minH
	}
	dpiX, dpiY := uint32(96), uint32(96)
	if getDpiForMonitor.Find() == nil {
		const mdtEffectiveDPI = 0
		if r, _, _ := getDpiForMonitor.Call(mon, mdtEffectiveDPI, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); r != 0 || dpiX == 0 {
			dpiX, dpiY = 96, 96
		}
	}
	workW := int(mi.Work.Right-mi.Work.Left) * 96 / int(dpiX)
	workH := int(mi.Work.Bottom-mi.Work.Top) * 96 / int(dpiY)
	if workW <= 0 || workH <= 0 {
		return w, h, minW, minH
	}
	// 四周各留一点空，窗口居中后标题栏和底边都在屏幕内
	w, h = min(w, workW-40), min(h, workH-24)
	minW, minH = min(minW, w), min(minH, h)
	return w, h, minW, minH
}
