//go:build !linux

package main

func setWMClass(uintptr, string, string) {}
func activateX11Window(uintptr) bool { return false }
func findChromeAppWindowsByTitle(string) map[uintptr]bool { return nil }
func findAllChromeAppWindows() map[uintptr]bool { return nil }
func isWindowValid(uintptr) bool { return false }
func extractHost(string) string { return "" }
