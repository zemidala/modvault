//go:build !windows

package darktide

import (
	"errors"
	"syscall"
)

func steamRoot() string { return "" }
func drives() []string  { return nil }
func openURL(string) error {
	return errors.New("запуск поддерживается только в Windows")
}

func hiddenWindow() *syscall.SysProcAttr { return nil }
