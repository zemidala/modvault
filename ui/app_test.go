package ui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func findMod(t *testing.T, s State, id string) Mod {
	t.Helper()
	for _, m := range s.Mods {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("мод %q не найден", id)
	return Mod{}
}

func TestInitialState(t *testing.T) {
	s := NewApp().State()

	if !s.Demo {
		t.Error("демонстрационные данные не помечены")
	}
	if !strings.HasPrefix(s.Version, "modvault ") {
		t.Errorf("версия = %q", s.Version)
	}
	if len(s.Mods) != 6 {
		t.Fatalf("модов %d, want 6", len(s.Mods))
	}
	if len(s.Issues) != 2 {
		t.Errorf("замечаний %d, want 2: %+v", len(s.Issues), s.Issues)
	}
	if s.PlanTitle != "План развёртывания: 1 изменение" || len(s.Plan) != 1 {
		t.Errorf("план = %q %v", s.PlanTitle, s.Plan)
	}

	want := map[string]Level{
		"dmf": LevelOK, "scoreboard": LevelWarn, "healthbars": LevelError, "spidey_sense": LevelOff,
	}
	for id, level := range want {
		if m := findMod(t, s, id); m.Level != level {
			t.Errorf("%s: уровень %q (%s), want %q", id, m.Level, m.State, level)
		}
	}
	if checks := s.Status[len(s.Status)-1]; checks.Value != "2 замечания" {
		t.Errorf("проверки = %q", checks.Value)
	}
}

func TestSetEnabled(t *testing.T) {
	a := NewApp()

	// Включаем обратно мод, который ещё лежит в игре: план пустеет.
	s, err := a.SetEnabled("spidey_sense", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Plan) != 0 || s.PlanTitle != "" {
		t.Errorf("план после возврата мода = %q %v", s.PlanTitle, s.Plan)
	}
	if m := findMod(t, s, "spidey_sense"); m.State != "Развёрнут" {
		t.Errorf("состояние = %q", m.State)
	}

	// Выключаем одного из участников конфликта: замечание о конфликте уходит.
	s, err = a.SetEnabled("numeric_ui", false)
	if err != nil {
		t.Fatal(err)
	}
	if m := findMod(t, s, "healthbars"); m.Level != LevelOK {
		t.Errorf("healthbars после выключения соперника: %q", m.State)
	}
	if len(s.Issues) != 1 {
		t.Errorf("замечаний %d, want 1: %+v", len(s.Issues), s.Issues)
	}
	if len(s.Plan) != 1 || !strings.Contains(s.Plan[0], "Numeric UI") {
		t.Errorf("план = %v", s.Plan)
	}

	if a.State().PlanTitle != s.PlanTitle {
		t.Error("State и SetEnabled расходятся")
	}
}

func TestSetEnabledErrors(t *testing.T) {
	a := NewApp()
	if _, err := a.SetEnabled("dmf", false); err == nil {
		t.Error("закреплённый мод выключился")
	}
	if _, err := a.SetEnabled("нет_такого", true); err == nil {
		t.Error("несуществующий мод включился")
	}
	if m := findMod(t, a.State(), "dmf"); !m.Enabled {
		t.Error("ошибка изменила состояние")
	}
}

func TestPlural(t *testing.T) {
	tests := map[int]string{0: "модов", 1: "мод", 2: "мода", 4: "мода", 5: "модов", 11: "модов", 12: "модов", 21: "мод", 22: "мода", 111: "модов"}
	for n, want := range tests {
		if got := plural(n, "мод", "мода", "модов"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
}

// Всё, на что ссылаются страница и стили, должно быть вшито в программу:
// окно работает без интернета.
func TestAssets(t *testing.T) {
	assets := Assets()
	ref := regexp.MustCompile(`(?:href|src)="([^"#]+)"|url\("?([^")]+)"?\)`)

	for _, name := range []string{"index.html", "app.css"} {
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ref.FindAllStringSubmatch(string(data), -1) {
			target := m[1] + m[2]
			if strings.HasPrefix(target, "#") {
				continue // ссылка внутри самой страницы
			}
			if strings.Contains(target, "://") {
				t.Errorf("%s ссылается на внешний адрес %s", name, target)
				continue
			}
			if _, err := fs.Stat(assets, target); err != nil {
				t.Errorf("%s ссылается на %s, которого нет: %v", name, target, err)
			}
		}
	}
}
