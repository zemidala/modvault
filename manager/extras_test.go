package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHideIssue(t *testing.T) {
	a, _, g := newGame(t)
	a.setGame(g)
	a.addArchive(writeZip(t, "Alpha.zip", map[string]string{"Shared/Shared.mod": "alpha"}))
	a.addArchive(writeZip(t, "Beta.zip", map[string]string{"Shared/Shared.mod": "beta"}))

	s := state(t, a)
	var conflict Issue
	for _, i := range s.Issues {
		if i.Key == "" {
			t.Errorf("замечание без ключа: %+v", i)
		}
		if i.Command == "ChooseWinner" {
			conflict = i
		}
	}
	if conflict.Key != "ChooseWinner|alpha|beta" || s.HiddenIssues != 0 {
		t.Fatalf("ключ замечания %q, скрыто %d", conflict.Key, s.HiddenIssues)
	}
	before := len(s.Issues)

	s, err := a.HideIssue(conflict.Key)
	if err != nil || len(s.Issues) != before-1 || s.HiddenIssues != 1 || strings.Contains(issueTitles(s), "меняют одни и те же файлы") {
		t.Errorf("после скрытия: %v, замечаний %d, скрыто %d", err, len(s.Issues), s.HiddenIssues)
	}
	// Повторное скрытие ничего не меняет; скрытое переживает перезапуск.
	a.HideIssue(conflict.Key)
	if s = state(t, NewWith(a.home, a.game)); s.HiddenIssues != 1 || len(a.settings.HiddenIssues) != 1 {
		t.Errorf("после перезапуска скрыто %d, записей %d", s.HiddenIssues, len(a.settings.HiddenIssues))
	}
	if s, err = a.ShowHiddenIssues(); err != nil || s.HiddenIssues != 0 || len(s.Issues) != before {
		t.Errorf("после возврата: %v, замечаний %d, скрыто %d", err, len(s.Issues), s.HiddenIssues)
	}
}

func TestUseVersion(t *testing.T) {
	a, g := setsApp(t)
	mod := func(version string) string {
		return writeZip(t, "Flux-30-"+version+"-1700000000.zip", map[string]string{"Flux/Flux.mod": "return {} -- " + version})
	}
	inGame := func() string { return snapshot(t, g)["mods/Flux/Flux.mod"] }
	a.addArchive(mod("1"))
	a.Deploy()
	a.addArchive(mod("2"))
	a.Deploy()
	a.CreateSet("Тест", []string{"flux"})

	versions, err := a.ModVersions("flux")
	if err != nil || len(versions) != 2 || versions[0].Version != "2" || !versions[0].Current || versions[1].Current {
		t.Fatalf("версии: %+v, %v", versions, err)
	}
	// Возврат к прежней версии: включённый мод сразу получает её в игре.
	res, err := a.UseVersion("flux", versions[1].ID)
	if err != nil || inGame() != "return {} -- 1" || res.State.PlanTitle != "" {
		t.Fatalf("возврат версии: %v, в игре %q, сообщение %q", err, inGame(), res.Message)
	}
	if !strings.Contains(res.Message, "Выбрана версия: Flux 1") {
		t.Errorf("сообщение: %q", res.Message)
	}
	if m := findMod(t, res.State, "flux"); m.Version != "1" || m.Versions != 2 {
		t.Errorf("мод после возврата: %+v", m)
	}
	// Версия одна на все наборы.
	if sw, _ := a.SwitchSet("Тест"); findMod(t, sw.State, "flux").Version != "1" {
		t.Error("в другом наборе осталась новая версия")
	}
	if _, err := a.UseVersion("flux", "нет такой"); err == nil {
		t.Error("выбрана несуществующая версия")
	}
}

func TestSetFile(t *testing.T) {
	a, _ := setsApp(t)
	a.addArchive(writeZip(t, "Scoreboard-22-1-4-0-1700000000.zip", map[string]string{"Scoreboard/Scoreboard.mod": "return {}"}))
	a.CreateSet("Мой", []string{"scoreboard", "plain"})
	a.SwitchSet("Мой")
	path := filepath.Join(t.TempDir(), "мой.modvault-set.json")

	msg, err := a.ExportSet(path)
	if err != nil || !strings.Contains(msg, "Набор «Мой» сохранён в файл: 4 мода") || !strings.Contains(msg, "нет номера на Nexus") {
		t.Fatalf("сохранение: %q, %v", msg, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"nexusId": 22`) || !strings.Contains(string(data), `"game": "warhammer40kdarktide"`) {
		t.Errorf("файл набора:\n%s", data)
	}

	// На другом компьютере части модов нет: набор создаётся из того, что есть.
	b, _, g := newGame(t)
	b.setGame(g)
	b.addArchive(writeZip(t, "dml.zip", dmlArchive))
	b.addArchive(modZip(t, "Plain", `return {}`))
	b.addArchive(modZip(t, "Extra", `return {}`))
	res, err := b.ImportSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Set != "Мой" || res.State.Profile != mainProfile || !strings.Contains(res.Message, "2 из 4") || !strings.Contains(res.Message, "Не хватает 2") {
		t.Errorf("загрузка: набор %q, сообщение %q", res.Set, res.Message)
	}
	if len(res.Missing) != 2 {
		t.Fatalf("недостающие: %+v", res.Missing)
	}
	for _, m := range res.Missing {
		switch m.Name {
		case "Scoreboard":
			if m.URL != "https://www.nexusmods.com/warhammer40kdarktide/mods/22" || m.Version != "1.4.0" {
				t.Errorf("недостающий мод с Nexus: %+v", m)
			}
		default:
			if m.URL != "" {
				t.Errorf("недостающий мод без номера: %+v", m)
			}
		}
	}
	sw, err := b.SwitchSet("Мой")
	if err != nil || enabledIDs(sw.State) != "dml,plain" {
		t.Errorf("созданный набор: %v, включены %s", err, enabledIDs(sw.State))
	}
	// Второй раз — название не совпадает с существующим набором.
	if res, err = b.ImportSet(path); err != nil || res.Set != "Мой (2)" {
		t.Errorf("повторная загрузка: %q, %v", res.Set, err)
	}

	bad := filepath.Join(t.TempDir(), "x.json")
	os.WriteFile(bad, []byte(`{"name":"не набор"}`), 0o644)
	if _, err := b.ImportSet(bad); err == nil {
		t.Error("посторонний файл принят за набор")
	}
}

func TestFolders(t *testing.T) {
	a, g := setsApp(t)
	f := a.Folders()
	if f["game"] != g || f["store"] != a.home {
		t.Errorf("папки: %v", f)
	}
	if _, ok := f["logs"]; ok {
		t.Errorf("папка журналов без журналов: %v", f)
	}
}
