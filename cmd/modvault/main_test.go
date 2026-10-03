package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zemidala/modvault/game/darktide"
	"github.com/zemidala/modvault/manager"
)

var (
	anchor      = []byte{0xa3, 0x3a, 0x4a, 0xa4, 0xaf, 0x26, 0xa6, 0x9b}
	cleanBundle = append(append(bytes.Repeat([]byte{1}, 32), anchor...), bytes.Repeat([]byte{2}, 32)...)
)

// env — пробная игра и папка данных Modvault для команд.
type env struct {
	t          *testing.T
	home, game string
}

func newEnv(t *testing.T) *env {
	base := t.TempDir()
	e := &env{t: t, home: filepath.Join(base, "Modvault"), game: filepath.Join(base, "Darktide")}
	for rel, data := range map[string][]byte{
		"binaries/Darktide.exe":       []byte("exe"),
		"bundle/bundle_database.data": cleanBundle,
	} {
		p := filepath.Join(e.game, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o644)
	}
	return e
}

func (e *env) open() *manager.Manager {
	return manager.NewWith(e.home, &darktide.Darktide{Patcher: func(_, dir string) error {
		f := filepath.Join(dir, "bundle_database.data")
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		return os.WriteFile(f, append(data, "9ba626afa44a3aa3.patch_999"...), 0o644)
	}})
}

// run выполняет команду в новом процессе программы — как при настоящем запуске.
func (e *env) run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, e.open)
	return code, stdout.String(), stderr.String()
}

func (e *env) ok(args ...string) string {
	e.t.Helper()
	code, out, errOut := e.run(args...)
	if code != exitOK {
		e.t.Fatalf("modvault %s: код %d\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func (e *env) zip(name string, files map[string]string) string {
	path := filepath.Join(e.t.TempDir(), name)
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	for entry, content := range files {
		x, _ := w.Create(entry)
		x.Write([]byte(content))
	}
	w.Close()
	f.Close()
	return path
}

func TestHelpAndVersion(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		args       []string
		code       int
		out, error string
	}{
		{nil, exitOK, "Использование:", ""},
		{[]string{"help"}, exitOK, "Профили:", ""},
		{[]string{"version"}, exitOK, "modvault ", ""},
		{[]string{"nope"}, exitUsage, "", "неизвестная команда: nope"},
		{[]string{"add"}, exitUsage, "", "использование: modvault add"},
		{[]string{"move", "x"}, exitUsage, "", "использование: modvault move"},
		{[]string{"deploy", "--bogus"}, exitUsage, "", "flag provided but not defined"},
		{[]string{"list"}, exitError, "", "игра не выбрана"},
		{[]string{"help"}, exitOK, "nexus get <ссылка nxm>", ""},
		{[]string{"help"}, exitOK, "profile switch <имя>", ""},
		{[]string{"profile", "new"}, exitUsage, "", "использование: modvault profile new"},
		{[]string{"profile", "switch"}, exitUsage, "", "использование: modvault profile switch"},
		{[]string{"profile", "switch", "Нет такого"}, exitError, "", "набор «Нет такого» не найден"},
		{[]string{"nexus"}, exitOK, "ключ Nexus не задан", ""},
		{[]string{"nexus", "get"}, exitUsage, "", "использование: modvault nexus get"},
		{[]string{"nexus", "nope"}, exitUsage, "", "неизвестная команда Nexus: nope"},
		{[]string{"nexus", "get", "https://example.com"}, exitError, "", "ссылка nxm не разобрана"},
		{[]string{"nexus", "get", "nxm://warhammer40kdarktide/mods/1/files/2"}, exitError, "", "ключ Nexus не задан"},
		{[]string{"nexus", "check"}, exitError, "", "ключ Nexus не задан"},
		{[]string{"nexus", "logout"}, exitOK, "ключ Nexus забыт", ""},
	}
	for _, tt := range tests {
		code, out, errOut := e.run(tt.args...)
		if code != tt.code || !strings.Contains(out, tt.out) || !strings.Contains(errOut, tt.error) {
			t.Errorf("modvault %v: код %d\nout: %s\nerr: %s", tt.args, code, out, errOut)
		}
	}
}

func TestWorkflow(t *testing.T) {
	e := newEnv(t)
	clean, _ := os.ReadFile(filepath.Join(e.game, "bundle", "bundle_database.data"))

	if out := e.ok("game", e.game); !strings.Contains(out, e.game) {
		t.Errorf("game: %s", out)
	}
	e.ok("add",
		e.zip("Darktide Mod Loader-19-25-3-1700000000.zip", map[string]string{"mods/base/base.mod": "return {}", "binaries/mod_loader": "l", "tools/dtkit-patch.exe": "p"}),
		e.zip("Darktide Mod Framework-8-25-3-1700000001.zip", map[string]string{"mods/dmf/dmf.mod": "return {}"}),
		e.zip("Scoreboard-22-1-4-0-1700000002.zip", map[string]string{"Scoreboard/Scoreboard.mod": "return {}"}),
		e.zip("Flux-30-1-0-1700000003.zip", map[string]string{"Flux/Flux.mod": "return {}"}),
	)
	if code, _, errOut := e.run("add", e.zip("плохой.zip", map[string]string{"a.lua": "1"})); code != exitError || !strings.Contains(errOut, "не добавлен") {
		t.Errorf("плохой архив: %d %s", code, errOut)
	}

	list := e.ok("list")
	for _, want := range []string{"Scoreboard", "1.4.0", "Ждёт развёртывания", "flux"} {
		if !strings.Contains(list, want) {
			t.Errorf("list без %q:\n%s", want, list)
		}
	}

	e.ok("move", "Flux", "3")
	e.ok("disable", "scoreboard")
	if !strings.Contains(e.ok("list"), "  3 [x] Flux") {
		t.Errorf("перестановка не видна:\n%s", e.ok("list"))
	}

	// Пробный прогон ничего не меняет.
	dry := e.ok("deploy", "--dry-run")
	if !strings.Contains(dry, "+ mods/Flux/Flux.mod") || !strings.Contains(dry, "--dry-run") {
		t.Errorf("deploy --dry-run:\n%s", dry)
	}
	if _, err := os.Stat(filepath.Join(e.game, "mods")); err == nil {
		t.Fatal("--dry-run изменил игру")
	}

	if out := e.ok("deploy"); !strings.Contains(out, "Развёрнуто") {
		t.Errorf("deploy: %s", out)
	}
	order, _ := os.ReadFile(filepath.Join(e.game, "mods", "mod_load_order.txt"))
	if !strings.Contains(string(order), "Flux\r\n-- Scoreboard") {
		t.Errorf("порядок загрузки:\n%s", order)
	}
	if out := e.ok("verify"); !strings.Contains(out, "Проблем не найдено") {
		t.Errorf("verify: %s", out)
	}
	if out := e.ok("status"); !strings.Contains(out, "совпадают с профилем") {
		t.Errorf("status:\n%s", out)
	}

	// Второе развёртывание, затем откат к первому.
	e.ok("enable", "Scoreboard")
	e.ok("deploy")
	if _, err := os.Stat(filepath.Join(e.game, "mods", "Scoreboard", "Scoreboard.mod")); err != nil {
		t.Fatal("Scoreboard не развёрнут")
	}
	e.ok("rollback")
	if _, err := os.Stat(filepath.Join(e.game, "mods", "Scoreboard", "Scoreboard.mod")); err == nil {
		t.Error("откат не убрал Scoreboard")
	}

	// Порча файла мода видна в verify.
	target := filepath.Join(e.game, "mods", "Flux", "Flux.mod")
	os.Remove(target)
	os.WriteFile(target, []byte("правка"), 0o644)
	if code, out, _ := e.run("verify"); code != exitError || !strings.Contains(out, "изменён вне программы") {
		t.Errorf("verify после порчи: %d\n%s", code, out)
	}
	e.ok("deploy")

	// Профили: пустой профиль снимает всё, и игра становится как была.
	e.ok("profile", "copy", "Основной", "Запасной")
	e.ok("profile", "use", "Пустой")
	if out := e.ok("profile"); !strings.Contains(out, "* Пустой") || !strings.Contains(out, "Запасной") {
		t.Errorf("profile list:\n%s", out)
	}
	if code, _, _ := e.run("profile", "delete", "Пустой"); code != exitError {
		t.Error("текущий профиль удалился")
	}
	e.ok("deploy")
	if _, err := os.Stat(filepath.Join(e.game, "mods")); err == nil {
		t.Error("после снятия всего в игре осталась папка mods")
	}
	if now, _ := os.ReadFile(filepath.Join(e.game, "bundle", "bundle_database.data")); !bytes.Equal(now, clean) {
		t.Error("база бандлов не вернулась к исходной")
	}

	e.ok("profile", "use", "Основной")
	e.ok("remove", "Flux", "--permanent")
	if strings.Contains(e.ok("list"), "Flux") {
		t.Error("Flux остался в списке после remove")
	}
	if code, _, errOut := e.run("enable", "нет-такого"); code != exitError || !strings.Contains(errOut, "не найден") {
		t.Errorf("неизвестный мод: %d %s", code, errOut)
	}
	if code, _, errOut := e.run("adopt", "--dry-run"); code != exitError || !strings.Contains(errOut, "Vortex") {
		t.Errorf("adopt без Vortex: %d %s", code, errOut)
	}
}

func TestSortAndCheck(t *testing.T) {
	e := newEnv(t)
	e.ok("game", e.game)
	e.ok("add",
		e.zip("dml.zip", map[string]string{"mods/base/base.mod": "return {}", "mods/base/mod_manager.lua": "-- DML", "binaries/mod_loader": "l", "tools/dtkit-patch.exe": "p"}),
		e.zip("Addon.zip", map[string]string{"Addon/Addon.mod": `return { load_after = { "Core" }, require = { "Ghost" } }`}),
		e.zip("Core.zip", map[string]string{"Core/Core.mod": "return {}"}),
	)

	check := e.ok("check")
	for _, want := range []string{"нарушает 1 правило", "нужен мод Ghost"} {
		if !strings.Contains(check, want) {
			t.Errorf("check без %q:\n%s", want, check)
		}
	}

	dry := e.ok("sort", "--dry-run")
	if !strings.Contains(dry, "Core: с 3 на 2") || !strings.Contains(dry, "--dry-run") {
		t.Errorf("sort --dry-run:\n%s", dry)
	}
	if !strings.Contains(e.ok("list"), "  2 [x] Addon") {
		t.Error("--dry-run изменил порядок")
	}

	e.ok("sort")
	if !strings.Contains(e.ok("list"), "  2 [x] Core") {
		t.Errorf("после sort:\n%s", e.ok("list"))
	}
	if out := e.ok("sort"); !strings.Contains(out, "Передвигать нечего") {
		t.Errorf("повторный sort: %s", out)
	}
	if strings.Contains(e.ok("check"), "нарушает") {
		t.Error("после sort нарушение осталось")
	}
}
