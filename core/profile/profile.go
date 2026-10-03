package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/fsx"
)

const ext = ".json"

var (
	// ErrInvalidName — название профиля не годится в имя файла.
	ErrInvalidName = errors.New("недопустимое название профиля")
	// ErrNoMod — в профиле нет такого мода.
	ErrNoMod = errors.New("мода нет в профиле")
)

// Entry — мод в профиле. Позиция в списке задаёт порядок загрузки.
type Entry struct {
	ModID     string `json:"mod"`
	VersionID string `json:"version"`
	Enabled   bool   `json:"enabled"`
}

// Profile — набор модов и их порядок.
type Profile struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"mods"`
}

// Index возвращает позицию мода в профиле или -1.
func (p *Profile) Index(modID string) int {
	for i, e := range p.Entries {
		if e.ModID == modID {
			return i
		}
	}
	return -1
}

// Add ставит мод в конец профиля включённым.
func (p *Profile) Add(modID, versionID string) error {
	if p.Index(modID) >= 0 {
		return fmt.Errorf("мод %q уже есть в профиле «%s»", modID, p.Name)
	}
	p.Entries = append(p.Entries, Entry{ModID: modID, VersionID: versionID, Enabled: true})
	return nil
}

// Remove убирает мод из профиля.
func (p *Profile) Remove(modID string) error {
	i := p.Index(modID)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrNoMod, modID)
	}
	p.Entries = append(p.Entries[:i], p.Entries[i+1:]...)
	return nil
}

// SetEnabled включает или выключает мод; место в порядке он сохраняет.
func (p *Profile) SetEnabled(modID string, enabled bool) error {
	i := p.Index(modID)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrNoMod, modID)
	}
	p.Entries[i].Enabled = enabled
	return nil
}

// SetVersion меняет версию мода, используемую профилем.
func (p *Profile) SetVersion(modID, versionID string) error {
	i := p.Index(modID)
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrNoMod, modID)
	}
	p.Entries[i].VersionID = versionID
	return nil
}

// Move переставляет мод на позицию to (с нуля). Позиция за пределами списка
// означает его край.
func (p *Profile) Move(modID string, to int) error {
	from := p.Index(modID)
	if from < 0 {
		return fmt.Errorf("%w: %q", ErrNoMod, modID)
	}
	to = max(0, min(to, len(p.Entries)-1))
	e := p.Entries[from]
	p.Entries = append(p.Entries[:from], p.Entries[from+1:]...)
	p.Entries = append(p.Entries[:to], append([]Entry{e}, p.Entries[to:]...)...)
	return nil
}

func (p *Profile) validate() error {
	seen := make(map[string]bool, len(p.Entries))
	for _, e := range p.Entries {
		if e.ModID == "" {
			return errors.New("запись без мода")
		}
		if seen[e.ModID] {
			return fmt.Errorf("мод %q записан дважды", e.ModID)
		}
		seen[e.ModID] = true
	}
	return nil
}

// Dir — папка с профилями, по файлу на профиль.
type Dir struct {
	dir string
}

// Open открывает папку профилей, создавая её при необходимости.
func Open(dir string) (*Dir, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// Остатки оборванной записи профиля.
	if _, err := fsx.CleanTemp(dir); err != nil {
		return nil, err
	}
	return &Dir{dir: dir}, nil
}

func (d *Dir) path(name string) (string, error) {
	if name == "" || name != strings.TrimSpace(name) || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	path, err := fsx.SafeJoin(d.dir, name+ext)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return path, nil
}

// List возвращает названия профилей по алфавиту.
func (d *Dir) List() ([]string, error) {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ext); ok && !e.IsDir() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Load читает профиль. Если его нет — ошибка, совместимая с fs.ErrNotExist.
func (d *Dir) Load(name string) (Profile, error) {
	path, err := d.path(name)
	if err != nil {
		return Profile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := p.validate(); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	p.Name = name // имя файла главнее записанного внутри
	if p.Entries == nil {
		p.Entries = []Entry{}
	}
	return p, nil
}

// Save атомарно записывает профиль: на диске остаётся либо прежний, либо новый.
func (d *Dir) Save(p Profile) error {
	path, err := d.path(p.Name)
	if err != nil {
		return err
	}
	if err := p.validate(); err != nil {
		return fmt.Errorf("профиль «%s»: %w", p.Name, err)
	}
	if p.Entries == nil {
		p.Entries = []Entry{}
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(path, data)
}

// Delete удаляет профиль. Моды в хранилище остаются.
func (d *Dir) Delete(name string) error {
	path, err := d.path(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// Rename переименовывает профиль. Занятое название — ошибка, совместимая с fs.ErrExist.
func (d *Dir) Rename(from, to string) error {
	p, err := d.Load(from)
	if err != nil {
		return err
	}
	if err := d.create(to, p); err != nil {
		return err
	}
	return d.Delete(from)
}

// Copy создаёт профиль to с тем же набором модов, что в from.
func (d *Dir) Copy(from, to string) error {
	p, err := d.Load(from)
	if err != nil {
		return err
	}
	return d.create(to, p)
}

func (d *Dir) create(name string, p Profile) error {
	path, err := d.path(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("профиль «%s»: %w", name, fs.ErrExist)
	}
	p.Name = name
	return d.Save(p)
}
