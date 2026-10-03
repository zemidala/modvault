//go:build !windows

package ui

func onScreen(x, y int) bool { return x >= 0 && y >= 0 }
