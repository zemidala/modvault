package ui

import "golang.org/x/sys/windows"

var monitorFromPoint = windows.NewLazySystemDLL("user32.dll").NewProc("MonitorFromPoint")

// onScreen сообщает, что точка лежит на каком-нибудь из подключённых экранов.
func onScreen(x, y int) bool {
	// POINT передаётся по значению: два 32-битных числа в одном аргументе.
	point := uintptr(uint32(int32(x))) | uintptr(uint32(int32(y)))<<32
	const monitorDefaultToNull = 0
	monitor, _, _ := monitorFromPoint.Call(point, monitorDefaultToNull)
	return monitor != 0
}
