package manager

import (
	"strings"
	"testing"
)

func conflictFor(t *testing.T, a *Manager, key string) ConflictInfo {
	t.Helper()
	c, err := a.Conflict(key)
	if err != nil {
		list, _ := a.Conflicts()
		t.Fatalf("конфликта %q нет: %v; есть %+v", key, err, list)
	}
	return c
}

func TestConflictAnalysis(t *testing.T) {
	a, _, g := newGame(t)
	if _, err := a.setGame(g); err != nil {
		t.Fatal(err)
	}
	add := func(name string, files map[string]string) {
		t.Helper()
		if _, err := a.addArchive(writeZip(t, name+".zip", files)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Основа и два мода поверх неё: один перекрывает её файл, другой кладёт такой же.
	add("Core", map[string]string{"Core/Core.mod": "return {}", "Core/a.lua": "core a", "Core/b.lua": "core b"})
	add("Patch", map[string]string{"mods/Patch/Patch.mod": `return { load_after = { "Core" } }`, "mods/Core/a.lua": "patched a"})
	add("Twin", map[string]string{"mods/Twin/Twin.mod": "return {}", "mods/Core/b.lua": "core b"})
	// Мод, от которого в игре ничего не останется: его единственный файл перекрыт.
	add("Solo", map[string]string{"mods/Solo/Solo.mod": "solo"})
	add("Over", map[string]string{"mods/Over/Over.mod": "return {}", "mods/Solo/Solo.mod": "over", "mods/Over/x.lua": "x"})
	// Два мода с общими файлами без всяких подсказок.
	add("Left", map[string]string{"mods/Left/Left.mod": "return {}", "mods/Left/own.lua": "l", "mods/Shared/s.lua": "left"})
	add("Right", map[string]string{"mods/Right/Right.mod": "return {}", "mods/Right/own.lua": "r", "mods/Shared/s.lua": "right"})

	list, err := a.Conflicts()
	if err != nil || len(list) != 4 {
		t.Fatalf("конфликты: %+v, %v", list, err)
	}

	// Автор патча указал, что грузится после основы: совет — выбрать патч.
	c := conflictFor(t, a, "core|patch")
	if c.Kind != ConflictOrdered || c.Suggested != "patch" || c.Total != 1 || c.Resolved || !strings.Contains(c.Advice, "грузится после") {
		t.Errorf("порядок от автора: %+v", c)
	}
	if m := c.Mods[0]; m.ID != "core" || m.Files != 3 || m.Shared != 1 || m.Lost != 1 || m.Covered || m.Winner {
		t.Errorf("основа в конфликте с патчем: %+v", m)
	}

	// Одинаковые файлы — конфликт безвреден и внимания не требует.
	c = conflictFor(t, a, "core|twin")
	if c.Kind != ConflictIdentical || !c.Resolved || c.Pinned || !strings.Contains(c.Advice, "безвреден") {
		t.Errorf("одинаковые файлы: %+v", c)
	}

	// Мод перекрыт целиком: совет — выключить его.
	c = conflictFor(t, a, "solo|over")
	if c.Kind != ConflictCovered || c.Disable != "solo" || !c.Mods[0].Covered || c.Mods[1].Covered || !strings.Contains(c.Advice, "перекрыт целиком") {
		t.Errorf("перекрытый мод: %+v", c)
	}

	// Частичное пересечение: доли файлов названы, победителя программа не навязывает.
	c = conflictFor(t, a, "left|right")
	if c.Kind != ConflictOverlap || c.Suggested != "" || c.Disable != "" || !strings.Contains(c.Advice, "«Left» — 1 из 3") {
		t.Errorf("частичное пересечение: %+v", c)
	}

	// В «Требуют внимания» — только то, что ждёт решения: безвредный конфликт туда не идёт.
	s := state(t, a)
	n := 0
	for _, i := range s.Issues {
		if i.Command == "ChooseWinner" {
			n++
			if i.Arg == "core|twin" {
				t.Error("безвредный конфликт попал в замечания")
			}
			if i.Detail == "" || i.Action != "Разобрать конфликт" {
				t.Errorf("замечание без совета: %+v", i)
			}
		}
	}
	if n != 3 || statusValue(s, "Конфликты") != "4 · ждут решения: 3" {
		t.Errorf("замечаний о конфликтах %d, строка состояния %q", n, statusValue(s, "Конфликты"))
	}

	// Решения: выбрать рекомендованного, выключить перекрытый мод.
	if _, err := a.SetWinner("core|patch", "patch"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetEnabled("solo", false); err != nil {
		t.Fatal(err)
	}
	s = state(t, a)
	if statusValue(s, "Конфликты") != "3 · ждут решения: 1" {
		t.Errorf("после двух решений: %q", statusValue(s, "Конфликты"))
	}
	if c = conflictFor(t, a, "core|patch"); !c.Pinned || !c.Resolved {
		t.Errorf("решённый конфликт: %+v", c)
	}
}

func TestConflictDuplicate(t *testing.T) {
	a, _, g := newGame(t)
	a.setGame(g)
	a.addArchive(writeZip(t, "Alpha.zip", map[string]string{"Shared/Shared.mod": "alpha", "Shared/a.lua": "alpha"}))
	a.addArchive(writeZip(t, "Beta.zip", map[string]string{"Shared/Shared.mod": "beta", "Shared/a.lua": "beta"}))
	c := conflictFor(t, a, "alpha|beta")
	if c.Kind != ConflictDuplicate || c.Disable != "alpha" || c.Suggested != "beta" || !strings.Contains(c.Advice, "одну папку «Shared»") {
		t.Errorf("два варианта одного мода: %+v", c)
	}
}
