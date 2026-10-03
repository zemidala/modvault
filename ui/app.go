// Package ui — Go-сторона окна программы: то, что страница может запросить
// у программы. Пока ядро не подключено, отдаёт демонстрационные данные.
package ui

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sync"

	"github.com/zemidala/modvault/internal/version"
)

//go:embed all:frontend
var frontend embed.FS

// Assets возвращает файлы страницы, вшитые в программу.
func Assets() fs.FS {
	sub, err := fs.Sub(frontend, "frontend")
	if err != nil {
		panic(err) // папка вшита при сборке, ошибка невозможна
	}
	return sub
}

// Level — степень внимания, которую требует строка: от неё зависит цвет.
type Level string

const (
	LevelOK    Level = "ok"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
	LevelOff   Level = "off"
)

// State — всё, что показывает главное окно.
type State struct {
	Version   string       `json:"version"`
	Demo      bool         `json:"demo"`
	Game      string       `json:"game"`
	Profile   string       `json:"profile"`
	Status    []StatusItem `json:"status"`
	Issues    []Issue      `json:"issues"`
	Mods      []Mod        `json:"mods"`
	PlanTitle string       `json:"planTitle"`
	Plan      []string     `json:"plan"`
}

// StatusItem — один пункт строки состояния под шапкой.
type StatusItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Level Level  `json:"level"`
}

// Issue — замечание в блоке «Требуют внимания».
type Issue struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Level  Level  `json:"level"`
	Action string `json:"action"`
	// Stage — этап плана, на котором действие заработает.
	Stage int `json:"stage"`
}

// Mod — строка списка и карточка мода.
type Mod struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Available string `json:"available"`
	Source    string `json:"source"`
	Files     int    `json:"files"`
	DependsOn string `json:"dependsOn"`
	Enabled   bool   `json:"enabled"`
	Pinned    bool   `json:"pinned"`
	State     string `json:"state"`
	Level     Level  `json:"level"`
}

// App — объект, методы которого доступны странице.
type App struct {
	ctx  context.Context
	mu   sync.Mutex
	mods []demoMod
}

func NewApp() *App {
	return &App{mods: demoMods()}
}

// Startup вызывается при открытии окна.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// State возвращает текущее состояние окна.
func (a *App) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state()
}

// SetEnabled включает или выключает мод в профиле и возвращает новое состояние.
func (a *App) SetEnabled(id string, enabled bool) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.mods {
		m := &a.mods[i]
		if m.id != id {
			continue
		}
		if m.pinned && !enabled {
			return State{}, fmt.Errorf("%s нужен остальным модам, его нельзя выключить", m.name)
		}
		m.enabled = enabled
		return a.state(), nil
	}
	return State{}, fmt.Errorf("мод %q не найден", id)
}

func (a *App) state() State {
	s := State{
		Version: version.String(),
		Demo:    true,
		Game:    "Darktide · Steam",
		Profile: "Основной",
		Issues:  []Issue{},
		Mods:    make([]Mod, 0, len(a.mods)),
		Plan:    []string{},
	}

	enabled := make(map[string]bool, len(a.mods))
	for _, m := range a.mods {
		enabled[m.id] = m.enabled
	}

	for _, m := range a.mods {
		conflict := m.enabled && m.conflictWith != "" && enabled[m.conflictWith]
		text, level := modState(m, conflict)
		s.Mods = append(s.Mods, Mod{
			ID: m.id, Name: m.name, Version: m.version, Available: m.available,
			Source: m.source, Files: m.files, DependsOn: m.dependsOn,
			Enabled: m.enabled, Pinned: m.pinned, State: text, Level: level,
		})

		if conflict {
			winner := a.name(m.conflictWith)
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
	s.Status = a.status(s)
	return s
}

func (a *App) status(s State) []StatusItem {
	files := StatusItem{Label: "Файлы в игре", Value: "совпадают с профилем", Level: LevelOK}
	if len(s.Plan) > 0 {
		files.Value, files.Level = "ждут развёртывания", LevelWarn
	}
	checks := StatusItem{Label: "Проверки", Value: "замечаний нет", Level: LevelOK}
	if n := len(s.Issues); n > 0 {
		checks.Value = fmt.Sprintf("%d %s", n, plural(n, "замечание", "замечания", "замечаний"))
		checks.Level = LevelWarn
	}
	return []StatusItem{
		{Label: "Игра", Value: s.Game, Level: LevelOK},
		{Label: "Патч загрузчика", Value: "установлен", Level: LevelOK},
		files,
		checks,
	}
}

func (a *App) name(id string) string {
	for _, m := range a.mods {
		if m.id == id {
			return m.name
		}
	}
	return id
}

// modState описывает состояние мода одной фразой для списка.
func modState(m demoMod, conflict bool) (string, Level) {
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

// plural выбирает форму слова по правилам русского языка: 1 мод, 2 мода, 5 модов.
func plural(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}
