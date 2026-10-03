package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zemidala/modvault/game/darktide"
)

func TestRunDiagnosis(t *testing.T) {
	a, _ := setsApp(t)
	logs := t.TempDir()
	a.game.(*darktide.Darktide).LogDir = logs

	// Журналов нет — замечаний о запуске нет.
	if titles := issueTitles(state(t, a)); strings.Contains(titles, "запуск") {
		t.Errorf("замечания без журнала: %s", titles)
	}

	// Игра отработала после установки модов и записала ошибки.
	log := "12:00:00.000 [Lua] [MOD][Plain][ERROR] attempt to index a nil value\n" +
		"12:00:01.000 [Lua] [MOD][plain][ERROR] attempt to index a nil value\n" +
		"12:00:02.000 [Lua] [MOD][Other][ERROR] hook failed\n" +
		"12:00:03.000 [Lua] [MOD][NotInStore][ERROR] кто-то посторонний\n" +
		"12:00:04.000 <<Crash>>Out of memory\n<<Crash type>>memory<</Crash type>>\n"
	path := filepath.Join(logs, "console-1.log")
	os.WriteFile(path, []byte(log), 0o644)
	at := time.Now().Add(time.Hour) // запуск после того, как моды добавлены
	os.Chtimes(path, at, at)

	s := state(t, a)
	titles := issueTitles(s)
	for _, want := range []string{"закончился сбоем", "«Plain» выдал 2 ошибки", "«Other» выдал 1 ошибку"} {
		if !strings.Contains(titles, want) {
			t.Errorf("нет замечания %q: %s", want, titles)
		}
	}
	if strings.Contains(titles, "NotInStore") {
		t.Errorf("замечание о постороннем моде: %s", titles)
	}
	if m := findMod(t, s, "plain"); m.RunErrors != 2 || m.RunError != "attempt to index a nil value" {
		t.Errorf("отметка у мода: %+v", m)
	}
	if m := findMod(t, s, "deep"); m.RunErrors != 0 {
		t.Errorf("отметка у исправного мода: %+v", m)
	}
	var crash, disable Issue
	for _, i := range s.Issues {
		if strings.Contains(i.Title, "сбоем") {
			crash = i
		}
		if i.Command == "DisableMod" && i.Arg == "plain" {
			disable = i
		}
	}
	if !strings.Contains(crash.Detail, "не хватило памяти") || !strings.Contains(crash.Detail, "Out of memory") {
		t.Errorf("причина сбоя: %q", crash.Detail)
	}
	if disable.Action != "Выключить «Plain»" {
		t.Errorf("кнопка у замечания: %+v", disable)
	}

	// Мод выключили — замечание о нём уходит, о другом остаётся.
	s, _ = a.SetEnabled("plain", false)
	if titles = issueTitles(s); strings.Contains(titles, "«Plain»") || !strings.Contains(titles, "«Other»") {
		t.Errorf("после выключения: %s", titles)
	}

	// Мод обновили после того запуска — журнал уже не о новой версии.
	os.Chtimes(path, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if titles = issueTitles(state(t, a)); strings.Contains(titles, "выдал") {
		t.Errorf("замечания о версиях, которых в том запуске не было: %s", titles)
	}
}
