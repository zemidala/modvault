//go:build !windows

package darktide

import (
	"syscall"

	"github.com/zemidala/modvault/i18n"
)

func steamRoot() string  { return "" }
func drives() []string   { return nil }
func steamRunning() bool { return false }
func openURL(string) error {
	return i18n.NewError("запуск поддерживается только в Windows")
}

func hiddenWindow() *syscall.SysProcAttr { return nil }
