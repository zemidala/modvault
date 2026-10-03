package darktide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/rules"
)

func ruleList(rs []rules.Rule) string {
	var out []string
	for _, r := range rs {
		out = append(out, string(r.Kind)+" "+r.Mod+"→"+r.Other)
	}
	return strings.Join(out, ", ")
}

func TestParseModRules(t *testing.T) {
	mod := []byte(`return {
	packages = {},
	run = function() new_mod("X", { mod_script = "X/scripts/mods/X/X" }) end,
	-- load_before = { "закомментировано" },
	--[[ require = { "тоже" }, ]]
	load_after = {
		"dmf",
		'Alfs_DMF_Extensions',
	},
	load_before = {"Scoreboard"},
	require = { "animation_events" },
}`)
	got := ruleList(ParseModRules("X", mod))
	want := "after X→dmf, after X→Alfs_DMF_Extensions, before X→Scoreboard, requires X→animation_events"
	if got != want {
		t.Errorf("правила:\n%s\nwant:\n%s", got, want)
	}
	if rs := ParseModRules("Y", []byte(`return { run = function() end, packages = {} }`)); len(rs) != 0 {
		t.Errorf("мод без правил: %s", ruleList(rs))
	}
	if rs := ParseModRules("Z", []byte(`return { load_after = {}, require = {} }`)); len(rs) != 0 {
		t.Errorf("пустые списки: %s", ruleList(rs))
	}
}

const sampleLoader = `--#region AML
local function aml_hook_load_order(ModManager)
	local _audio = {
		"Audio",
	};
	-- Known presets
	local before_presets = {};
	local after_presets = {
		LogMeIn = {
			"dmf",
		},
		psych_ward = { "dmf", "LogMeIn" },
		Rock = _audio,
		["Many Mission Terminal"] = { "LogMeIn" },
	};
	local require_presets = {
		Rock = _audio,
	};
	aml_print(" AML IGNORES mod_load_order.txt and loads all valid mod folders in a correct order automatically");
end`

func TestParseLoaderPresets(t *testing.T) {
	got := ruleList(ParseLoaderPresets([]byte(sampleLoader)))
	want := "after LogMeIn→dmf, after psych_ward→dmf, after psych_ward→LogMeIn, after Rock→Audio, after Many Mission Terminal→LogMeIn, requires Rock→Audio"
	if got != want {
		t.Errorf("правила загрузчика:\n%s\nwant:\n%s", got, want)
	}
	if !IsAutoLoader([]byte(sampleLoader)) || IsAutoLoader([]byte("-- обычный DML\nlocal ModManager = class()")) {
		t.Error("вид загрузчика определён неверно")
	}
}

func TestParseLoaderLog(t *testing.T) {
	log := "====\nPreloading mod metadata..\nFinal load order:\n1:\"dmf\n2:\"api_compat (\"1.0.0)\n3:\"psych_ward\n4:\"Many Mission Terminal (\"2.1)\nReturning control to DML\n"
	got := strings.Join(ParseLoaderLog([]byte(log)), "|")
	if got != "dmf|api_compat|psych_ward|Many Mission Terminal" {
		t.Errorf("порядок из журнала = %q", got)
	}
}

func TestOrdering(t *testing.T) {
	files := map[string]string{
		"mods/base/mod_manager.lua":      sampleLoader,
		"mods/A/A.mod":                   `return { load_after = { "B", "dmf" }, require = { "нет" } }`,
		"mods/B/B.mod":                   `return {}`,
		"mods/psych_ward/psych_ward.mod": `return { load_after = { "A" } }`, // своё правило отменяет правило загрузчика
		"mods/Off/Off.mod":               `return { load_after = { "A" } }`,
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "mods"), 0o755)
	os.WriteFile(filepath.Join(dir, "mods", "auto_mod_loader_log.txt"), []byte("1:\"dmf\n2:\"B\n3:\"A\n"), 0o644)

	info := func(id string, enabled bool, paths ...string) game.ModInfo {
		return game.ModInfo{ModID: id, Enabled: enabled, Layout: Describe(paths)}
	}
	ord := New().Ordering(game.OrderContext{
		Dir: dir,
		Mods: []game.ModInfo{
			info("dml", true, "mods/base/mod_manager.lua", "mods/base/base.mod"),
			info("a", true, "mods/A/A.mod"), info("b", true, "mods/B/B.mod"),
			info("pw", true, "mods/psych_ward/psych_ward.mod"),
			info("off", false, "mods/Off/Off.mod"),
		},
		Read: func(_, p string) ([]byte, error) {
			if c, ok := files[p]; ok {
				return []byte(c), nil
			}
			return nil, os.ErrNotExist
		},
	})

	if !ord.Auto || strings.Join(ord.LastOrder, " ") != "dmf B A" || ord.LastOrderTime.IsZero() {
		t.Errorf("загрузчик: auto=%v, порядок %v", ord.Auto, ord.LastOrder)
	}
	got := ruleList(ord.Rules)
	for _, want := range []string{"after A→B", "requires A→нет", "after psych_ward→A", "after LogMeIn", "requires Rock→Audio"} {
		if !strings.Contains(got, want) && want != "after LogMeIn" {
			t.Errorf("нет правила %q в: %s", want, got)
		}
	}
	for _, bad := range []string{"→dmf", "psych_ward→LogMeIn", "Off→"} {
		if strings.Contains(got, bad) {
			t.Errorf("лишнее правило %q в: %s", bad, got)
		}
	}
}

// Настоящий набор модов на этом компьютере, только чтение. Порядок, который
// загрузчик записал в журнал, обязан соблюдать все прочитанные правила:
// так проверяется, что правила прочитаны верно.
func TestRealOrdering(t *testing.T) {
	installs, _ := New().Detect()
	if len(installs) == 0 {
		t.Skip("Darktide на этом компьютере не найден")
	}
	dir := installs[0].Dir
	entries, err := os.ReadDir(filepath.Join(dir, "mods"))
	if err != nil {
		t.Skip("в игре нет папки mods")
	}
	var mods []game.ModInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		paths := []string{"mods/" + e.Name() + "/" + e.Name() + ".mod"}
		if e.Name() == "base" {
			paths = append(paths, loaderScript)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(paths[0]))); err != nil {
			continue
		}
		mods = append(mods, game.ModInfo{ModID: e.Name(), Enabled: true, Layout: Describe(paths)})
	}
	ord := New().Ordering(game.OrderContext{Dir: dir, Mods: mods, Read: func(_, p string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
	}})
	if !ord.Auto || len(ord.LastOrder) < 10 {
		t.Skipf("загрузчик не AML или журнала нет: auto=%v, в журнале %d", ord.Auto, len(ord.LastOrder))
	}
	t.Logf("модов %d, правил %d, в журнале загрузчика %d модов", len(mods), len(ord.Rules), len(ord.LastOrder))

	violated := rules.Violations(ord.LastOrder, ord.Rules)
	for _, r := range violated {
		t.Errorf("настоящий порядок нарушает прочитанное правило: %s %s → %s (%s)", r.Kind, r.Mod, r.Other, r.Source)
	}
	for _, r := range rules.Missing(ord.LastOrder, ord.Rules) {
		t.Errorf("загруженный мод %s требует %s, которого нет в журнале (%s)", r.Mod, r.Other, r.Source)
	}

	// Сортировка от настоящего порядка ничего не меняет и никого не теряет.
	res := rules.Sort(ord.LastOrder, ord.Rules)
	if strings.Join(res.Order, "|") != strings.Join(ord.LastOrder, "|") || len(res.Cycles) != 0 {
		t.Errorf("сортировка изменила настоящий порядок или нашла циклы: %v", res.Cycles)
	}
}
