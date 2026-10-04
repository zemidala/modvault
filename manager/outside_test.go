package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zemidala/modvault/core/fsx"
)

// outsideGame — игра с развёрнутым набором и тремя папками вне Modvault:
// мод, положенный вручную, ссылка на папку проекта и пустая папка от Vortex.
func outsideGame(t *testing.T) (a *Manager, game, project string) {
	t.Helper()
	a, _, game = newGame(t)
	a.addArchive(writeZip(t, "dml.zip", dmlArchive))
	a.setGame(game)
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	write := func(rel, data string) {
		p := filepath.Join(game, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(data), 0o644)
	}
	write("mods/manual_mod/manual_mod.mod", "return {}")
	write("mods/manual_mod/scripts/manual_mod.lua", "-- ручной мод")
	write("mods/OldMod/__folder_managed_by_vortex", "")
	write("mods/OldMod/scripts/__folder_managed_by_vortex", "")

	project = filepath.Join(t.TempDir(), "devmod")
	os.MkdirAll(project, 0o755)
	os.WriteFile(filepath.Join(project, "devmod.mod"), []byte("return {}"), 0o644)
	if err := fsx.LinkDir(project, filepath.Join(game, "mods", "devmod")); err != nil {
		t.Fatal(err)
	}
	return a, game, project
}

func outsideRow(s State, folder string) (Mod, bool) {
	for _, m := range s.Mods {
		if m.ID == outsidePrefix+folder {
			return m, true
		}
	}
	return Mod{}, false
}

// Папки вне Modvault видны в списке, а пустые папки Vortex — замечанием.
func TestOutsideFolders(t *testing.T) {
	a, game, project := outsideGame(t)
	s := state(t, a)
	manual, ok := outsideRow(s, "manual_mod")
	if !ok || manual.Outside != outsideManual || !manual.Enabled {
		t.Errorf("ручной мод: %+v, %v", manual, ok)
	}
	link, ok := outsideRow(s, "devmod")
	if !ok || link.Outside != outsideLink || !fsx.SamePath(link.Link, project) {
		t.Errorf("ссылка: %+v, %v", link, ok)
	}
	if _, ok := outsideRow(s, "OldMod"); ok {
		t.Error("пустая папка Vortex показана как мод")
	}
	if !strings.Contains(issueTitles(s), "1 пустая папка от Vortex") {
		t.Errorf("замечания: %s", issueTitles(s))
	}
	// Своё Modvault за чужое не принимает.
	for _, m := range s.Mods {
		if m.Outside != "" && !strings.HasPrefix(m.ID, outsidePrefix) {
			t.Errorf("мод хранилища помечен как чужой: %+v", m)
		}
	}
	if _, err := a.CleanLeftovers(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(game, "mods", "OldMod")); !os.IsNotExist(err) {
		t.Errorf("пустая папка Vortex осталась: %v", err)
	}
	if _, err := os.Stat(filepath.Join(game, "mods", "manual_mod", "manual_mod.mod")); err != nil {
		t.Errorf("уборка задела ручной мод: %v", err)
	}
}

// Ручной мод переходит в хранилище и остаётся в игре теми же файлами.
func TestTakeOutside(t *testing.T) {
	a, game, _ := outsideGame(t)
	before := snapshot(t, filepath.Join(game, "mods", "manual_mod"))
	res, err := a.TakeOutside(outsidePrefix + "manual_mod")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := outsideRow(res.State, "manual_mod"); ok {
		t.Error("мод всё ещё вне Modvault")
	}
	m := findMod(t, res.State, "manual_mod")
	if !m.Enabled || m.State != "Развёрнут" || res.State.PlanTitle != "" {
		t.Errorf("после переноса: %+v, план %q %v", m, res.State.PlanTitle, res.State.Plan)
	}
	if after := snapshot(t, filepath.Join(game, "mods", "manual_mod")); len(after) != len(before) || after["manual_mod.mod"] != before["manual_mod.mod"] || after["scripts/manual_mod.lua"] != before["scripts/manual_mod.lua"] {
		t.Errorf("файлы в игре: было %v, стало %v", before, after)
	}
	// Теперь мод выключается, как любой другой.
	if _, err := a.SetEnabled("manual_mod", false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(game, "mods", "manual_mod", "manual_mod.mod")); !os.IsNotExist(err) {
		t.Errorf("выключенный мод остался в игре: %v", err)
	}
	if _, err := a.TakeOutside(outsidePrefix + "manual_mod"); err == nil {
		t.Error("взят мод, которого вне Modvault уже нет")
	}
}

// Ссылка на папку проекта становится модом в разработке: его выключают и
// включают, а папка проекта не страдает.
func TestLinkOutside(t *testing.T) {
	a, game, project := outsideGame(t)
	link := filepath.Join(game, "mods", "devmod")
	res, err := a.LinkOutside(outsidePrefix + "devmod")
	if err != nil {
		t.Fatal(err)
	}
	m := findMod(t, res.State, "devmod")
	if !m.Enabled || m.State != "Развёрнут" || !fsx.SamePath(m.Link, project) || res.State.PlanTitle != "" {
		t.Errorf("после подключения: %+v, план %v", m, res.State.Plan)
	}

	s, err := a.SetEnabled("devmod", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(s.Plan, " | "), "devmod — убрать ссылку на папку проекта") || s.PlanTitle == "" {
		t.Errorf("план после выключения: %q %v", s.PlanTitle, s.Plan)
	}
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fsx.LinkTarget(link); ok {
		t.Error("ссылка осталась у выключенного мода")
	}
	if _, err := os.Stat(filepath.Join(project, "devmod.mod")); err != nil {
		t.Errorf("папка проекта пострадала: %v", err)
	}
	if _, ok := outsideRow(state(t, a), "devmod"); ok {
		t.Error("выключенный мод в разработке показан как чужой")
	}

	if _, err := a.SetEnabled("devmod", true); err != nil {
		t.Fatal(err)
	}
	res2, err := a.Deploy()
	if err != nil {
		t.Fatal(err)
	}
	if cur, ok := fsx.LinkTarget(link); !ok || !fsx.SamePath(cur, project) {
		t.Errorf("ссылка не вернулась: %q, %v; %q", cur, ok, res2.Message)
	}
	// Правка в проекте сразу видна в игре.
	os.WriteFile(filepath.Join(project, "new.lua"), []byte("-- новое"), 0o644)
	if data, err := os.ReadFile(filepath.Join(link, "new.lua")); err != nil || string(data) != "-- новое" {
		t.Errorf("правка проекта в игре: %q, %v", data, err)
	}

	// Удаление мода снимает ссылку, папка проекта остаётся.
	if _, err := a.RemoveMod("devmod", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := fsx.LinkTarget(link); ok {
		t.Error("ссылка осталась после удаления мода")
	}
	if _, err := os.Stat(filepath.Join(project, "new.lua")); err != nil {
		t.Errorf("удаление мода задело проект: %v", err)
	}
}

// Устаревшая запись о развёртывании не мешает: удалённый мод из неё выпадает,
// и обновление или перенос мода в хранилище всё равно доходят до игры.
func TestStaleDeployedProfile(t *testing.T) {
	a, game, _ := outsideGame(t)
	a.addArchive(writeZip(t, "Healthbars.zip", map[string]string{"mods/hb/hb.mod": "1"}))
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RemoveMod("healthbars", true); err != nil {
		t.Fatal(err)
	}
	deployed, ok := a.deployedProfile()
	if !ok || deployed.Index("healthbars") >= 0 {
		t.Fatalf("запись о развёртывании: %+v, %v", deployed.Entries, ok)
	}
	if _, err := a.TakeOutside(outsidePrefix + "manual_mod"); err != nil {
		t.Fatalf("перенос при устаревшей записи: %v", err)
	}
	if _, err := os.Stat(filepath.Join(game, asideDir)); !os.IsNotExist(err) {
		t.Errorf("временная папка осталась в игре: %v", err)
	}
}
