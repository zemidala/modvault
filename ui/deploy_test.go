package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func issueTitles(s State) string {
	out := make([]string, len(s.Issues))
	for i, issue := range s.Issues {
		out[i] = issue.Title
	}
	return strings.Join(out, " | ")
}

func TestDeployFromWindow(t *testing.T) {
	a, home := newApp(t)
	game := filepath.Join(t.TempDir(), "Игра")
	os.MkdirAll(game, 0o755)
	os.WriteFile(filepath.Join(game, "Darktide.exe"), []byte("игра"), 0o644)

	s, err := a.addArchive(writeZip(t, "Scoreboard-22-1-4-0-1700000000.zip", map[string]string{
		"Scoreboard/Scoreboard.mod": "return {}",
		"Darktide.exe":              "мод заменяет файл игры",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(issueTitles(s), "Папка игры не выбрана") || s.Status[0].Command != "ChooseGame" {
		t.Fatalf("без папки игры: %s, %+v", issueTitles(s), s.Status[0])
	}
	if _, err := a.Deploy(); err == nil {
		t.Error("развёртывание без папки игры прошло")
	}

	s, err = a.setGame(game)
	if err != nil {
		t.Fatal(err)
	}
	if m := findMod(t, s, "scoreboard"); m.State != "Ждёт развёртывания" {
		t.Errorf("до развёртывания: %q", m.State)
	}
	if s.PlanTitle != "План развёртывания: 2 изменения" || len(s.Plan) != 1 || !strings.Contains(s.Plan[0], "положить 2 файла") {
		t.Errorf("план: %q %v", s.PlanTitle, s.Plan)
	}
	files, err := a.PlanFiles()
	if err != nil || len(files) != 2 || !strings.HasPrefix(files[0], "+ ") {
		t.Errorf("план по файлам: %v, %v", files, err)
	}

	res, err := a.Deploy()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Message, "2 изменения") || res.State.PlanTitle != "" {
		t.Errorf("после развёртывания: %q, план %q", res.Message, res.State.PlanTitle)
	}
	if m := findMod(t, res.State, "scoreboard"); m.State != "Развёрнут" {
		t.Errorf("состояние мода: %q", m.State)
	}
	if data, _ := os.ReadFile(filepath.Join(game, "Scoreboard", "Scoreboard.mod")); string(data) != "return {}" {
		t.Errorf("файл мода в игре: %q", data)
	}

	// Папка игры и развёрнутое переживают перезапуск программы.
	b := NewAppAt(home)
	if s := state(t, b); findMod(t, s, "scoreboard").State != "Развёрнут" || s.Status[0].Value != game {
		t.Errorf("после перезапуска: %+v", s.Status[0])
	}

	// Мод удалён из хранилища, но его файлы ещё в игре: окно не уходит
	// в демонстрационный режим, а следующее развёртывание их убирает.
	if err := b.removeMod("scoreboard", true); err != nil {
		t.Fatal(err)
	}
	s = state(t, b)
	if s.Demo || s.PlanTitle == "" {
		t.Fatalf("после удаления развёрнутого мода: demo=%v, план %q", s.Demo, s.PlanTitle)
	}
	if _, err := b.Deploy(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(game, "Darktide.exe")); string(data) != "игра" {
		t.Errorf("файл игры не вернулся: %q", data)
	}
	if _, err := os.Stat(filepath.Join(game, "Scoreboard")); !os.IsNotExist(err) {
		t.Errorf("папка мода осталась в игре: %v", err)
	}
	if s := state(t, b); !s.Demo {
		t.Error("после снятия всего окно не вернулось к демонстрационным данным")
	}
}

func TestConflictAndDriftIssues(t *testing.T) {
	a, _ := newApp(t)
	game := t.TempDir()
	if _, err := a.setGame(game); err != nil {
		t.Fatal(err)
	}
	a.addArchive(writeZip(t, "Healthbars.zip", map[string]string{"shared/ui.lua": "healthbars", "hb/hb.mod": "1"}))
	a.addArchive(writeZip(t, "Numeric UI.zip", map[string]string{"shared/ui.lua": "numeric", "nu/nu.mod": "2"}))
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	s := state(t, a)
	if !strings.Contains(issueTitles(s), "Healthbars и Numeric UI меняют одни и те же файлы (1)") {
		t.Errorf("конфликт: %s", issueTitles(s))
	}
	if data, _ := os.ReadFile(filepath.Join(game, "shared", "ui.lua")); string(data) != "numeric" {
		t.Errorf("победил не нижний мод: %q", data)
	}

	// Файл мода изменили вне программы.
	target := filepath.Join(game, "nu", "nu.mod")
	os.Remove(target)
	os.WriteFile(target, []byte("правка"), 0o644)
	s = state(t, a)
	if !strings.Contains(issueTitles(s), "1 файл мода изменён вне программы") {
		t.Errorf("расхождение: %s", issueTitles(s))
	}
	if s.Status[2].Level != LevelError {
		t.Errorf("строка «Файлы в игре»: %+v", s.Status[2])
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
	a, home := newApp(t)
	game := filepath.Join(t.TempDir(), "игра")
	os.MkdirAll(game, 0o755)
	if _, err := a.setGame(game); err != nil {
		t.Fatal(err)
	}
	a.addArchive(writeZip(t, "m.zip", map[string]string{"m/m.mod": "1"}))
	os.RemoveAll(game)

	s := state(t, NewAppAt(home))
	if !strings.Contains(issueTitles(s), "Развёртывание недоступно") || s.Status[0].Level != LevelError {
		t.Errorf("пропавшая папка игры: %s, %+v", issueTitles(s), s.Status[0])
	}
}
