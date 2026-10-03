package deploy

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/zemidala/modvault/core/fsx"
)

// Запущенная игра держит файлы мода открытыми: снять мод нельзя, и игра
// должна остаться как была.
func TestBusyGameFile(t *testing.T) {
	e := newEnv(t, foreignFiles)
	a := mod(t, e.store, "a", "1", map[string]string{"mods/a/a.mod": "A", "mods/base.lua": "A база", "mods/a/z.lua": "z"})
	e.deploy(t, a)
	before := snapshot(t, e.game)

	p, err := windows.UTF16PtrFromString(filepath.Join(e.game, "mods", "a", "z.lua"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := e.d.Plan(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.d.Apply(plan)
	if !errors.Is(err, fsx.ErrBusy) || !strings.Contains(err.Error(), "прежнее состояние") {
		t.Errorf("снятие занятого мода: %v", err)
	}
	if got := snapshot(t, e.game); got != before {
		t.Errorf("неудачное снятие изменило игру:\n%s\n---\n%s", got, before)
	}

	windows.CloseHandle(h)
	e.deploy(t)
}
