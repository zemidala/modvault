package manager

import (
	"archive/zip"
	"github.com/zemidala/modvault/game"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newApp(t testing.TB) (*Manager, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "Modvault")
	return NewAt(home), home
}

func state(t *testing.T, a *Manager) State {
	t.Helper()
	s, err := a.State()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func findMod(t *testing.T, s State, id string) Mod {
	t.Helper()
	for _, m := range s.Mods {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("мод %q не найден", id)
	return Mod{}
}

func writeZip(t testing.TB, name string, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for entry, content := range files {
		e, err := w.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		e.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func ids(s State) string {
	out := make([]string, len(s.Mods))
	for i, m := range s.Mods {
		out[i] = m.ID
	}
	return strings.Join(out, ",")
}

func TestDemoState(t *testing.T) {
	a, _ := newApp(t)
	s := state(t, a)

	if !s.Demo {
		t.Error("демонстрационные данные не помечены")
	}
	if !strings.HasPrefix(s.Version, "modvault ") {
		t.Errorf("версия = %q", s.Version)
	}
	if len(s.Mods) != 6 {
		t.Fatalf("модов %d, want 6", len(s.Mods))
	}
	if len(s.Issues) != 2 {
		t.Errorf("замечаний %d, want 2: %+v", len(s.Issues), s.Issues)
	}
	if s.PlanTitle != "План развёртывания: 1 изменение" || len(s.Plan) != 1 {
		t.Errorf("план = %q %v", s.PlanTitle, s.Plan)
	}

	want := map[string]Level{
		"dmf": LevelOK, "scoreboard": LevelWarn, "healthbars": LevelError, "spidey_sense": LevelOff,
	}
	for id, level := range want {
		if m := findMod(t, s, id); m.Level != level {
			t.Errorf("%s: уровень %q (%s), want %q", id, m.Level, m.State, level)
		}
	}
	if checks := s.Status[len(s.Status)-1]; checks.Value != "2 замечания" {
		t.Errorf("проверки = %q", checks.Value)
	}
}

func TestDemoSetEnabled(t *testing.T) {
	a, _ := newApp(t)

	// Включаем обратно мод, который ещё лежит в игре: план пустеет.
	s, err := a.SetEnabled("spidey_sense", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Plan) != 0 || s.PlanTitle != "" {
		t.Errorf("план после возврата мода = %q %v", s.PlanTitle, s.Plan)
	}

	// Выключаем одного из участников конфликта: замечание о конфликте уходит.
	s, err = a.SetEnabled("numeric_ui", false)
	if err != nil {
		t.Fatal(err)
	}
	if m := findMod(t, s, "healthbars"); m.Level != LevelOK {
		t.Errorf("healthbars после выключения соперника: %q", m.State)
	}
	if len(s.Issues) != 1 || len(s.Plan) != 1 {
		t.Errorf("замечаний %d, изменений %d", len(s.Issues), len(s.Plan))
	}

	if _, err := a.SetEnabled("dmf", false); err == nil {
		t.Error("закреплённый мод выключился")
	}
	if _, err := a.SetEnabled("нет_такого", true); err == nil {
		t.Error("несуществующий мод включился")
	}
	if _, err := a.ModFiles("scoreboard"); err == nil {
		t.Error("у демонстрационного мода нашлись файлы")
	}
}

func TestRealMods(t *testing.T) {
	a, home := newApp(t)
	scoreboard := writeZip(t, "Scoreboard-22-1-4-0-1700000000.zip", map[string]string{
		"Scoreboard/Scoreboard.mod":   "return {}",
		"Scoreboard/scripts/main.lua": "print(1)",
	})
	numeric := writeZip(t, "Numeric UI.zip", map[string]string{"NumericUI/NumericUI.mod": "return {}"})

	// Первый настоящий мод вытесняет демонстрационные.
	s, err := a.addArchive(scoreboard)
	if err != nil {
		t.Fatal(err)
	}
	if s.Demo || ids(s) != "scoreboard" {
		t.Fatalf("после добавления: demo=%v, моды %s", s.Demo, ids(s))
	}
	m := findMod(t, s, "scoreboard")
	if m.Name != "Scoreboard" || m.Version != "1.4.0" || m.Files != 2 || m.Versions != 1 || !m.Enabled || m.Source != "Nexus Mods" {
		t.Errorf("мод = %+v", m)
	}

	s, err = a.addArchive(numeric)
	if err != nil {
		t.Fatal(err)
	}
	if ids(s) != "scoreboard,numeric_ui" {
		t.Errorf("порядок = %s", ids(s))
	}
	if m := findMod(t, s, "numeric_ui"); m.Version != "—" || m.Source != "Архив с диска" {
		t.Errorf("мод без версии = %+v", m)
	}

	if _, err := a.addArchive(scoreboard); err == nil || !strings.Contains(err.Error(), "уже есть") {
		t.Errorf("повторное добавление: %v", err)
	}

	files, err := a.ModFiles("scoreboard")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, "|") != "Scoreboard/Scoreboard.mod|Scoreboard/scripts/main.lua" {
		t.Errorf("файлы = %v", files)
	}

	// Выключение сохраняется на диск и переживает перезапуск программы.
	if _, err := a.SetEnabled("scoreboard", false); err != nil {
		t.Fatal(err)
	}
	s = state(t, NewAt(home))
	if m := findMod(t, s, "scoreboard"); m.Enabled || m.State != "Выключен" {
		t.Errorf("после перезапуска: %+v", m)
	}
	if ids(s) != "scoreboard,numeric_ui" {
		t.Errorf("порядок после перезапуска = %s", ids(s))
	}

	// Новая версия занимает место старой в профиле, старая остаётся в хранилище.
	newer := writeZip(t, "Scoreboard-22-1-5-0-1700000500.zip", map[string]string{"Scoreboard/Scoreboard.mod": "return {1}"})
	s, err = a.addArchive(newer)
	if err != nil {
		t.Fatal(err)
	}
	if m := findMod(t, s, "scoreboard"); m.Version != "1.5.0" || m.Versions != 2 || m.Files != 1 || m.Enabled {
		t.Errorf("после обновления: %+v", m)
	}

	// Удаление: мод уходит из хранилища и из профиля.
	if err := a.removeMod("scoreboard", true); err != nil {
		t.Fatal(err)
	}
	s = state(t, a)
	if ids(s) != "numeric_ui" {
		t.Errorf("после удаления: %s", ids(s))
	}
	if _, err := os.Stat(filepath.Join(home, "mods", "scoreboard")); !os.IsNotExist(err) {
		t.Errorf("папка мода осталась: %v", err)
	}

	// Последний мод удалён — окно снова показывает демонстрационные.
	if err := a.removeMod("numeric_ui", true); err != nil {
		t.Fatal(err)
	}
	if s := state(t, a); !s.Demo {
		t.Error("пустое хранилище не вернуло демонстрационный режим")
	}
}

func TestBadArchive(t *testing.T) {
	a, _ := newApp(t)
	bad := filepath.Join(t.TempDir(), "мод.zip")
	os.WriteFile(bad, []byte("не архив"), 0o644)

	if _, err := a.addArchive(bad); err == nil {
		t.Fatal("текстовый файл принят за архив")
	}
	if s := state(t, a); !s.Demo {
		t.Error("отклонённый архив оставил след в хранилище")
	}
}

func TestBrokenHome(t *testing.T) {
	// Папка данных — на месте обычного файла: хранилище открыть нельзя.
	file := filepath.Join(t.TempDir(), "занято")
	os.WriteFile(file, nil, 0o644)

	a := NewAt(file)
	if _, err := a.State(); err == nil {
		t.Error("State не сообщил о недоступном хранилище")
	}
	if _, err := a.addArchive("x.zip"); err == nil {
		t.Error("addArchive не сообщил о недоступном хранилище")
	}
}

func TestPlural(t *testing.T) {
	tests := map[int]string{0: "модов", 1: "мод", 2: "мода", 4: "мода", 5: "модов", 11: "модов", 12: "модов", 21: "мод", 22: "мода", 111: "модов"}
	for n, want := range tests {
		if got := plural(n, "мод", "мода", "модов"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestChooseHome(t *testing.T) {
	if got := chooseHome([]game.Install{{Dir: `G:\SteamLibrary\steamapps\common\Warhammer 40,000 DARKTIDE`}}); got != `G:\Modvault` {
		t.Errorf("хранилище для игры на G: = %q", got)
	}
	if got := chooseHome(nil); filepath.Base(got) != "Modvault" {
		t.Errorf("без игры = %q", got)
	}
}
