//go:build !windows

package darktide

import "syscall"

func steamRoot() string  { return "" }
func drives() []string   { return nil }
func steamRunning() bool { return false }

func hiddenWindow() *syscall.SysProcAttr { return nil }
