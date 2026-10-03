package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zemidala/modvault/game/darktide"
)

// vortexSetup повторяет устройство игры под Vortex: хранилище модов Vortex,
// жёсткие ссылки на его файлы в игре, учёт vortex.deployment.json, порядок
// загрузки от Vortex и базу бандлов, пропатченную dtkit-patch с .bak рядом.
type vortexSetup struct {
	game, staging string
	links         []string // пути в игре, которые должны быть ссылками на хранилище Vortex
}

const (
	srcDML      = "Darktide Mod Loader 19 26.06.24 2026-06-24T06-07Z ndQ1md9gG"
	srcAFK      = "AFK-33-23-4-05-1680675716"
	srcFlux     = "Flux 30 1.0 2026-01-01T00-00Z aaaa"
	srcOther    = "Other 31 2.0 2026-01-01T00-00Z bbbb"
	srcDisabled = "Disabled 40 1.0 2026-01-01T00-00Z cccc"
)

func newVortexSetup(t *testing.T, base string) *vortexSetup {
	t.Helper()
	v := &vortexSetup{game: filepath.Join(base, "Warhammer 40,000 DARKTIDE"), staging: filepath.Join(base, "warhammer40kdarktide")}
	write := func(path string, data []byte) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	staged := map[string]map[string]string{
		srcDML: {
			"mods/base/base.mod": "return {}", "binaries/mod_loader": "loader",
			"bundle/9ba626afa44a3aa3.patch_999": "бандл DML", "tools/dtkit-patch.exe": "patcher",
		},
		srcAFK:      {"mods/afk/afk.mod": "return {}", "mods/afk/scripts/afk.lua": "-- afk"},
		srcFlux:     {"mods/Flux/Flux.mod": "return {}", "mods/shared.lua": "от Flux"},
		srcOther:    {"mods/Other/Other.mod": "return {}", "mods/shared.lua": "от Other"},
		srcDisabled: {"mods/Disabled/Disabled.mod": "return {}"},
	}
	for src, files := range staged {
		for rel, content := range files {
			write(filepath.Join(v.staging, src, filepath.FromSlash(rel)), []byte(content))
		}
	}
	write(filepath.Join(v.staging, "__vortex_staging_folder"), []byte(`{"instance":"abc","game":"warhammer40kdarktide"}`))

	// Vortex развернул всё, кроме выключенного мода; в конфликте победил Other.
	type depFile struct {
		RelPath string `json:"relPath"`
		Source  string `json:"source"`
	}
	var files []depFile
	for src, list := range staged {
		if src == srcDisabled {
			continue
		}
		for rel := range list {
			if rel == "mods/shared.lua" && src == srcFlux {
				continue
			}
			files = append(files, depFile{strings.ReplaceAll(rel, "/", `\`), src})
			gameFile := filepath.Join(v.game, filepath.FromSlash(rel))
			os.MkdirAll(filepath.Dir(gameFile), 0o755)
			if err := os.Link(filepath.Join(v.staging, src, filepath.FromSlash(rel)), gameFile); err != nil {
				t.Skipf("жёсткие ссылки недоступны: %v", err)
			}
			v.links = append(v.links, rel)
		}
	}
	dep, _ := json.Marshal(map[string]any{"version": 1, "instance": "abc", "deploymentMethod": "hardlink_activator", "files": files})
	write(filepath.Join(v.game, "vortex.deployment.json"), dep)

	write(filepath.Join(v.game, "binaries", "Darktide.exe"), []byte("exe"))
	write(filepath.Join(v.game, "bundle", "bundle_database.data.bak"), cleanBundle)
	write(filepath.Join(v.game, "bundle", "bundle_database.data"), append(append([]byte(nil), cleanBundle...), patchMarker...))
	write(filepath.Join(v.game, "mods", "mod_load_order.txt"), []byte("-- File managed by Vortex mod manager\nOther\nFlux\nafk\n"))
	write(filepath.Join(v.game, "mods", "mod_load_order.txt.vortex_backup"), []byte("-- оригинал DML\n"))
	write(filepath.Join(v.game, "mods", "afk", "afk.mod.vortex_backup"), []byte("файл до Vortex"))
	write(filepath.Join(v.game, "mods", "afk", "__folder_managed_by_vortex"), []byte("{}"))
	return v
}

func (v *vortexSetup) checkLinks(t *testing.T) {
	t.Helper()
	deployed := map[string]string{}
	data, _ := os.ReadFile(filepath.Join(v.game, "vortex.deployment.json"))
	var dep struct {
		Files []struct{ RelPath, Source string } `json:"files"`
	}
	json.Unmarshal(data, &dep)
	for _, f := range dep.Files {
		deployed[strings.ReplaceAll(f.RelPath, `\`, "/")] = f.Source
	}
	for _, rel := range v.links {
		gi, err1 := os.Stat(filepath.Join(v.game, filepath.FromSlash(rel)))
		si, err2 := os.Stat(filepath.Join(v.staging, deployed[rel], filepath.FromSlash(rel)))
		if err1 != nil || err2 != nil || !os.SameFile(gi, si) {
			t.Errorf("%s не ссылается на хранилище Vortex", rel)
		}
	}
}

func adoptApp(t *testing.T) (*Manager, *vortexSetup, string) {
	t.Helper()
	base := t.TempDir()
	v := newVortexSetup(t, base)
	home := filepath.Join(base, "Modvault")
	a := NewAt(home)
	a.game = &darktide.Darktide{Patcher: fakePatcher}
	if _, err := a.setGame(v.game); err != nil {
		t.Fatal(err)
	}
	return a, v, home
}

func TestAdoptAndRelease(t *testing.T) {
	a, v, home := adoptApp(t)
	before := snapshot(t, v.game)
	v.checkLinks(t)

	s := state(t, a)
	if !strings.Contains(issueTitles(s), "Игрой управляет Vortex") {
		t.Fatalf("Vortex не замечен: %s", issueTitles(s))
	}
	if _, err := a.Deploy(); err == nil {
		t.Fatal("развёртывание поверх Vortex прошло")
	}

	// Пробный прогон ничего не меняет.
	rep, err := a.Adopt(true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mods != 5 || rep.Enabled != 4 || rep.Pinned != 1 || rep.Originals != 2 || len(rep.Problems) != 0 {
		t.Errorf("пробный отчёт: %+v", rep)
	}
	if !sameTree(snapshot(t, v.game), before) || len(state(t, a).Mods) != 0 {
		t.Fatal("пробный прогон что-то изменил")
	}

	// Усыновление: в игре меняется только учёт Vortex.
	if _, err := a.Adopt(false); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, v.game)
	if _, ok := after["vortex.deployment.json"]; ok {
		t.Error("учёт Vortex остался в игре")
	}
	delete(before, "vortex.deployment.json")
	if !sameTree(after, before) {
		t.Fatalf("усыновление изменило игру:\n%v\n---\n%v", after, before)
	}
	if !a.Adopted() {
		t.Error("Adopted = false после усыновления")
	}

	s = state(t, a)
	if s.Demo || len(s.Mods) != 5 {
		t.Fatalf("после усыновления: demo=%v, модов %d", s.Demo, len(s.Mods))
	}
	if ids(s) != "darktide_mod_loader,other,flux,afk,disabled" {
		t.Errorf("порядок = %s", ids(s))
	}
	if findMod(t, s, "disabled").Enabled || !findMod(t, s, "afk").Enabled {
		t.Error("включённость не совпала с Vortex")
	}
	if strings.Contains(issueTitles(s), "Vortex") || strings.Contains(issueTitles(s), "изменён") {
		t.Errorf("замечания после усыновления: %s", issueTitles(s))
	}
	// Вся раскладка Vortex принята как есть: меняется только порядок загрузки.
	if s.PlanTitle != "План развёртывания: 1 изменение" {
		files, _ := a.PlanFiles()
		t.Fatalf("план после усыновления: %q\n%s", s.PlanTitle, strings.Join(files, "\n"))
	}

	// Работа в Modvault: своё развёртывание, выключение, новый мод.
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(v.game, "mods", "mod_load_order.txt")); !strings.HasPrefix(got, "-- Файл собран Modvault") || !strings.Contains(got, "Other\r\nFlux\r\nafk") {
		t.Errorf("порядок загрузки:\n%s", got)
	}
	if got := readFile(t, filepath.Join(v.game, "mods", "shared.lua")); got != "от Other" {
		t.Errorf("закреплённый победитель Vortex не удержался: %q", got)
	}
	a.SetEnabled("afk", false)
	a.addArchive(writeZip(t, "Новый мод.zip", map[string]string{"New/New.mod": "return {}"}))
	if _, err := a.Deploy(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(v.game, "mods", "afk", "afk.mod")); got != "файл до Vortex" {
		t.Errorf("снятый мод не вернул сохранённый Vortex оригинал: %q", got)
	}

	// Возврат: игра в точности как её оставил Vortex.
	rel, err := a.Release(true)
	if err != nil || rel.Changes == 0 {
		t.Fatalf("пробный возврат: %+v, %v", rel, err)
	}
	if _, err := a.Release(false); err != nil {
		t.Fatal(err)
	}
	orig := snapshot(t, v.game)
	before["vortex.deployment.json"] = orig["vortex.deployment.json"]
	if !sameTree(orig, before) || orig["vortex.deployment.json"] == "" {
		t.Fatalf("после возврата игра не как у Vortex:\n%v\n---\n%v", orig, before)
	}
	v.checkLinks(t)
	if a.Adopted() {
		t.Error("Adopted = true после возврата")
	}

	// Моды остаются в хранилище Modvault, а Vortex снова управляет игрой.
	s = state(t, NewAt(home))
	if len(s.Mods) != 6 || !strings.Contains(issueTitles(s), "Игрой управляет Vortex") {
		t.Errorf("после возврата: %d модов, %s", len(s.Mods), issueTitles(s))
	}

	// Можно перенять снова — даже если мод в Vortex обновился без смены версии.
	afk := filepath.Join(v.staging, srcAFK, "mods", "afk", "scripts", "afk.lua")
	os.Remove(afk) // как при обновлении: новый файл, ссылка в игре — на старый
	os.WriteFile(afk, []byte("-- afk, новая сборка"), 0o644)
	if _, err := a.Adopt(false); err != nil {
		t.Fatalf("повторное усыновление: %v", err)
	}
	s = state(t, a)
	if m := findMod(t, s, "afk"); m.Versions != 2 {
		t.Errorf("обновлённая сборка не легла отдельной версией: %+v", m)
	}
}

func TestAdoptRefuses(t *testing.T) {
	a, v, _ := adoptApp(t)
	if _, err := a.Release(false); err == nil {
		t.Error("возврат без усыновления прошёл")
	}
	os.Remove(filepath.Join(v.staging, "__vortex_staging_folder"))
	if _, err := a.Adopt(false); err == nil {
		t.Error("усыновление без хранилища Vortex прошло")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
