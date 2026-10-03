package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zemidala/modvault/core/fsx"
)

func entry(path, mod string) Entry {
	hash, size, _ := fsx.HashReader(strings.NewReader(path))
	return Entry{
		Path: path, ModID: mod, VersionID: "1.0", Hash: hash, Size: size,
		Method: MethodLink, Deployed: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
	}
}

func TestSetGetDelete(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 0 {
		t.Fatalf("новый манифест не пуст: %d", m.Len())
	}

	for _, e := range []Entry{
		entry("mods/Scoreboard/Scoreboard.mod", "scoreboard"),
		entry(`mods\Scoreboard\scripts\main.lua`, "scoreboard"),
		entry("mods/NumericUI/NumericUI.mod", "numeric_ui"),
		entry("binaries/Моды/файл.dll", "numeric_ui"),
	} {
		if err := m.Set(e); err != nil {
			t.Fatal(err)
		}
	}

	// Регистр букв и вид разделителя не важны.
	got, ok := m.Get(`MODS\scoreboard\SCRIPTS/Main.lua`)
	if !ok || got.ModID != "scoreboard" || got.Path != "mods/Scoreboard/scripts/main.lua" {
		t.Errorf("Get = %+v, %v", got, ok)
	}
	if _, ok := m.Get("mods/нет.lua"); ok {
		t.Error("найден файл, которого нет в манифесте")
	}

	// Тот же путь в другом регистре — тот же файл: запись заменяется.
	replaced := entry("MODS/scoreboard/scoreboard.MOD", "other")
	if err := m.Set(replaced); err != nil {
		t.Fatal(err)
	}
	if m.Len() != 4 {
		t.Errorf("записей %d, want 4", m.Len())
	}
	if got, _ := m.Get("mods/Scoreboard/Scoreboard.mod"); got.ModID != "other" {
		t.Errorf("владелец = %q, want other", got.ModID)
	}

	if files := m.ByMod("numeric_ui"); len(files) != 2 || files[0].Path != "binaries/Моды/файл.dll" {
		t.Errorf("ByMod = %+v", files)
	}

	m.Delete("BINARIES/моды/ФАЙЛ.dll")
	if m.Len() != 3 || len(m.ByMod("numeric_ui")) != 1 {
		t.Errorf("после Delete записей %d", m.Len())
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, _ := Load(path)
	want := []Entry{
		entry("mods/b.lua", "b"),
		entry("mods/a.lua", "a"),
		entry("binaries/x.dll", "a"),
	}
	want[2].Method = MethodCopy
	for _, e := range want {
		m.Set(e)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)

	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := back.Entries()
	if len(got) != 3 || got[0].Path != "binaries/x.dll" || got[1].Path != "mods/a.lua" || got[2].Path != "mods/b.lua" {
		t.Fatalf("после чтения: %+v", got)
	}
	for _, e := range want {
		g, ok := back.Get(e.Path)
		if !ok || g.Hash != e.Hash || g.Size != e.Size || g.Method != e.Method || !g.Deployed.Equal(e.Deployed) || g.VersionID != e.VersionID {
			t.Errorf("%s: %+v, want %+v", e.Path, g, e)
		}
	}

	// Повторная запись того же набора даёт тот же файл байт в байт.
	if err := back.Save(); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Error("манифест меняется при перезаписи без изменений")
	}
}

func TestRejectsUnsafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	m, _ := Load(path)

	for _, p := range []string{"", "../outside.dll", "/abs.dll", `C:\Windows\x.dll`, "mods/NUL", "a:b"} {
		if err := m.Set(entry(p, "mod")); !errors.Is(err, fsx.ErrUnsafePath) {
			t.Errorf("Set(%q): %v, want ErrUnsafePath", p, err)
		}
	}
	if err := m.Set(Entry{Path: "mods/a.lua"}); err == nil {
		t.Error("запись без мода принята")
	}
	if m.Len() != 0 {
		t.Errorf("недопустимые записи попали в манифест: %d", m.Len())
	}

	// Подложенный манифест с путём наружу не читается.
	os.WriteFile(path, []byte(`{"files":[{"path":"../../evil.dll","mod":"x"}]}`), 0o644)
	if _, err := Load(path); !errors.Is(err, fsx.ErrUnsafePath) {
		t.Errorf("манифест с путём наружу: %v, want ErrUnsafePath", err)
	}
	os.WriteFile(path, []byte(`{"files":[`), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("обрезанный манифест прочитан без ошибки")
	}
}
