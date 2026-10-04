package fsx

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/zemidala/modvault/i18n"
)

// LinkDir создаёт ссылку на папку: в Windows — junction, ей не нужны права
// администратора и режим разработчика.
func LinkDir(target, link string) error {
	if fi, err := os.Stat(target); err != nil {
		return err
	} else if !fi.IsDir() {
		return i18n.Errorf("%s — не папка", target)
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mklink %s: %v: %s", link, err, strings.TrimSpace(string(out)))
	}
	return nil
}
