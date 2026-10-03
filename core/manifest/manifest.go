package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/i18n"
)

// Способы, которыми файл попал в игру.
const (
	MethodLink = "link" // жёсткая ссылка на файл в хранилище
	MethodCopy = "copy" // копия
)

// Entry — запись об одном файле, который программа положила в папку игры.
type Entry struct {
	Path      string    `json:"path"` // относительно корня игры, через «/»
	ModID     string    `json:"mod"`
	VersionID string    `json:"version"`
	Hash      fsx.Hash  `json:"hash"`
	Size      int64     `json:"size"`
	Method    string    `json:"method"`
	Deployed  time.Time `json:"deployed"`
	// Backup — на этом месте лежал чужой файл; он сохранён и вернётся, когда файл мода уберут.
	Backup bool `json:"backup,omitempty"`
}

// Manifest — учёт развёрнутых файлов одной установки игры. Отвечает на
// вопросы «чей это файл» и «что нужно убрать, чтобы игра стала чистой».
type Manifest struct {
	path       string
	generation string
	entries    map[string]Entry // ключ — путь в нижнем регистре: Windows не различает регистр
	dirs       map[string]string
}

type document struct {
	Generation string   `json:"generation,omitempty"`
	Files      []Entry  `json:"files"`
	Dirs       []string `json:"dirs,omitempty"`
}

// New возвращает пустой манифест, который Save запишет в path.
func New(path string) *Manifest {
	return &Manifest{path: path, entries: map[string]Entry{}, dirs: map[string]string{}}
}

// Load читает манифест. Если файла ещё нет, манифест пуст.
func Load(path string) (*Manifest, error) {
	m := New(path)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.generation = doc.Generation
	for _, e := range doc.Files {
		if err := m.Set(e); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	for _, d := range doc.Dirs {
		if err := m.AddDir(d); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return m, nil
}

// Save атомарно записывает манифест. Записи идут по алфавиту путей, чтобы
// файл не менялся от запуска к запуску без причины.
func (m *Manifest) Save() error {
	data, err := json.MarshalIndent(document{Generation: m.generation, Files: m.Entries(), Dirs: m.Dirs()}, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(m.path, data)
}

func key(path string) string {
	return strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
}

// Set добавляет или заменяет запись о файле.
func (m *Manifest) Set(e Entry) error {
	// Путь из манифеста позже склеивается с корнем игры — он обязан быть безопасным.
	if _, err := fsx.SafeJoin("x", e.Path); err != nil {
		return err
	}
	if e.ModID == "" {
		return i18n.Errorf("запись %q без мода", e.Path)
	}
	e.Path = strings.ReplaceAll(e.Path, `\`, "/")
	m.entries[key(e.Path)] = e
	return nil
}

// Get возвращает запись о файле. Регистр букв в пути не важен.
func (m *Manifest) Get(path string) (Entry, bool) {
	e, ok := m.entries[key(path)]
	return e, ok
}

// Delete убирает запись о файле.
func (m *Manifest) Delete(path string) {
	delete(m.entries, key(path))
}

// Len возвращает число записей.
func (m *Manifest) Len() int {
	return len(m.entries)
}

// Entries возвращает все записи по алфавиту путей.
func (m *Manifest) Entries() []Entry {
	out := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return key(out[i].Path) < key(out[j].Path) })
	return out
}

// ByMod возвращает записи о файлах одного мода.
func (m *Manifest) ByMod(modID string) []Entry {
	var out []Entry
	for _, e := range m.Entries() {
		if e.ModID == modID {
			out = append(out, e)
		}
	}
	return out
}

// Generation — метка развёртывания, которое записало манифест. По ней
// восстановление после сбоя понимает, успело ли развёртывание завершиться.
func (m *Manifest) Generation() string {
	return m.generation
}

func (m *Manifest) SetGeneration(g string) {
	m.generation = g
}

// AddDir запоминает папку, созданную программой в игре: когда она опустеет,
// её можно убрать.
func (m *Manifest) AddDir(path string) error {
	if _, err := fsx.SafeJoin("x", path); err != nil {
		return err
	}
	path = strings.ReplaceAll(path, `\`, "/")
	m.dirs[key(path)] = path
	return nil
}

// RemoveDir забывает папку.
func (m *Manifest) RemoveDir(path string) {
	delete(m.dirs, key(path))
}

// Dirs возвращает созданные программой папки: сначала вложенные, потом внешние.
func (m *Manifest) Dirs() []string {
	out := make([]string, 0, len(m.dirs))
	for _, d := range m.dirs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "/"), strings.Count(out[j], "/")
		if di != dj {
			return di > dj
		}
		return key(out[i]) < key(out[j])
	})
	return out
}
