// Package manager — всё, что делает Modvault: хранилище, профиль,
// развёртывание и плагин игры вместе. Окно и командная строка — оболочки
// над ним. Пока в хранилище нет ни одного мода, состояние демонстрационное.
package manager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/game/darktide"
)

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
	// Command — метод Manager, который вызывает щелчок по значению.
	Command string `json:"command"`
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
	// Command — метод Manager, который вызывает кнопка; пусто — действие ещё не готово.
	Command string `json:"command"`
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

// Manager — Modvault целиком. Методы с заглавной буквы безопасны для
// вызова из разных потоков.
type Manager struct {
	mu sync.Mutex

	home     string
	store    *store.Store
	profiles *profile.Dir
	openErr  error // почему не открылись хранилище или профили

	game      game.Game
	settings  settings
	deployer  *deploy.Deployer
	deployErr error           // почему нельзя развёртывать в выбранную папку
	recovery  deploy.Recovery // что сделано с прерванным развёртыванием при запуске

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

// New создаёт Modvault для настоящего запуска: папка данных по
// умолчанию, игра ищется сама, если папка ещё не выбрана.
func New() *Manager {
	a := NewAt(DefaultHome())
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr == nil && a.settings.GameDir == "" {
		if installs, err := a.game.Detect(); err == nil && len(installs) > 0 {
			a.setInstall(installs[0])
		}
	}
	return a
}

// NewAt создаёт Modvault с папкой данных home.
func NewAt(home string) *Manager {
	a := &Manager{home: home, demo: demoMods(), game: darktide.New()}
	a.store, a.openErr = store.Open(home)
	if a.openErr == nil {
		a.profiles, a.openErr = profile.Open(filepath.Join(home, "profiles"))
	}
	if a.openErr == nil {
		a.settings, a.openErr = loadSettings(home)
	}
	if a.openErr == nil && a.settings.GameDir != "" {
		a.openDeployer()
	}
	return a
}

// State возвращает текущее состояние окна.
func (a *Manager) State() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state()
}

// SetEnabled включает или выключает мод в профиле и возвращает новое состояние.
func (a *Manager) SetEnabled(id string, enabled bool) (State, error) {
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

// AddArchive добавляет мод из архива в хранилище и в профиль.
func (a *Manager) AddArchive(path string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addArchive(path)
}

func (a *Manager) addArchive(path string) (State, error) {
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
	if _, err := a.layout(v); err != nil {
		// Архив, который игра не умеет разложить, в хранилище не остаётся.
		if rerr := a.store.Remove(v.ModID, v.ID, true); rerr != nil {
			return State{}, errors.Join(err, rerr)
		}
		return State{}, fmt.Errorf("«%s» не добавлен: %w", info.Name, err)
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

// ModName возвращает название мода.
func (a *Manager) ModName(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.modName(id)
}

// RemoveMod удаляет мод со всеми версиями: в Корзину или, с permanent,
// насовсем. Если Корзина недоступна — ошибка fsx.ErrTrashUnavailable,
// и мод остаётся на месте.
func (a *Manager) RemoveMod(id string, permanent bool) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	real, err := a.hasMods()
	if err != nil {
		return State{}, err
	}
	if !real {
		return State{}, errors.New("это демонстрационный мод: удалять нечего")
	}
	if err := a.removeMod(id, permanent); err != nil {
		return State{}, err
	}
	return a.state()
}

func (a *Manager) removeMod(id string, permanent bool) error {
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
func (a *Manager) ModFiles(id string) ([]string, error) {
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
func (a *Manager) hasMods() (bool, error) {
	if a.openErr != nil {
		return false, a.openErr
	}
	mods, _, err := a.store.List()
	if err != nil || len(mods) > 0 {
		return len(mods) > 0, err
	}
	// Хранилище пусто, но в игре ещё лежат файлы модов: их надо показать и снять.
	if a.deployer != nil {
		m, err := a.deployer.Manifest()
		return err == nil && m.Len() > 0, err
	}
	return false, nil
}

func (a *Manager) modName(id string) string {
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
func (a *Manager) loadProfile() (profile.Profile, []store.Mod, error) {
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

func (a *Manager) state() (State, error) {
	real, err := a.hasMods()
	if err != nil {
		return State{}, fmt.Errorf("хранилище %s: %w", a.home, err)
	}
	if !real {
		return a.demoState(), nil
	}
	return a.realState()
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
