//go:build !linux

package main

import (
	"os/exec"
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func setWMClass(uintptr, string, string) {}
func activateX11Window(uintptr) bool { return false }
func findChromeAppWindowsByTitle(string) map[uintptr]bool { return nil }
func findAllChromeAppWindows() map[uintptr]bool { return nil }
func isWindowValid(uintptr) bool { return false }
func extractHost(string) string { return "" }
func setChromeProcessAttr(*exec.Cmd) {}

func setWindowOpacity(wid uintptr, opacity uint32) {
	setWin32Opacity(wid, float64(opacity)/float64(0xFFFFFFFF))
}

func setLinuxWindowOpacity(opacity float64) {
	if opacity < 0.1 {
		opacity = 0.1
	}
	if opacity > 1.0 {
		opacity = 1.0
	}
	app := application.Get()
	w := app.Window.Current()
	if w == nil {
		return
	}
	nw := w.NativeWindow()
	if nw == nil {
		return
	}
	setWin32Opacity(uintptr(nw), opacity)
}

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procGetWindowLong   = user32.NewProc("GetWindowLongW")
	procSetWindowLong   = user32.NewProc("SetWindowLongW")
	procSetLayeredAttr  = user32.NewProc("SetLayeredWindowAttributes")
)

const (
	GWL_EXSTYLE     = ^uintptr(20) // -20
	WS_EX_LAYERED   = 0x00080000
	LWA_ALPHA       = 0x02
)

func setWin32Opacity(hwnd uintptr, opacity float64) {
	if opacity < 0.1 {
		opacity = 0.1
	}
	if opacity > 1.0 {
		opacity = 1.0
	}
	style, _, _ := procGetWindowLong.Call(hwnd, uintptr(GWL_EXSTYLE))
	procSetWindowLong.Call(hwnd, uintptr(GWL_EXSTYLE), style|WS_EX_LAYERED)
	alpha := byte(opacity * 255)
	procSetLayeredAttr.Call(hwnd, 0, uintptr(alpha), LWA_ALPHA)
}
