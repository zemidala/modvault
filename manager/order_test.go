package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func modZip(t *testing.T, name, modFile string) string {
	return writeZip(t, name+".zip", map[string]string{name + "/" + name + ".mod": modFile})
}

func TestSortManualLoader(t *testing.T) {
	a, _, g := newGame(t)
	a.addArchive(writeZip(t, "dml.zip", dmlArchive)) // обычный DML: порядок берётся из профиля
	a.addArchive(modZip(t, "Addon", `return { load_after = { "Core" } }`))
	a.addArchive(modZip(t, "Core", `return {}`))
	a.addArchive(modZip(t, "Plain", `return {}`))
	a.addArchive(modZip(t, "Needy", `return { require = { "Helper" } }`))
	a.addArchive(modZip(t, "Helper", `return {}`))
	a.SetEnabled("helper", false)
	a.SetEnabled("plain", false)
	if _, err := a.setGame(g); err != nil {
		t.Fatal(err)
	}

	s := state(t, a)
	if s.OrderNote != "" {
		t.Errorf("пояснение о порядке при обычном DML: %q", s.OrderNote)
	}
	titles := issueTitles(s)
	if !strings.Contains(titles, "Порядок загрузки нарушает 1 правило модов") {
		t.Errorf("нарушение порядка не замечено: %s", titles)
	}
	var enable Issue
	for _, i := range s.Issues {
		if i.Command == "EnableMod" {
			enable = i
		}
	}
	if enable.Arg != "helper" || !strings.Contains(enable.Title, "«Needy» не заработает") || !strings.Contains(enable.Action, "Helper") {
		t.Errorf("замечание о зависимости: %+v", enable)
	}

	plan, err := a.SortPreview()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Auto || len(plan.Moves) != 2 || len(plan.Cycles) != 0 {
		t.Errorf("план сортировки: %+v", plan)
	}
	if ids(state(t, a)) != ids(s) {
		t.Error("просмотр сортировки изменил профиль")
	}

	s, err = a.Sort()
	if err != nil {
		t.Fatal(err)
	}
	// Выключенный Plain и загрузчик остались на местах, Core встал перед Addon.
	if ids(s) != "dml,core,addon,plain,needy,helper" {
		t.Errorf("после сортировки: %s", ids(s))
	}
	if strings.Contains(issueTitles(s), "нарушает") {
		t.Errorf("после сортировки нарушение осталось: %s", issueTitles(s))
	}
	if again, _ := a.SortPreview(); len(again.Moves) != 0 {
		t.Errorf("повторная сортировка что-то двигает: %v", again.Moves)
	}

	// Включили нужный мод — замечание ушло; порядок попал в игру.
	if _, err := a.SetEnabled("helper", true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(issueTitles(state(t, a)), "не заработает") {
		t.Error("замечание о зависимости осталось после включения мода")
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	order := readFile(t, filepath.Join(g, "mods", "mod_load_order.txt"))
	if !strings.Contains(order, "Core\r\nAddon\r\n-- Plain\r\nNeedy\r\nHelper") {
		t.Errorf("порядок загрузки в игре:\n%s", order)
	}
}

func TestMissingDependencyNotInStore(t *testing.T) {
	a, _, g := newGame(t)
	a.addArchive(writeZip(t, "dml.zip", dmlArchive))
	a.addArchive(modZip(t, "Needy", `return { require = { "Ghost" } }`))
	a.setGame(g)
	s := state(t, a)
	for _, i := range s.Issues {
		if strings.Contains(i.Title, "нужен мод Ghost") {
			if i.Command != "" || !strings.Contains(i.Detail, "нет в хранилище") {
				t.Errorf("замечание: %+v", i)
			}
			return
		}
	}
	t.Errorf("нет замечания о недостающем моде: %s", issueTitles(s))
}

func TestCycleKeepsMods(t *testing.T) {
	a, _, g := newGame(t)
	a.addArchive(writeZip(t, "dml.zip", dmlArchive))
	a.addArchive(modZip(t, "A", `return { load_after = { "B" } }`))
	a.addArchive(modZip(t, "B", `return { load_after = { "A" } }`))
	a.setGame(g)
	s := state(t, a)
	if !strings.Contains(issueTitles(s), "Правила модов противоречат друг другу: A, B") {
		t.Errorf("цикл не показан: %s", issueTitles(s))
	}
	s, err := a.Sort()
	if err != nil || len(s.Mods) != 3 {
		t.Errorf("после сортировки с циклом: %d модов, %v", len(s.Mods), err)
	}
}

// Загрузчик AML сам расставляет моды: список следует его журналу, а
// замечания о нарушенном порядке не нужны.
func TestSortAutoLoader(t *testing.T) {
	a, _, g := newGame(t)
	aml := map[string]string{}
	for k, v := range dmlArchive {
		aml[k] = v
	}
	aml["mods/base/mod_manager.lua"] = "local function aml_hook_load_order(ModManager)\n\tlocal after_presets = { Gamma = { \"Alpha\" } };\nend"
	a.addArchive(writeZip(t, "aml.zip", aml))
	for _, name := range []string{"Alpha", "Beta", "Gamma", "New"} {
		a.addArchive(modZip(t, name, `return {}`))
	}
	a.setGame(g)
	os.WriteFile(filepath.Join(g, "mods", "auto_mod_loader_log.txt"), []byte("Final load order:\n1:\"dmf\n2:\"Beta\n3:\"Alpha\n4:\"Gamma (\"1.0)\n"), 0o644)

	s := state(t, a)
	if !strings.Contains(s.OrderNote, "задаёт загрузчик модов") || !strings.Contains(s.OrderNote, "последнего запуска") {
		t.Errorf("пояснение: %q", s.OrderNote)
	}
	if strings.Contains(issueTitles(s), "нарушает") {
		t.Errorf("при AML показано нарушение порядка: %s", issueTitles(s))
	}
	plan, err := a.SortPreview()
	if err != nil || !plan.Auto {
		t.Fatalf("план: %+v, %v", plan, err)
	}
	s, err = a.Sort()
	if err != nil {
		t.Fatal(err)
	}
	// Как в журнале: Beta, Alpha, Gamma; новый мод — за своим прежним соседом.
	if ids(s) != "aml,beta,alpha,gamma,new" {
		t.Errorf("порядок по журналу загрузчика: %s", ids(s))
	}
}
