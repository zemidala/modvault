package profile

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func order(p Profile) string {
	ids := make([]string, len(p.Entries))
	for i, e := range p.Entries {
		ids[i] = e.ModID
	}
	return strings.Join(ids, ",")
}

func sample() Profile {
	p := Profile{Name: "Основной"}
	for _, id := range []string{"a", "b", "c", "d"} {
		p.Add(id, "1")
	}
	return p
}

func TestProfileEdits(t *testing.T) {
	p := sample()
	if order(p) != "a,b,c,d" {
		t.Fatalf("порядок = %s", order(p))
	}
	if err := p.Add("b", "2"); err == nil {
		t.Error("мод добавился в профиль дважды")
	}

	moves := []struct {
		id   string
		to   int
		want string
	}{
		{"a", 2, "b,c,a,d"},
		{"d", 0, "d,b,c,a"},
		{"b", 99, "d,c,a,b"},
		{"a", -5, "a,d,c,b"},
		{"c", 2, "a,d,c,b"},
	}
	for _, m := range moves {
		if err := p.Move(m.id, m.to); err != nil {
			t.Fatal(err)
		}
		if order(p) != m.want {
			t.Fatalf("Move(%s, %d): %s, want %s", m.id, m.to, order(p), m.want)
		}
	}

	if err := p.SetEnabled("d", false); err != nil {
		t.Fatal(err)
	}
	if err := p.SetVersion("d", "2"); err != nil {
		t.Fatal(err)
	}
	if e := p.Entries[p.Index("d")]; e.Enabled || e.VersionID != "2" || p.Index("d") != 1 {
		t.Errorf("запись d = %+v на месте %d", e, p.Index("d"))
	}

	if err := p.Remove("d"); err != nil {
		t.Fatal(err)
	}
	if order(p) != "a,c,b" {
		t.Errorf("после Remove: %s", order(p))
	}

	for name, err := range map[string]error{
		"Remove":     p.Remove("нет"),
		"SetEnabled": p.SetEnabled("нет", true),
		"SetVersion": p.SetVersion("нет", "1"),
		"Move":       p.Move("нет", 0),
	} {
		if !errors.Is(err, ErrNoMod) {
			t.Errorf("%s для отсутствующего мода: %v, want ErrNoMod", name, err)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "profiles"))
	if err != nil {
		t.Fatal(err)
	}
	p := sample()
	p.SetEnabled("c", false)
	if err := d.Save(p); err != nil {
		t.Fatal(err)
	}

	got, err := d.Load("Основной")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Основной" || order(got) != "a,b,c,d" || got.Entries[2].Enabled || !got.Entries[0].Enabled {
		t.Errorf("после чтения: %+v", got)
	}

	empty := Profile{Name: "Пустой"}
	if err := d.Save(empty); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Load("Пустой"); err != nil || got.Entries == nil || len(got.Entries) != 0 {
		t.Errorf("пустой профиль: %+v, %v", got, err)
	}

	names, err := d.List()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "Основной,Пустой" {
		t.Errorf("List = %v", names)
	}

	if _, err := d.Load("Нет такого"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("чтение отсутствующего: %v, want fs.ErrNotExist", err)
	}
}

func TestRenameCopyDelete(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Save(sample()); err != nil {
		t.Fatal(err)
	}

	if err := d.Copy("Основной", "Копия"); err != nil {
		t.Fatal(err)
	}
	if err := d.Copy("Основной", "Копия"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("копия поверх существующего: %v, want fs.ErrExist", err)
	}
	if err := d.Rename("Копия", "Основной"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("переименование в занятое название: %v, want fs.ErrExist", err)
	}
	if err := d.Rename("Копия", "Для стрима"); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete("Основной"); err != nil {
		t.Fatal(err)
	}

	names, _ := d.List()
	if strings.Join(names, ",") != "Для стрима" {
		t.Errorf("List = %v", names)
	}
	if got, err := d.Load("Для стрима"); err != nil || got.Name != "Для стрима" || order(got) != "a,b,c,d" {
		t.Errorf("переименованный профиль: %+v, %v", got, err)
	}
}

func TestInvalidNames(t *testing.T) {
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", " ", "a/b", `a\b`, "..", ".скрытый", "NUL", "a:b", "хвост ", "вопрос?"} {
		if err := d.Save(Profile{Name: name}); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Save(%q): %v, want ErrInvalidName", name, err)
		}
		if _, err := d.Load(name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Load(%q): %v, want ErrInvalidName", name, err)
		}
	}
	if names, _ := d.List(); len(names) != 0 {
		t.Errorf("недопустимые названия создали файлы: %v", names)
	}
}

func TestRejectsCorrupt(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "Обрезан.json"), []byte(`{"name":"Обрезан","mods":[{"mod":"a"`), 0o644)
	os.WriteFile(filepath.Join(dir, "Дубль.json"), []byte(`{"mods":[{"mod":"a"},{"mod":"a"}]}`), 0o644)

	for _, name := range []string{"Обрезан", "Дубль"} {
		if _, err := d.Load(name); err == nil {
			t.Errorf("испорченный профиль «%s» прочитан без ошибки", name)
		}
	}
	dup := Profile{Name: "x", Entries: []Entry{{ModID: "a"}, {ModID: "a"}}}
	if err := d.Save(dup); err == nil {
		t.Error("профиль с повтором мода сохранён")
	}
}

// Обрыв записи: вспомогательный процесс без остановки сохраняет профиль то
// с одним, то с другим набором, его убивают. Профиль обязан читаться и быть
// одним из двух наборов целиком.
const crashEnv = "MODVAULT_PROFILE_CRASH_DIR"

func crashProfile(variant int) Profile {
	p := Profile{Name: "Основной"}
	for i := 0; i < 2000; i++ {
		p.Add(fmt.Sprintf("mod_%d_%04d", variant, i), "1")
	}
	return p
}

func TestCrashHelper(t *testing.T) {
	dir := os.Getenv(crashEnv)
	if dir == "" {
		t.Skip("вспомогательный процесс для TestSaveSurvivesKill")
	}
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	variants := []Profile{crashProfile(0), crashProfile(1)}
	for i := 0; ; i++ {
		if err := d.Save(variants[i%2]); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			os.Stdout.WriteString("ready\n")
		}
	}
}

func TestSaveSurvivesKill(t *testing.T) {
	if testing.Short() {
		t.Skip("запускает и убивает процессы")
	}
	dir := t.TempDir()

	for round := 0; round < 6; round++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
		cmd.Env = append(os.Environ(), crashEnv+"="+dir)
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
		time.Sleep(time.Duration(3+round*7) * time.Millisecond)
		cmd.Process.Kill()
		cmd.Wait()

		d, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		p, err := d.Load("Основной")
		if err != nil {
			t.Fatalf("раунд %d: профиль не читается: %v", round, err)
		}
		if len(p.Entries) != 2000 {
			t.Fatalf("раунд %d: записей %d", round, len(p.Entries))
		}
		prefix := p.Entries[0].ModID[:6] // «mod_0_» или «mod_1_»
		for _, e := range p.Entries {
			if !strings.HasPrefix(e.ModID, prefix) {
				t.Fatalf("раунд %d: в профиле смешаны два набора", round)
			}
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Fatalf("раунд %d: после Open в папке %d файлов", round, len(entries))
		}
	}
}
