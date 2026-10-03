package manager

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zemidala/modvault/game/darktide"
)

func issueTitles(s State) string {
	out := make([]string, len(s.Issues))
	for i, issue := range s.Issues {
		out[i] = issue.Title
	}
	return strings.Join(out, " | ")
}

var (
	bundleAnchor = []byte{0xa3, 0x3a, 0x4a, 0xa4, 0xaf, 0x26, 0xa6, 0x9b}
	patchMarker  = []byte("9ba626afa44a3aa3.patch_999")
	cleanBundle  = append(append(bytes.Repeat([]byte{1}, 64), bundleAnchor...), bytes.Repeat([]byte{2}, 64)...)
)

// newGame создаёт папку, похожую на чистый Darktide, и приложение,
// у которого вместо dtkit-patch подменный патчер.
func newGame(t *testing.T) (*Manager, string, string) {
	t.Helper()
	a, home := newApp(t)
	a.game = &darktide.Darktide{Patcher: func(_, dir string) error {
		f := filepath.Join(dir, "bundle_database.data")
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		return os.WriteFile(f, append(data, patchMarker...), 0o644)
	}}
	g := filepath.Join(t.TempDir(), "Warhammer 40,000 DARKTIDE")
	for rel, data := range map[string][]byte{
		"binaries/Darktide.exe":       []byte("exe"),
		"bundle/bundle_database.data": cleanBundle,
		"mods/mod_load_order.txt":     []byte("Flux\r\n"), // чей-то старый порядок загрузки
	} {
		p := filepath.Join(g, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, data, 0o644)
	}
	return a, home, g
}

func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			data, _ := os.ReadFile(p)
			out[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	return out
}

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

var (
	dmlArchive = map[string]string{
		"binaries/mod_loader":       "loader",
		"mods/base/base.mod":        "return {}",
		"mods/base/mod_manager.lua": "-- base",
		"mods/mod_load_order.txt":   "-- пример из архива DML",
		"tools/dtkit-patch.exe":     "patcher",
		"toggle_darktide_mods.bat":  "@echo off",
		"README.md":                 "DML",
	}
	dmfArchive = map[string]string{"Darktide Mod Framework/mods/dmf/dmf.mod": "return {}", "Darktide Mod Framework/mods/dmf/scripts/dmf.lua": "-- dmf"}
)

func TestDarktideDeploy(t *testing.T) {
	a, home, g := newGame(t)
	clean := snapshot(t, g)

	if _, err := a.addArchive(writeZip(t, "Darktide Mod Loader-19-25-3-1700000000.zip", dmlArchive)); err != nil {
		t.Fatal(err)
	}
	a.addArchive(writeZip(t, "Darktide Mod Framework-8-25-3-1700000001.zip", dmfArchive))
	a.addArchive(writeZip(t, "Scoreboard-22-1-4-0-1700000002.zip", map[string]string{"scoreboard-main/Scoreboard.mod": "return {}", "scoreboard-main/scripts/a.lua": "-- a"}))
	a.addArchive(writeZip(t, "Flux-30-1-0-1700000003.zip", map[string]string{"Flux/Flux.mod": "return {}"}))
	if _, err := a.SetEnabled("flux", false); err != nil {
		t.Fatal(err)
	}

	s, err := a.setGame(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(issueTitles(s), "не папка") || strings.Contains(issueTitles(s), "Mod Loader") {
		t.Errorf("замечания до развёртывания: %s", issueTitles(s))
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}

	got := snapshot(t, g)
	want := map[string]string{
		"mods/base/base.mod":             "return {}",
		"mods/dmf/dmf.mod":               "return {}",
		"mods/Scoreboard/Scoreboard.mod": "return {}",
		"mods/Scoreboard/scripts/a.lua":  "-- a",
		"tools/dtkit-patch.exe":          "patcher",
	}
	for path, content := range want {
		if got[path] != content {
			t.Errorf("%s = %q, want %q", path, got[path], content)
		}
	}
	if _, ok := got["mods/Flux/Flux.mod"]; ok {
		t.Error("выключенный мод развёрнут")
	}
	if _, ok := got["README.md"]; ok {
		t.Error("описание из архива DML легло в игру")
	}
	order := strings.Split(strings.TrimSpace(got["mods/mod_load_order.txt"]), "\r\n")
	if len(order) != 3 || !strings.HasPrefix(order[0], "-- Файл собран Modvault") || order[1] != "Scoreboard" || order[2] != "-- Flux" {
		t.Errorf("порядок загрузки:\n%s", got["mods/mod_load_order.txt"])
	}
	if !bytes.Contains([]byte(got["bundle/bundle_database.data"]), patchMarker) {
		t.Error("база бандлов не пропатчена")
	}

	s = state(t, NewAt(home))
	if findMod(t, s, "scoreboard").State != "Развёрнут" || s.PlanTitle != "" {
		t.Errorf("после перезапуска: %s, план %q", findMod(t, s, "scoreboard").State, s.PlanTitle)
	}

	// Выключить всё, включая DML и DMF: игра как до модов, с чужим порядком
	// загрузки и непропатченной базой.
	for _, id := range []string{"darktide_mod_loader", "darktide_mod_framework", "scoreboard"} {
		if _, err := a.SetEnabled(id, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, g); !sameTree(after, clean) {
		t.Errorf("после «выключить всё» игра отличается от исходной:\n%v\n---\n%v", after, clean)
	}
}

func TestRejectsUnknownLayout(t *testing.T) {
	a, _, _ := newGame(t)
	_, err := a.addArchive(writeZip(t, "непонятный.zip", map[string]string{"scripts/a.lua": "1"}))
	if err == nil || !strings.Contains(err.Error(), "не добавлен") {
		t.Fatalf("архив без .mod: %v", err)
	}
	if s := state(t, a); !s.Demo {
		t.Error("отклонённый архив остался в хранилище")
	}
}

func TestOtherManagerBlocksDeploy(t *testing.T) {
	a, _, g := newGame(t)
	os.WriteFile(filepath.Join(g, "vortex.deployment.json"), []byte("{}"), 0o644)
	a.addArchive(writeZip(t, "dml.zip", dmlArchive))
	a.setGame(g)
	before := snapshot(t, g)

	s := state(t, a)
	if !strings.Contains(issueTitles(s), "Игрой управляет Vortex") {
		t.Errorf("замечания: %s", issueTitles(s))
	}
	if _, err := a.Deploy(); err == nil || !strings.Contains(err.Error(), "Vortex") {
		t.Errorf("развёртывание поверх Vortex: %v", err)
	}
	if after := snapshot(t, g); !sameTree(after, before) {
		t.Error("отказ от развёртывания изменил игру")
	}
}

func TestMissingLoaderNotice(t *testing.T) {
	a, _, g := newGame(t)
	a.addArchive(writeZip(t, "Flux.zip", map[string]string{"Flux/Flux.mod": "return {}"}))
	s, err := a.setGame(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(issueTitles(s), "Не установлен Darktide Mod Loader") {
		t.Errorf("нет замечания о DML: %s", issueTitles(s))
	}
}

func TestNotDarktideFolder(t *testing.T) {
	a, _, _ := newGame(t)
	a.addArchive(writeZip(t, "Flux.zip", map[string]string{"Flux/Flux.mod": "return {}"}))
	s, err := a.setGame(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(issueTitles(s), "Похоже, это не папка Darktide") {
		t.Errorf("замечания: %s", issueTitles(s))
	}
}

func TestConflictAndDriftIssues(t *testing.T) {
	a, _, g := newGame(t)
	a.addArchive(writeZip(t, "dml.zip", dmlArchive))
	a.addArchive(writeZip(t, "Healthbars.zip", map[string]string{"mods/hb/hb.mod": "1", "bundle/shared.patch_001": "healthbars"}))
	a.addArchive(writeZip(t, "Numeric UI.zip", map[string]string{"mods/nu/nu.mod": "2", "bundle/shared.patch_001": "numeric"}))
	a.setGame(g)
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	s := state(t, a)
	if !strings.Contains(issueTitles(s), "Healthbars и Numeric UI меняют одни и те же файлы (1)") {
		t.Errorf("конфликт: %s", issueTitles(s))
	}
	if data, _ := os.ReadFile(filepath.Join(g, "bundle", "shared.patch_001")); string(data) != "numeric" {
		t.Errorf("победил не нижний мод: %q", data)
	}

	target := filepath.Join(g, "mods", "nu", "nu.mod")
	os.Remove(target)
	os.WriteFile(target, []byte("правка"), 0o644)
	s = state(t, a)
	if !strings.Contains(issueTitles(s), "1 файл мода изменён вне программы") || s.Status[2].Level != LevelError {
		t.Errorf("расхождение: %s, %+v", issueTitles(s), s.Status[2])
	}
	res, err := a.Deploy()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Message, "сохранены в") {
		t.Errorf("сообщение не говорит, куда ушла правка: %q", res.Message)
	}
}

func TestMissingGameDir(t *testing.T) {
	a, home, g := newGame(t)
	if _, err := a.setGame(g); err != nil {
		t.Fatal(err)
	}
	a.addArchive(writeZip(t, "m.zip", map[string]string{"m/m.mod": "1"}))
	os.RemoveAll(g)

	s := state(t, NewAt(home))
	if !strings.Contains(issueTitles(s), "Развёртывание недоступно") || s.Status[0].Level != LevelError {
		t.Errorf("пропавшая папка игры: %s, %+v", issueTitles(s), s.Status[0])
	}
}
