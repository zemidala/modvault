package manager

import (
	"strings"
	"testing"
)

func enabledIDs(s State) string {
	var out []string
	for _, m := range s.Mods {
		if m.Enabled {
			out = append(out, m.ID)
		}
	}
	return strings.Join(out, ",")
}

// setsApp — игра с загрузчиком, фреймворком и несколькими модами, всё развёрнуто.
func setsApp(t *testing.T) (*Manager, string) {
	t.Helper()
	a, _, g := newGame(t)
	if _, err := a.setGame(g); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		writeZip(t, "dml.zip", dmlArchive),
		writeZip(t, "dmf.zip", dmfArchive),
		modZip(t, "Needy", `return { require = { "Helper" } }`),
		modZip(t, "Helper", `return { require = { "Deep" } }`),
		modZip(t, "Deep", `return {}`),
		modZip(t, "Plain", `return {}`),
		modZip(t, "Other", `return {}`),
	} {
		if _, err := a.addArchive(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	return a, g
}

func TestSets(t *testing.T) {
	a, g := setsApp(t)
	all := enabledIDs(state(t, a))
	inGame := func(folder string) bool {
		_, ok := snapshot(t, g)["mods/"+folder+"/"+folder+".mod"]
		return ok
	}

	// Набор для проверки одного мода: сам мод и то, без чего он не заработает.
	res, err := a.CreateSet(" Тест ", []string{"plain"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Message, "Набор «Тест» создан: 1 мод") || !strings.Contains(res.Message, "обязательные") {
		t.Errorf("сообщение: %q", res.Message)
	}
	// Создание набора текущий не трогает.
	if res.State.Profile != mainProfile || enabledIDs(res.State) != all || res.State.PlanTitle != "" {
		t.Errorf("после создания: набор %q, включены %s, план %q", res.State.Profile, enabledIDs(res.State), res.State.PlanTitle)
	}
	if _, err := a.CreateSet("Тест", nil); err == nil {
		t.Error("второй набор с тем же названием создан")
	}

	sets, err := a.Sets()
	if err != nil || len(sets) != 2 {
		t.Fatalf("наборы: %+v, %v", sets, err)
	}
	if s := sets[0]; s.Name != mainProfile || !s.Current || s.Enabled != 7 {
		t.Errorf("основной набор: %+v", s)
	}
	if s := sets[1]; s.Name != "Тест" || s.Current || s.Enabled != 3 {
		t.Errorf("новый набор: %+v", s)
	}

	// Переключение сразу приводит игру к набору.
	res, err = a.SwitchSet("Тест")
	if err != nil {
		t.Fatal(err)
	}
	if res.State.Profile != "Тест" || res.State.PlanTitle != "" || !strings.Contains(res.Message, "в игре") {
		t.Errorf("после переключения: набор %q, план %q, сообщение %q", res.State.Profile, res.State.PlanTitle, res.Message)
	}
	if got := enabledIDs(res.State); got != "dml,dmf,plain" {
		t.Errorf("в тестовом наборе включены: %s", got)
	}
	// У каждого мода видно, в каких наборах он включён.
	for id, want := range map[string]string{"plain": "Основной,Тест", "dml": "Основной,Тест", "other": "Основной"} {
		if got := strings.Join(findMod(t, res.State, id).Sets, ","); got != want {
			t.Errorf("наборы мода %s: %q, ждали %q", id, got, want)
		}
	}
	if !inGame("Plain") || inGame("Other") || inGame("Needy") || !inGame("dmf") {
		t.Error("игра не совпала с тестовым набором")
	}

	// Мод с зависимостями тянет их за собой, по цепочке.
	add, err := a.AddToSet("Тест", []string{"needy"})
	if err != nil {
		t.Fatal(err)
	}
	if got := enabledIDs(add.State); got != "dml,dmf,needy,helper,deep,plain" {
		t.Errorf("после добавления мода с зависимостями: %s", got)
	}
	if !strings.Contains(add.Message, "Helper") || !strings.Contains(add.Message, "Deep") {
		t.Errorf("сообщение о зависимостях: %q", add.Message)
	}

	// Несколько модов разом.
	s, err := a.SetEnabledMany([]string{"needy", "helper", "deep"}, false)
	if err != nil || enabledIDs(s) != "dml,dmf,plain" {
		t.Errorf("выключение нескольких: %s, %v", enabledIDs(s), err)
	}

	// Возврат к основному набору: в игре снова весь список.
	res, err = a.SwitchSet(mainProfile)
	if err != nil || enabledIDs(res.State) != all || res.State.PlanTitle != "" {
		t.Fatalf("возврат: включены %s, план %q, %v", enabledIDs(res.State), res.State.PlanTitle, err)
	}
	if !inGame("Other") || !inGame("Needy") || !inGame("Plain") {
		t.Error("игра не вернулась к основному набору")
	}
	if res, _ = a.SwitchSet(mainProfile); !strings.Contains(res.Message, "уже совпадает") {
		t.Errorf("повторное переключение: %q", res.Message)
	}

	// Добавление в чужой набор текущий не меняет.
	if add, err = a.AddToSet("Тест", []string{"other"}); err != nil || enabledIDs(add.State) != all {
		t.Errorf("добавление в другой набор: %v, включены %s", err, enabledIDs(add.State))
	}
	if sets, _ = a.Sets(); sets[1].Enabled != 4 {
		t.Errorf("в другом наборе включено %d", sets[1].Enabled)
	}

	// Копия текущего, переименование, удаление.
	if _, err := a.CreateSet("Копия", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RenameSet("Копия", "Тест"); err == nil {
		t.Error("переименование в занятое название прошло")
	}
	if _, err := a.RenameSet("Копия", "Запас"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteSet(mainProfile); err == nil {
		t.Error("текущий набор удалён")
	}
	if _, err := a.DeleteSet("Запас"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SwitchSet("Запас"); err == nil {
		t.Error("переключение на удалённый набор прошло")
	}
	if sets, _ = a.Sets(); len(sets) != 2 {
		t.Errorf("наборы после удаления: %+v", sets)
	}
}

// Версия мода одна на все наборы: обновление действует в каждом.
func TestVersionSharedBySets(t *testing.T) {
	a, _ := setsApp(t)
	mod := func(version string) string {
		return writeZip(t, "Flux-30-"+version+"-1700000000.zip", map[string]string{"Flux/Flux.mod": "return {} -- " + version})
	}
	if _, err := a.addArchive(mod("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateSet("Тест", []string{"flux"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"2", "3"} {
		if _, err := a.addArchive(mod(v)); err != nil {
			t.Fatal(err)
		}
	}
	// В хранилище новая версия и одна прежняя.
	if m := findMod(t, state(t, a), "flux"); m.Version != "3" || m.Versions != 2 {
		t.Errorf("в текущем наборе: %+v", m)
	}
	res, err := a.SwitchSet("Тест")
	if err != nil {
		t.Fatal(err)
	}
	if m := findMod(t, res.State, "flux"); m.Version != "3" || !m.Enabled {
		t.Errorf("в другом наборе: %+v", m)
	}
	// Мод, добавленный позже, в старом наборе выключен.
	if _, err := a.SwitchSet(mainProfile); err != nil {
		t.Fatal(err)
	}
	a.addArchive(modZip(t, "Late", `return {}`))
	res, _ = a.SwitchSet("Тест")
	if m := findMod(t, res.State, "late"); m.Enabled {
		t.Errorf("новый мод включён в старом наборе: %+v", m)
	}
}
