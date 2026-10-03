package manager

import (
	"context"
	"strings"
	"testing"
)

func setting(t *testing.T, s State, key string) Setting {
	t.Helper()
	for _, item := range s.Settings {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("настройки %q нет: %+v", key, s.Settings)
	return Setting{}
}

func TestJournal(t *testing.T) {
	a, _ := setsApp(t) // добавлены моды и выполнено развёртывание
	a.CreateSet("Тест", []string{"plain"})
	a.SwitchSet("Тест")
	a.RemoveMod("other", true)

	events, err := a.Journal(0)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range events {
		if e.Time.IsZero() || e.Kind == "" {
			t.Errorf("запись без времени или вида: %+v", e)
		}
		texts = append(texts, e.Text)
	}
	all := strings.Join(texts, "\n")
	for _, want := range []string{"Удалён мод «Other»", "Набор «Тест» в игре", "Набор «Тест» создан", "Развёрнуто:", "Из архива добавлен «Plain»"} {
		if !strings.Contains(all, want) {
			t.Errorf("в журнале нет %q:\n%s", want, all)
		}
	}
	// Новые записи идут первыми; предел числа записей соблюдается.
	if events[0].Text != "Удалён мод «Other»" {
		t.Errorf("первая запись: %q", events[0].Text)
	}
	if two, _ := a.Journal(2); len(two) != 2 || two[0] != events[0] {
		t.Errorf("две последние записи: %+v", two)
	}
	// Журнал переживает перезапуск.
	if again, _ := NewWith(a.home, a.game).Journal(1); len(again) != 1 || again[0] != events[0] {
		t.Errorf("после перезапуска: %+v", again)
	}
}

func TestDownloadsList(t *testing.T) {
	a, f, _ := nexusApp(t)
	ctx := context.Background()
	login(t, a)
	f.add(t, 30, "Flux", fakeFile{ID: 300, Version: "1.0", Category: "MAIN"}, map[string]string{"Flux/Flux.mod": "return {}"})
	f.add(t, 31, "Glow", fakeFile{ID: 310, Version: "2.0", Category: "MAIN"}, map[string]string{"Glow/Glow.mod": "return {}"})
	f.broken[310] = true

	if len(a.Downloads()) != 0 {
		t.Error("загрузки до первой загрузки")
	}
	a.InstallLink(ctx, link(30, 300), nil)
	a.InstallLink(ctx, link(31, 310), nil)

	list := a.Downloads()
	if len(list) != 2 {
		t.Fatalf("загрузки: %+v", list)
	}
	// Новая загрузка первой; неудачная помечена и объясняет причину.
	if d := list[0]; d.Name != "Glow" || d.Version != "2.0" || d.State != DownloadFailed || d.Message == "" || d.Finished.IsZero() {
		t.Errorf("неудачная загрузка: %+v", d)
	}
	if d := list[1]; d.Name != "Flux" || d.State != DownloadDone || d.Done != d.Total || d.Total == 0 || !strings.Contains(d.Message, "Установлен: Flux 1.0") {
		t.Errorf("удачная загрузка: %+v", d)
	}
	events, _ := a.Journal(0)
	var all []string
	for _, e := range events {
		all = append(all, e.Kind+": "+e.Text)
	}
	joined := strings.Join(all, "\n")
	if !strings.Contains(joined, "install: Установлен: Flux 1.0") || !strings.Contains(joined, "error: Загрузка «Glow»") {
		t.Errorf("журнал загрузок:\n%s", joined)
	}
}

func TestSettings(t *testing.T) {
	a, f, _ := nexusApp(t)
	ctx := context.Background()
	login(t, a)
	s := state(t, a)
	for _, key := range []string{SettingCheckOnStart, SettingDeployUpdates, SettingDirectLaunch} {
		if !setting(t, s, key).On {
			t.Errorf("настройка %s по умолчанию выключена", key)
		}
	}
	// Без доступа к системе переключателя ссылок nxm нет.
	for _, item := range s.Settings {
		if item.Key == SettingNxm {
			t.Error("переключатель nxm показан без доступа к системе")
		}
	}
	a.protocol = &fakeProtocol{cmd: `"G:\Vortex\Vortex.exe" -d "%1"`}
	if item := setting(t, state(t, a), SettingNxm); item.On || !strings.Contains(item.Detail, "открывает Vortex") {
		t.Errorf("переключатель nxm: %+v", item)
	}

	s, err := a.SetSetting(SettingCheckOnStart, false)
	if err != nil || setting(t, s, SettingCheckOnStart).On || s.CheckOnStart {
		t.Errorf("проверка при запуске: %v, %+v", err, setting(t, s, SettingCheckOnStart))
	}
	if _, err := a.SetSetting("нет такой", true); err == nil {
		t.Error("неизвестная настройка принята")
	}
	// Настройки переживают перезапуск.
	if setting(t, state(t, NewWith(a.home, a.game)), SettingCheckOnStart).On {
		t.Error("настройка не сохранилась")
	}

	// «Обновлённый мод сразу попадает в игру» выключено — обновление ждёт кнопки.
	for i, version := range []string{"1.0", "2.0"} {
		f.add(t, 30, "Flux", fakeFile{ID: 300 + i, Version: version, Category: "MAIN"}, map[string]string{"Flux/Flux.mod": "return {} -- " + version})
	}
	a.InstallLink(ctx, link(30, 300), nil)
	a.Deploy()
	a.SetSetting(SettingDeployUpdates, false)
	res, err := a.InstallLink(ctx, link(30, 301), nil)
	if err != nil || res.State.PlanTitle == "" || !strings.Contains(res.Message, "по кнопке «Развернуть»") {
		t.Errorf("обновление при ручном развёртывании: %v, %q", err, res.Message)
	}
}

func TestSetupSteps(t *testing.T) {
	a, _, _ := nexusApp(t)
	a.protocol = &fakeProtocol{}
	steps := state(t, a).Setup
	if len(steps) != 3 || !steps[0].Done || steps[1].Done || steps[1].Command != "NexusKey" || steps[2].Command != "ToggleNxm" {
		t.Fatalf("шаги: %+v", steps)
	}
	login(t, a)
	if steps = state(t, a).Setup; !steps[1].Done || steps[2].Done {
		t.Errorf("после ввода ключа: %+v", steps)
	}
	// Всё сделано — памятка исчезает сама.
	a.ToggleNxm(`C:\m.exe`)
	if steps = state(t, a).Setup; len(steps) != 0 {
		t.Errorf("после всех шагов: %+v", steps)
	}
	// Памятку можно скрыть, не выполняя шагов.
	a.ToggleNxm("")
	if steps = state(t, a).Setup; len(steps) == 0 {
		t.Fatal("памятка не вернулась с невыполненным шагом")
	}
	if s, err := a.HideSetup(); err != nil || len(s.Setup) != 0 {
		t.Errorf("после скрытия: %v, %+v", err, s.Setup)
	}
}

func TestChooseWinner(t *testing.T) {
	a, _, g := newGame(t)
	if _, err := a.setGame(g); err != nil {
		t.Fatal(err)
	}
	a.addArchive(writeZip(t, "Alpha.zip", map[string]string{"Shared/Shared.mod": "alpha", "Shared/a.lua": "alpha"}))
	a.addArchive(writeZip(t, "Beta.zip", map[string]string{"Shared/Shared.mod": "beta", "Shared/a.lua": "beta"}))
	inGame := func() string { return snapshot(t, g)["mods/Shared/Shared.mod"] }

	var issue Issue
	for _, i := range state(t, a).Issues {
		if i.Command == "ChooseWinner" {
			issue = i
		}
	}
	if issue.Arg != "alpha|beta" || issue.Action != "Выбрать победителя" {
		t.Fatalf("замечание о конфликте: %+v", issue)
	}
	choice, err := a.WinnerOptions(issue.Arg)
	if err != nil || choice.Files != 2 || len(choice.Options) != 2 {
		t.Fatalf("выбор: %+v, %v", choice, err)
	}
	if o := choice.Options[1]; o.ID != "beta" || !o.Current || !o.Default || o.Name != "Beta" {
		t.Errorf("победитель по порядку: %+v", o)
	}

	// Закрепляем верхний мод: в игру идут его файлы.
	if _, err := a.SetWinner(issue.Arg, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Deploy(); err != nil || inGame() != "alpha" {
		t.Errorf("после закрепления: %v, в игре %q", err, inGame())
	}
	if choice, _ = a.WinnerOptions(issue.Arg); !choice.Options[0].Current {
		t.Errorf("закреплённый победитель не отмечен: %+v", choice.Options)
	}
	// Возврат к порядку загрузки убирает закрепление.
	if _, err := a.SetWinner(issue.Arg, "beta"); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := a.loadProfile(); len(p.Winners) != 0 {
		t.Errorf("закрепление осталось: %v", p.Winners)
	}
	if _, err := a.Deploy(); err != nil || inGame() != "beta" {
		t.Errorf("после возврата: %v, в игре %q", err, inGame())
	}

	if _, err := a.SetWinner(issue.Arg, "gamma"); err == nil {
		t.Error("победителем принят посторонний мод")
	}
	if _, err := a.SetWinner("x|y", "x"); err == nil {
		t.Error("выбор в несуществующем конфликте прошёл")
	}
}
