// Package ui — Go-сторона окна программы: то, что страница может запросить
// у программы. Пока в хранилище нет ни одного мода, отдаёт демонстрационные
// данные.
package ui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
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

// mainProfile — профиль, с которым окно работает, пока нет выбора профилей.
const mainProfile = "Основной"

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
	Home      string       `json:"home"`
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
	// Action — подпись кнопки исправления; пустая — кнопки нет.
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
	Versions  int    `json:"versions"`
	DependsOn string `json:"dependsOn"`
	Enabled   bool   `json:"enabled"`
	Pinned    bool   `json:"pinned"`
	State     string `json:"state"`
	Level     Level  `json:"level"`
}

// App — объект, методы которого доступны странице.
type App struct {
	ctx context.Context
	mu  sync.Mutex

	home     string
	store    *store.Store
	profiles *profile.Dir
	openErr  error // почему не открылись хранилище или профили

	demo []demoMod
}

// DefaultHome возвращает папку данных программы. До этапа 4, когда программа
// научится находить игру и класть хранилище рядом с ней, это
// %LOCALAPPDATA%\Modvault; переменная MODVAULT_HOME задаёт другое место.
func DefaultHome() string {
	if home := os.Getenv("MODVAULT_HOME"); home != "" {
		return home
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "Modvault")
}

func NewApp() *App {
	return NewAppAt(DefaultHome())
}

// NewAppAt создаёт приложение с папкой данных home.
func NewAppAt(home string) *App {
	a := &App{home: home, demo: demoMods()}
	a.store, a.openErr = store.Open(home)
	if a.openErr == nil {
		a.profiles, a.openErr = profile.Open(filepath.Join(home, "profiles"))
	}
	return a
}

// Startup вызывается при открытии окна.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
}

// State возвращает текущее состояние окна.
func (a *App) State() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state()
}

// SetEnabled включает или выключает мод в профиле и возвращает новое состояние.
func (a *App) SetEnabled(id string, enabled bool) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	real, err := a.hasMods()
	if err != nil {
		return State{}, err
	}
	if !real {
		if err := a.setDemoEnabled(id, enabled); err != nil {
			return State{}, err
		}
		return a.state()
	}

	p, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	if err := p.SetEnabled(id, enabled); err != nil {
		return State{}, err
	}
	if err := a.profiles.Save(p); err != nil {
		return State{}, err
	}
	return a.state()
}

// AddMod спрашивает у пользователя архив и добавляет мод из него в хранилище
// и в профиль. Если пользователь отказался, состояние возвращается прежним.
func (a *App) AddMod() (State, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Добавить мод из архива",
		Filters: []runtime.FileFilter{{DisplayName: "Архивы модов (*.zip, *.7z, *.rar)", Pattern: "*.zip;*.7z;*.rar"}},
	})
	if err != nil {
		return State{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if path == "" {
		return a.state()
	}
	return a.addArchive(path)
}

func (a *App) addArchive(path string) (State, error) {
	if a.openErr != nil {
		return State{}, a.openErr
	}
	info := store.GuessInfo(path)
	v, err := a.store.Add(path, info)
	if errors.Is(err, fs.ErrExist) {
		return State{}, fmt.Errorf("«%s» этой версии уже есть в хранилище", info.Name)
	}
	if err != nil {
		return State{}, err
	}

	// Новая версия уже установленного мода занимает его место в профиле.
	p, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	if p.Index(v.ModID) >= 0 {
		err = p.SetVersion(v.ModID, v.ID)
	} else {
		err = p.Add(v.ModID, v.ID)
	}
	if err != nil {
		return State{}, err
	}
	if err := a.profiles.Save(p); err != nil {
		return State{}, err
	}
	return a.state()
}

// RemoveMod после подтверждения удаляет мод со всеми версиями в Корзину.
// Если Корзина недоступна, спрашивает, удалять ли насовсем.
func (a *App) RemoveMod(id string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	real, err := a.hasMods()
	if err != nil {
		return State{}, err
	}
	if !real {
		return State{}, errors.New("это демонстрационный мод: удалять нечего")
	}

	if !a.confirm("Удалить мод", fmt.Sprintf("Удалить «%s» из хранилища?\n\nВсе его версии уйдут в Корзину.", a.modName(id))) {
		return a.state()
	}
	err = a.removeMod(id, false)
	if errors.Is(err, fsx.ErrTrashUnavailable) {
		if !a.confirm("Корзина недоступна", "Положить мод в Корзину не удалось.\n\nУдалить его насовсем? Вернуть его будет нельзя.") {
			return a.state()
		}
		err = a.removeMod(id, true)
	}
	if err != nil {
		return State{}, err
	}
	return a.state()
}

func (a *App) confirm(title, message string) bool {
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type: runtime.QuestionDialog, Title: title, Message: message, DefaultButton: "No",
	})
	return err == nil && answer == "Yes"
}

func (a *App) removeMod(id string, permanent bool) error {
	if err := a.store.RemoveMod(id, permanent); err != nil {
		return err
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return err
	}
	if p.Index(id) < 0 {
		return nil
	}
	if err := p.Remove(id); err != nil {
		return err
	}
	return a.profiles.Save(p)
}

// ModFiles возвращает пути файлов той версии мода, что выбрана в профиле.
func (a *App) ModFiles(id string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	real, err := a.hasMods()
	if err != nil {
		return nil, err
	}
	if !real {
		return nil, errors.New("это демонстрационный мод: файлов у него нет")
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return nil, err
	}
	i := p.Index(id)
	if i < 0 {
		return nil, fmt.Errorf("мод %q не найден", id)
	}
	v, err := a.store.Get(id, p.Entries[i].VersionID)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(v.Files))
	for i, f := range v.Files {
		paths[i] = f.Path
	}
	return paths, nil
}

// hasMods сообщает, есть ли в хранилище настоящие моды. Пока их нет, окно
// показывает демонстрационные.
func (a *App) hasMods() (bool, error) {
	if a.openErr != nil {
		return false, a.openErr
	}
	mods, _, err := a.store.List()
	return len(mods) > 0, err
}

func (a *App) modName(id string) string {
	mods, _, _ := a.store.List()
	for _, m := range mods {
		if m.ID == id {
			return m.Latest().Name
		}
	}
	return id
}

// loadProfile читает основной профиль и приводит его в согласие с хранилищем:
// пропавшие моды убирает, новые ставит в конец, пропавшую версию заменяет
// последней. Изменённый профиль сохраняет.
func (a *App) loadProfile() (profile.Profile, []store.Mod, error) {
	mods, _, err := a.store.List()
	if err != nil {
		return profile.Profile{}, nil, err
	}
	p, err := a.profiles.Load(mainProfile)
	if errors.Is(err, fs.ErrNotExist) {
		p, err = profile.Profile{Name: mainProfile}, nil
	}
	if err != nil {
		return profile.Profile{}, nil, err
	}

	byID := make(map[string]store.Mod, len(mods))
	for _, m := range mods {
		byID[m.ID] = m
	}
	changed := false
	kept := p.Entries[:0]
	for _, e := range p.Entries {
		m, ok := byID[e.ModID]
		if !ok {
			changed = true
			continue
		}
		if !hasVersion(m, e.VersionID) {
			e.VersionID = m.Latest().ID
			changed = true
		}
		kept = append(kept, e)
	}
	p.Entries = kept
	for _, m := range mods {
		if p.Index(m.ID) < 0 {
			if err := p.Add(m.ID, m.Latest().ID); err != nil {
				return profile.Profile{}, nil, err
			}
			changed = true
		}
	}
	if changed {
		if err := a.profiles.Save(p); err != nil {
			return profile.Profile{}, nil, err
		}
	}
	return p, mods, nil
}

func hasVersion(m store.Mod, versionID string) bool {
	for _, v := range m.Versions {
		if v.ID == versionID {
			return true
		}
	}
	return false
}

func (a *App) state() (State, error) {
	real, err := a.hasMods()
	if err != nil {
		return State{}, fmt.Errorf("хранилище %s: %w", a.home, err)
	}
	if !real {
		return a.demoState(), nil
	}
	return a.realState()
}

// realState собирает состояние окна из хранилища и профиля.
func (a *App) realState() (State, error) {
	p, mods, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	_, problems, err := a.store.List()
	if err != nil {
		return State{}, err
	}

	s := State{
		Version: version.String(),
		Home:    a.home,
		Profile: p.Name,
		Issues:  []Issue{},
		Mods:    make([]Mod, 0, len(p.Entries)),
		Plan:    []string{},
	}
	byID := make(map[string]store.Mod, len(mods))
	for _, m := range mods {
		byID[m.ID] = m
	}
	for _, e := range p.Entries {
		m := byID[e.ModID]
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return State{}, err
		}
		row := Mod{
			ID: e.ModID, Name: v.Name, Version: v.Version, Source: v.Source,
			Files: len(v.Files), Versions: len(m.Versions), Enabled: e.Enabled,
			State: "В хранилище", Level: LevelOK,
		}
		if row.Version == "" {
			row.Version = "—"
		}
		if !e.Enabled {
			row.State, row.Level = "Выключен", LevelOff
		}
		s.Mods = append(s.Mods, row)
	}
	for _, problem := range problems {
		s.Issues = append(s.Issues, Issue{
			Title: "Запись в хранилище повреждена", Detail: problem.Error(), Level: LevelError,
		})
	}

	n := len(mods)
	checks := StatusItem{Label: "Проверки", Value: "замечаний нет", Level: LevelOK}
	if k := len(s.Issues); k > 0 {
		checks.Value = fmt.Sprintf("%d %s", k, plural(k, "замечание", "замечания", "замечаний"))
		checks.Level = LevelError
	}
	s.Status = []StatusItem{
		{Label: "Игра", Value: "поиск появится на этапе 4", Level: LevelOff},
		{Label: "Хранилище", Value: fmt.Sprintf("%d %s", n, plural(n, "мод", "мода", "модов")), Level: LevelOK},
		{Label: "Файлы в игре", Value: "развёртывание появится на этапе 3", Level: LevelOff},
		checks,
	}
	return s, nil
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
