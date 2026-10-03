// Package manager — всё, что делает Modvault: хранилище, профиль,
// развёртывание и плагин игры вместе. Окно и командная строка — оболочки
// над ним. Пока в хранилище нет ни одного мода, состояние демонстрационное.
package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/game/darktide"
)

// mainProfile — профиль по умолчанию.
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
	// OrderNote поясняет, кто задаёт порядок загрузки, если не профиль.
	OrderNote string   `json:"orderNote"`
	Plan      []string `json:"plan"`
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
	// Arg — к чему применить команду (например, какой мод включить).
	Arg string `json:"arg"`
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

// Хранилище должно лежать на одном диске с игрой: только тогда файлы
// кладутся в игру жёсткими ссылками. Где оно, запоминается в маленьком
// файле в профиле пользователя.
const locationName = "location.json"

func locationPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "Modvault", locationName)
}

// DefaultHome возвращает папку данных программы: из MODVAULT_HOME, из
// запомненного места или пусто, если место ещё не выбрано.
func DefaultHome() string {
	if home := os.Getenv("MODVAULT_HOME"); home != "" {
		return home
	}
	data, err := os.ReadFile(locationPath())
	if err != nil {
		return ""
	}
	var loc struct {
		Home string `json:"home"`
	}
	if json.Unmarshal(data, &loc) != nil {
		return ""
	}
	return loc.Home
}

// chooseHome выбирает место хранилища при первом запуске: папка Modvault
// в корне диска с игрой, а если игра не найдена — в профиле пользователя.
func chooseHome(installs []game.Install) string {
	if len(installs) > 0 {
		if vol := filepath.VolumeName(installs[0].Dir); vol != "" {
			return vol + `\Modvault`
		}
	}
	return filepath.Dir(locationPath())
}

func saveLocation(home string) error {
	data, err := json.Marshal(map[string]string{"home": home})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(locationPath()), 0o755); err != nil {
		return err
	}
	return fsx.WriteFile(locationPath(), data)
}

// New создаёт Modvault для настоящего запуска: при первом запуске находит
// игру и кладёт хранилище на её диск, дальше берёт запомненное место.
func New() *Manager {
	g := darktide.New()
	installs, _ := g.Detect()
	home := DefaultHome()
	if home == "" {
		home = chooseHome(installs)
		saveLocation(home) // не запомнилось — выберем так же в следующий раз
	}
	a := NewAt(home)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr == nil && a.settings.GameDir == "" && len(installs) > 0 {
		a.setInstall(installs[0])
	}
	return a
}

// NewAt создаёт Modvault с папкой данных home.
func NewAt(home string) *Manager {
	return NewWith(home, darktide.New())
}

// NewWith создаёт Modvault с папкой данных home и плагином игры g.
func NewWith(home string, g game.Game) *Manager {
	a := &Manager{home: home, demo: demoMods(), game: g}
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
	// Был ли мод в профиле до добавления: новый мод включается, у старого
	// меняется только версия.
	before, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	listed := before.Index(store.Slug(info.Name)) >= 0
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
	if p.Index(v.ModID) < 0 {
		err = p.Add(v.ModID, v.ID)
	} else {
		err = p.SetVersion(v.ModID, v.ID)
	}
	if err == nil && !listed {
		err = p.SetEnabled(v.ModID, true)
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

// hasMods сообщает, что показывать настоящее состояние, а не пример:
// выбрана игра или в хранилище есть моды.
func (a *Manager) hasMods() (bool, error) {
	if a.openErr != nil {
		return false, a.openErr
	}
	// Игра выбрана — показываем настоящее, даже пустое: иначе за примером
	// не видно, что в игре уже есть (например, моды Vortex).
	if a.settings.GameDir != "" {
		return true, nil
	}
	mods, _, err := a.store.List()
	return len(mods) > 0, err
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
// пропавшие моды убирает, новые ставит в конец выключенными, пропавшую версию заменяет
// последней. Изменённый профиль сохраняет.
func (a *Manager) loadProfile() (profile.Profile, []store.Mod, error) {
	mods, _, err := a.store.List()
	if err != nil {
		return profile.Profile{}, nil, err
	}
	p, err := a.profiles.Load(a.profileName())
	if errors.Is(err, fs.ErrNotExist) {
		p, err = profile.Profile{Name: a.profileName()}, nil
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
			// Мод из хранилища, которого в профиле нет, виден в нём выключенным:
			// включает его только сам пользователь.
			if err := p.Add(m.ID, m.Latest().ID); err != nil {
				return profile.Profile{}, nil, err
			}
			p.SetEnabled(m.ID, false)
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
