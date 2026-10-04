package store

import (
	"archive/zip"
	"bufio"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zemidala/modvault/core/fsx"
)

func writeZip(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for entry, content := range files {
		e, err := w.CreateHeader(&zip.FileHeader{Name: entry, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func open(t *testing.T) (*Store, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Хранилище")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

// tree перечисляет все файлы и папки под dir.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != dir {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var modFiles = map[string]string{
	"Scoreboard/Scoreboard.mod":         "return {}\n",
	"Scoreboard/scripts/mods/main.lua":  "print('привет')\n",
	"Scoreboard/scripts/mods/Тест.lua":  "-- кириллица\n",
	"Scoreboard/localization/empty.lua": "",
}

func TestAddAndList(t *testing.T) {
	s, _ := open(t)
	archivePath := writeZip(t, "Scoreboard-22-1-4-0-1700000000.zip", modFiles)

	v, err := s.Add(archivePath, GuessInfo(archivePath))
	if err != nil {
		t.Fatal(err)
	}
	if v.ModID != "scoreboard" || v.ID != "1.4.0" || v.Name != "Scoreboard" || v.Source != "Nexus Mods" {
		t.Errorf("версия = %+v", v)
	}

	// Список файлов и хеши совпадают с тем, что было в архиве.
	if len(v.Files) != len(modFiles) {
		t.Fatalf("файлов %d, want %d", len(v.Files), len(modFiles))
	}
	for _, f := range v.Files {
		content, ok := modFiles[f.Path]
		if !ok {
			t.Errorf("лишний файл %q", f.Path)
			continue
		}
		want, _, _ := fsx.HashReader(strings.NewReader(content))
		if f.Hash != want || f.Size != int64(len(content)) {
			t.Errorf("%s: хеш или размер не совпадают с архивом", f.Path)
		}
		onDisk, err := os.ReadFile(filepath.Join(s.FilesDir(v.ModID, v.ID), filepath.FromSlash(f.Path)))
		if err != nil || string(onDisk) != content {
			t.Errorf("%s на диске: %q, %v", f.Path, onDisk, err)
		}
	}

	// Исходный архив сохранён без изменений.
	stored, _, err := fsx.HashFile(filepath.Join(s.versionDir(v.ModID, v.ID), v.Archive))
	if err != nil {
		t.Fatal(err)
	}
	if stored != v.ArchiveHash {
		t.Error("сохранённый архив отличается от исходного")
	}

	got, err := s.Get(v.ModID, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveHash != v.ArchiveHash || len(got.Files) != len(v.Files) || !got.Added.Equal(v.Added) {
		t.Errorf("Get вернул другое: %+v", got)
	}

	if problems, err := s.Verify(v.ModID, v.ID); err != nil || len(problems) != 0 {
		t.Errorf("Verify свежей версии: %v, %v", problems, err)
	}

	if _, err := s.Add(archivePath, GuessInfo(archivePath)); !errors.Is(err, fs.ErrExist) {
		t.Errorf("повторное добавление: %v, want fs.ErrExist", err)
	}
}

func TestVersionsSideBySide(t *testing.T) {
	s, _ := open(t)
	old := writeZip(t, "old.zip", map[string]string{"m/a.lua": "1"})
	newer := writeZip(t, "new.zip", map[string]string{"m/a.lua": "2", "m/b.lua": "3"})

	v1, err := s.Add(old, Info{Name: "Мой мод", Version: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // время добавления хранится с точностью до секунды
	v2, err := s.Add(newer, Info{Name: "Мой мод", Version: "2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if v1.ModID != "мой_мод" || v1.ModID != v2.ModID {
		t.Fatalf("идентификаторы: %q и %q", v1.ModID, v2.ModID)
	}

	mods, problems, err := s.List()
	if err != nil || len(problems) != 0 {
		t.Fatal(err, problems)
	}
	if len(mods) != 1 || len(mods[0].Versions) != 2 {
		t.Fatalf("List = %+v", mods)
	}
	if mods[0].Latest().ID != "2.0" {
		t.Errorf("последняя версия = %q", mods[0].Latest().ID)
	}

	// Удаление одной версии не трогает другую.
	if err := s.Remove(v1.ModID, v1.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(v1.ModID, v1.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("удалённая версия читается: %v", err)
	}
	if problems, err := s.Verify(v2.ModID, v2.ID); err != nil || len(problems) != 0 {
		t.Errorf("оставшаяся версия повреждена: %v, %v", problems, err)
	}
}

func TestUnknownVersion(t *testing.T) {
	s, _ := open(t)
	a := writeZip(t, "мод.zip", map[string]string{"m/a.lua": "1"})
	b := writeZip(t, "мод.zip", map[string]string{"m/a.lua": "2"})

	va, err := s.Add(a, GuessInfo(a))
	if err != nil {
		t.Fatal(err)
	}
	vb, err := s.Add(b, GuessInfo(b))
	if err != nil {
		t.Fatal(err)
	}
	if va.ModID != "мод" || va.ModID != vb.ModID || va.ID == vb.ID || len(va.ID) != 12 {
		t.Errorf("версии без номера: %s/%s и %s/%s", va.ModID, va.ID, vb.ModID, vb.ID)
	}
	if _, err := s.Add(a, GuessInfo(a)); !errors.Is(err, fs.ErrExist) {
		t.Errorf("тот же архив второй раз: %v, want fs.ErrExist", err)
	}
}

func TestRemoveLeavesNothing(t *testing.T) {
	s, root := open(t)
	archivePath := writeZip(t, "mod.zip", modFiles)
	v, err := s.Add(archivePath, Info{Name: "Scoreboard", Version: "1.4.0"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveMod(v.ModID, true); err != nil {
		t.Fatal(err)
	}
	if left := tree(t, filepath.Join(root, modsDir)); len(left) != 0 {
		t.Errorf("после удаления в mods осталось: %v", left)
	}
	if left := tree(t, filepath.Join(root, tmpDir)); len(left) != 0 {
		t.Errorf("после удаления в tmp осталось: %v", left)
	}
	if mods, _, _ := s.List(); len(mods) != 0 {
		t.Errorf("List после удаления: %+v", mods)
	}
	if err := s.Remove(v.ModID, v.ID, true); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("повторное удаление: %v, want fs.ErrNotExist", err)
	}
}

func TestRemoveToTrash(t *testing.T) {
	s, root := open(t)
	archivePath := writeZip(t, "mod.zip", map[string]string{"m/a.lua": "1"})
	v, err := s.Add(archivePath, Info{Name: "modvault-тест-корзины", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}

	err = s.Remove(v.ModID, v.ID, false)
	if errors.Is(err, fsx.ErrTrashUnavailable) {
		if _, gerr := s.Get(v.ModID, v.ID); gerr != nil {
			t.Fatalf("Корзина недоступна, но версия пропала: %v", gerr)
		}
		t.Skipf("Корзина здесь недоступна: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if left := tree(t, filepath.Join(root, modsDir)); len(left) != 0 {
		t.Errorf("после удаления в Корзину осталось: %v", left)
	}
}

func TestRejectedArchiveLeavesNothing(t *testing.T) {
	s, root := open(t)
	bad := []string{
		writeZip(t, "evil.zip", map[string]string{"ok.lua": "1", "../evil.lua": "2"}),
		writeZip(t, "device.zip", map[string]string{"m/NUL": "1"}),
		writeZip(t, "empty.zip", nil),
	}
	notArchive := filepath.Join(t.TempDir(), "текст.zip")
	if err := os.WriteFile(notArchive, []byte("не архив"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad = append(bad, notArchive)

	for _, path := range bad {
		if _, err := s.Add(path, Info{Name: "Плохой мод", Version: "1"}); err == nil {
			t.Errorf("%s добавлен без ошибки", filepath.Base(path))
		}
	}
	for _, dir := range []string{modsDir, tmpDir} {
		if left := tree(t, filepath.Join(root, dir)); len(left) != 0 {
			t.Errorf("после отклонённых архивов в %s осталось: %v", dir, left)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "evil.lua")); err == nil {
		t.Error("файл записан за пределы папки версии")
	}
}

func TestInvalidIDs(t *testing.T) {
	s, _ := open(t)
	archivePath := writeZip(t, "mod.zip", map[string]string{"m/a.lua": "1"})

	for _, name := range []string{"", "???", "NUL", ".."} {
		if _, err := s.Add(archivePath, Info{Name: name}); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Add с названием %q: %v, want ErrInvalidID", name, err)
		}
	}
	for _, id := range []string{"", "..", "a/b", `a\b`, ".hidden", "NUL"} {
		if _, err := s.Get(id, "1"); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Get(%q): %v, want ErrInvalidID", id, err)
		}
		if err := s.Remove("mod", id, true); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Remove версии %q: %v, want ErrInvalidID", id, err)
		}
		if err := s.RemoveMod(id, true); !errors.Is(err, ErrInvalidID) {
			t.Errorf("RemoveMod(%q): %v, want ErrInvalidID", id, err)
		}
	}
}

func TestVerifyFindsDamage(t *testing.T) {
	s, _ := open(t)
	archivePath := writeZip(t, "mod.zip", map[string]string{"m/a.lua": "1", "m/b.lua": "2", "m/c.lua": "3"})
	v, err := s.Add(archivePath, Info{Name: "mod", Version: "1"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.FilesDir(v.ModID, v.ID), "m")
	os.Remove(filepath.Join(dir, "a.lua"))
	os.WriteFile(filepath.Join(dir, "b.lua"), []byte("испорчен"), 0o644)
	os.WriteFile(filepath.Join(dir, "d.lua"), []byte("чужой"), 0o644)

	problems, err := s.Verify(v.ModID, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range problems {
		got[p.Path] = p.Kind
	}
	want := map[string]string{"m/a.lua": "пропал", "m/b.lua": "изменён", "m/d.lua": "лишний"}
	if len(got) != len(want) {
		t.Fatalf("проблемы = %v, want %v", got, want)
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Errorf("%s: %q, want %q", path, got[path], kind)
		}
	}
}

func TestListSkipsBroken(t *testing.T) {
	s, root := open(t)
	archivePath := writeZip(t, "mod.zip", map[string]string{"m/a.lua": "1"})
	if _, err := s.Add(archivePath, Info{Name: "good", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, modsDir, "bad", "1")
	os.MkdirAll(broken, 0o755)
	os.WriteFile(filepath.Join(broken, metaFile), []byte("{не json"), 0o644)

	mods, problems, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].ID != "good" {
		t.Errorf("List = %+v", mods)
	}
	if len(problems) != 1 {
		t.Errorf("проблем %d, want 1: %v", len(problems), problems)
	}
}

func TestGuessInfo(t *testing.T) {
	tests := []struct {
		file          string
		name, version string
	}{
		{"Scoreboard-22-1-4-0-1700000000.zip", "Scoreboard", "1.4.0"},
		{"Numeric UI-12-2-1-1699999999.7z", "Numeric UI", "2.1"},
		{"true_level-99-1-6-3b-1700000001.rar", "true level", "1.6.3b"},
		{"Custom-HUD-7-0-9-1700000002.zip", "Custom-HUD", "0.9"},
		{"мой мод.zip", "мой мод", ""},
		{"mod-v2.zip", "mod-v2", ""},
	}
	for _, tt := range tests {
		got := GuessInfo(filepath.Join("C:", "Загрузки", tt.file))
		if got.Name != tt.name || got.Version != tt.version {
			t.Errorf("GuessInfo(%q) = %q, %q; want %q, %q", tt.file, got.Name, got.Version, tt.name, tt.version)
		}
	}
}

func TestSlug(t *testing.T) {
	tests := map[string]string{
		"Numeric UI":             "numeric_ui",
		"Darktide Mod Framework": "darktide_mod_framework",
		"  Мой   мод!  ":         "мой_мод",
		"true_level":             "true_level",
		"v1.4.0":                 "v1.4.0",
		"a/b\\c":                 "a_b_c",
		"...":                    "",
		"???":                    "",
	}
	for in, want := range tests {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// Обрыв посреди добавления: вспомогательный процесс без остановки добавляет
// версии, его убивают. Хранилище обязано открыться, а всё, что в нём видно,
// должно быть целым.
const (
	crashEnv        = "MODVAULT_STORE_CRASH_ROOT"
	crashArchiveEnv = "MODVAULT_STORE_CRASH_ARCHIVE"
)

func TestCrashHelper(t *testing.T) {
	root := os.Getenv(crashEnv)
	if root == "" {
		t.Skip("вспомогательный процесс для TestAddSurvivesKill")
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := os.Getenv(crashArchiveEnv)
	for i := 0; ; i++ {
		if _, err := s.Add(archivePath, Info{Name: "mod", Version: time.Now().Format("150405.000000000")}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			os.Stdout.WriteString("ready\n")
		}
	}
}

func TestAddSurvivesKill(t *testing.T) {
	if testing.Short() {
		t.Skip("запускает и убивает процессы")
	}
	root := filepath.Join(t.TempDir(), "store")
	files := map[string]string{}
	for i := 0; i < 40; i++ {
		files["m/"+strings.Repeat("f", i+1)+".lua"] = strings.Repeat("данные ", 2000)
	}
	archivePath := writeZip(t, "mod.zip", files)

	for round := 0; round < 5; round++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
		cmd.Env = append(os.Environ(), crashEnv+"="+root, crashArchiveEnv+"="+archivePath)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		ready := false
		for sc := bufio.NewScanner(stdout); sc.Scan(); {
			if strings.Contains(sc.Text(), "ready") {
				ready = true
				break
			}
		}
		if !ready {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal("вспомогательный процесс не начал работу")
		}
		time.Sleep(time.Duration(20+round*37) * time.Millisecond)
		cmd.Process.Kill()
		cmd.Wait()

		s, err := Open(root)
		if err != nil {
			t.Fatalf("раунд %d: хранилище не открылось: %v", round, err)
		}
		if left := tree(t, filepath.Join(root, tmpDir)); len(left) != 0 {
			t.Fatalf("раунд %d: Open не убрал остатки: %v", round, left)
		}
		mods, problems, err := s.List()
		if err != nil || len(problems) != 0 {
			t.Fatalf("раунд %d: List: %v, %v", round, err, problems)
		}
		if len(mods) != 1 {
			t.Fatalf("раунд %d: модов %d", round, len(mods))
		}
		for _, v := range mods[0].Versions {
			bad, err := s.Verify(v.ModID, v.ID)
			if err != nil || len(bad) != 0 {
				t.Fatalf("раунд %d: версия %s повреждена: %v, %v", round, v.ID, bad, err)
			}
			if len(v.Files) != 40 {
				t.Fatalf("раунд %d: в версии %s файлов %d", round, v.ID, len(v.Files))
			}
		}
	}
}

func TestAddFiles(t *testing.T) {
	s, _ := open(t)
	src := t.TempDir()
	files := map[string]string{"mods/afk/afk.mod": "return {}", "mods/afk/scripts/afk.lua": "-- afk"}
	for p, c := range files {
		full := filepath.Join(src, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	info := Info{Name: "AFK", Version: "23.4.05", Source: "Vortex", NexusID: 33, AsIs: true}

	v, err := s.AddFiles(src, []string{"mods/afk/scripts/afk.lua", "mods/afk/afk.mod"}, info)
	if err != nil {
		t.Fatal(err)
	}
	if v.ModID != "afk" || v.ID != "23.4.05" || v.Archive != "" || !v.AsIs || v.NexusID != 33 || len(v.Files) != 2 {
		t.Errorf("версия = %+v", v)
	}
	if problems, err := s.Verify(v.ModID, v.ID); err != nil || len(problems) != 0 {
		t.Errorf("Verify: %v, %v", problems, err)
	}
	got, err := s.Get(v.ModID, v.ID)
	if err != nil || !got.AsIs || got.NexusID != 33 {
		t.Errorf("Get: %+v, %v", got, err)
	}

	// Хранилище не зависит от исходной папки.
	os.RemoveAll(src)
	if problems, _ := s.Verify(v.ModID, v.ID); len(problems) != 0 {
		t.Errorf("после удаления исходной папки: %v", problems)
	}

	if _, err := s.AddFiles(t.TempDir(), nil, Info{Name: "пусто"}); err == nil {
		t.Error("версия без файлов добавлена")
	}
	if _, err := s.AddFiles(t.TempDir(), []string{"../x"}, Info{Name: "x"}); !errors.Is(err, fsx.ErrUnsafePath) {
		t.Errorf("путь наружу: %v", err)
	}
}

func TestAddFilesUnknownVersion(t *testing.T) {
	s, _ := open(t)
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.mod"), []byte("1"), 0o644)
	v, err := s.AddFiles(src, []string{"a.mod"}, Info{Name: "a"})
	if err != nil || len(v.ID) != 12 {
		t.Fatalf("версия по содержимому: %+v, %v", v, err)
	}
	if _, err := s.AddFiles(src, []string{"a.mod"}, Info{Name: "a"}); !errors.Is(err, fs.ErrExist) {
		t.Errorf("те же файлы второй раз: %v, want fs.ErrExist", err)
	}
}

// Мод в разработке хранит не файлы, а путь к папке проекта.
func TestAddLink(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), "auspex_vitals")
	os.MkdirAll(project, 0o755)
	v, err := s.AddLink("auspex_vitals", "mods/auspex_vitals", project)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(v.ModID, v.ID)
	if err != nil || got.Link != project || got.LinkPath != "mods/auspex_vitals" || len(got.Files) != 0 {
		t.Errorf("версия: %+v, %v", got, err)
	}
	if _, err := s.AddLink("auspex_vitals", "mods/auspex_vitals", project); !errors.Is(err, fs.ErrExist) {
		t.Errorf("повторное добавление: %v", err)
	}
	if _, err := s.AddLink("other", "mods/other", filepath.Join(project, "нет")); err == nil {
		t.Error("ссылка на несуществующую папку принята")
	}
}
