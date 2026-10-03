package manager

import (
	"fmt"

	"github.com/zemidala/modvault/internal/version"
)

// demoMod — мод из демонстрационного набора. Набор показывается, пока
// хранилище пусто: по нему видно, как окно выглядит с модами, обновлениями
// и конфликтами, до которых ядро ещё не доросло.
type demoMod struct {
	id, name     string
	version      string
	available    string // новая версия на Nexus, если есть
	source       string
	files        int
	versions     int
	dependsOn    string
	conflictWith string // id мода, с которым делит файл
	enabled      bool   // включён в профиле
	deployed     bool   // сейчас лежит в игре
	pinned       bool   // выключить нельзя
}

func demoMods() []demoMod {
	const dmf = "Darktide Mod Framework"
	return []demoMod{
		{id: "dmf", name: dmf, version: "25.03", source: "Nexus Mods", files: 212, versions: 1, enabled: true, deployed: true, pinned: true},
		{id: "animation_events", name: "animation_events", version: "1.0.2", source: "Nexus Mods", files: 3, versions: 1, dependsOn: dmf, enabled: true, deployed: true},
		{id: "scoreboard", name: "Scoreboard", version: "1.4.0", available: "1.5.0", source: "Nexus Mods", files: 14, versions: 2, dependsOn: dmf, enabled: true, deployed: true},
		{id: "numeric_ui", name: "Numeric UI", version: "2.1.0", source: "Nexus Mods", files: 9, versions: 1, dependsOn: dmf, enabled: true, deployed: true},
		{id: "healthbars", name: "Healthbars", version: "1.2.1", source: "Nexus Mods", files: 6, versions: 1, dependsOn: dmf, conflictWith: "numeric_ui", enabled: true, deployed: true},
		{id: "spidey_sense", name: "Spidey Sense", version: "1.1.0", source: "Архив с диска", files: 5, versions: 1, dependsOn: dmf, enabled: false, deployed: true},
	}
}

func (a *Manager) setDemoEnabled(id string, enabled bool) error {
	for i := range a.demo {
		m := &a.demo[i]
		if m.id != id {
			continue
		}
		if m.pinned && !enabled {
			return fmt.Errorf("%s нужен остальным модам, его нельзя выключить", m.name)
		}
		m.enabled = enabled
		return nil
	}
	return fmt.Errorf("мод %q не найден", id)
}

func (a *Manager) demoName(id string) string {
	for _, m := range a.demo {
		if m.id == id {
			return m.name
		}
	}
	return id
}

func (a *Manager) demoState() State {
	s := State{
		Version: version.String(),
		Demo:    true,
		Home:    a.home,
		Profile: mainProfile,
		Issues:  []Issue{},
		Mods:    make([]Mod, 0, len(a.demo)),
		Plan:    []string{},
	}

	enabled := make(map[string]bool, len(a.demo))
	for _, m := range a.demo {
		enabled[m.id] = m.enabled
	}

	for _, m := range a.demo {
		conflict := m.enabled && m.conflictWith != "" && enabled[m.conflictWith]
		text, level := demoModState(m, conflict)
		s.Mods = append(s.Mods, Mod{
			ID: m.id, Name: m.name, Version: m.version, Available: m.available,
			Source: m.source, Files: m.files, Versions: m.versions, DependsOn: m.dependsOn,
			Enabled: m.enabled, Pinned: m.pinned, State: text, Level: level,
		})

		if conflict {
			winner := a.demoName(m.conflictWith)
			s.Issues = append(s.Issues, Issue{
				Title:  fmt.Sprintf("%s и %s меняют один и тот же файл", m.name, winner),
				Detail: "Конфликт файлов · сейчас побеждает " + winner,
				Level:  LevelError, Action: "Выбрать победителя", Stage: 3,
			})
		}
		if m.enabled && m.available != "" {
			s.Issues = append(s.Issues, Issue{
				Title:  fmt.Sprintf("%s: доступна версия %s", m.name, m.available),
				Detail: "Обновление · установлена " + m.version,
				Level:  LevelWarn, Action: "Обновить", Stage: 7,
			})
		}

		switch {
		case m.enabled && !m.deployed:
			s.Plan = append(s.Plan, m.name+" будет развёрнут в игру")
		case !m.enabled && m.deployed:
			s.Plan = append(s.Plan, m.name+" будет убран из игры")
		}
	}

	if n := len(s.Plan); n > 0 {
		s.PlanTitle = fmt.Sprintf("План развёртывания: %d %s", n, plural(n, "изменение", "изменения", "изменений"))
	}

	files := StatusItem{Label: "Файлы в игре", Value: "совпадают с профилем", Level: LevelOK}
	if len(s.Plan) > 0 {
		files.Value, files.Level = "ждут развёртывания", LevelWarn
	}
	checks := StatusItem{Label: "Проверки", Value: "замечаний нет", Level: LevelOK}
	if n := len(s.Issues); n > 0 {
		checks.Value = fmt.Sprintf("%d %s", n, plural(n, "замечание", "замечания", "замечаний"))
		checks.Level = LevelWarn
	}
	s.Status = []StatusItem{
		{Label: "Игра", Value: "Darktide · Steam", Level: LevelOK},
		{Label: "Патч загрузчика", Value: "установлен", Level: LevelOK},
		files,
		checks,
	}
	return s
}

// demoModState описывает состояние мода одной фразой для списка.
func demoModState(m demoMod, conflict bool) (string, Level) {
	switch {
	case conflict:
		return "Конфликт файлов", LevelError
	case m.enabled && !m.deployed:
		return "Ждёт развёртывания", LevelWarn
	case !m.enabled && m.deployed:
		return "Выключен, ждёт развёртывания", LevelOff
	case !m.enabled:
		return "Выключен", LevelOff
	case m.available != "":
		return "Есть обновление", LevelWarn
	}
	return "Развёрнут", LevelOK
}
