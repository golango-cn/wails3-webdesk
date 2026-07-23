//go:build !linux

package main

import (
	"fmt"
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
		fmt.Println("[WebDesk] SetOpacity: no current window")
		return
	}
	nw := w.NativeWindow()
	if nw == nil {
		fmt.Println("[WebDesk] SetOpacity: no native window")
		return
	}
	hwnd := uintptr(nw)
	// Walk up to find the top-level owner window
	owner := getAncestor(hwnd)
	if owner != 0 {
		hwnd = owner
	}
	fmt.Printf("[WebDesk] SetOpacity: hwnd=%x opacity=%.2f\n", hwnd, opacity)
	setWin32Opacity(hwnd, opacity)
}

var (
	user32             = syscall.NewLazyDLL("user32.dll")
	procGetWindowLong  = user32.NewProc("GetWindowLongW")
	procSetWindowLong  = user32.NewProc("SetWindowLongW")
	procSetLayeredAttr = user32.NewProc("SetLayeredWindowAttributes")
	procGetAncestor    = user32.NewProc("GetAncestor")
)

const (
	GWL_EXSTYLE    = ^uintptr(19) // -20 (Win32 GWL_EXSTYLE)
	WS_EX_LAYERED  = 0x00080000
	WS_EX_TOPMOST  = 0x00000008
	LWA_ALPHA      = 0x02
	GA_ROOT        = 2 // GetAncestor flag: get root owner
)

// getAncestor walks up the window hierarchy to find the top-level window
func getAncestor(hwnd uintptr) uintptr {
	ret, _, _ := procGetAncestor.Call(hwnd, GA_ROOT)
	return ret
}

func setWin32Opacity(hwnd uintptr, opacity float64) {
	if opacity < 0.1 {
		opacity = 0.1
	}
	if opacity > 1.0 {
		opacity = 1.0
	}
	// Add WS_EX_LAYERED style to the window
	style, _, err := procGetWindowLong.Call(hwnd, uintptr(GWL_EXSTYLE))
	if style == 0 && err != nil && err.Error() != "" {
		fmt.Printf("[WebDesk] GetWindowLong failed: %v\n", err)
		return
	}
	newStyle := style | WS_EX_LAYERED
	procSetWindowLong.Call(hwnd, uintptr(GWL_EXSTYLE), newStyle)
	alpha := byte(opacity * 255)
	ret, _, err := procSetLayeredAttr.Call(hwnd, 0, uintptr(alpha), LWA_ALPHA)
	if ret == 0 {
		fmt.Printf("[WebDesk] SetLayeredWindowAttributes failed: %v\n", err)
	}
}
