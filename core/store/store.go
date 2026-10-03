package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/zemidala/modvault/core/archive"
	"github.com/zemidala/modvault/core/fsx"
)

// Раскладка хранилища:
//
//	mods/<мод>/<версия>/files/…     распакованные файлы
//	mods/<мод>/<версия>/meta.json   сведения о версии
//	mods/<мод>/<версия>/archive.*   исходный архив
//	tmp/                            незавершённые операции
//
// Папка версии появляется в mods/ одним переименованием уже готовой, поэтому
// всё, что лежит в mods/, цело. Обрыв оставляет мусор только в tmp/.
const (
	modsDir  = "mods"
	tmpDir   = "tmp"
	filesDir = "files"
	metaFile = "meta.json"
)

// ErrInvalidID — идентификатор мода или версии не годится в имя папки.
var ErrInvalidID = errors.New("недопустимый идентификатор")

// File — файл версии мода.
type File struct {
	Path string   `json:"path"` // относительно папки files, через «/»
	Size int64    `json:"size"`
	Hash fsx.Hash `json:"hash"`
}

// Version — одна версия мода в хранилище.
type Version struct {
	ModID       string    `json:"mod"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Source      string    `json:"source"`
	Added       time.Time `json:"added"`
	Archive     string    `json:"archive"` // имя файла архива в папке версии; пусто — архива нет
	ArchiveHash fsx.Hash  `json:"archiveHash,omitzero"`
	NexusID     int       `json:"nexusId,omitempty"`
	NexusFileID int       `json:"nexusFileId,omitempty"` // номер файла на Nexus; 0 — неизвестен
	// AsIs — файлы уже разложены так, как лежат в игре (например, приняты
	// у другого менеджера модов), раскладывать их заново не нужно.
	AsIs  bool   `json:"asIs,omitempty"`
	Files []File `json:"files"`
}

// Mod — мод со всеми его версиями, от старой к новой по времени добавления.
type Mod struct {
	ID       string
	Versions []Version
}

// Latest возвращает версию, добавленную последней.
func (m Mod) Latest() Version {
	return m.Versions[len(m.Versions)-1]
}

// Info — сведения о моде, известные при добавлении.
type Info struct {
	Name    string // обязательно
	Version string // если пусто, версия именуется по хешу содержимого
	Source  string
	NexusID int
	FileID  int  // номер файла на Nexus
	AsIs    bool // файлы уже разложены как в игре
}

// Store — хранилище модов в папке root.
type Store struct {
	root   string
	limits archive.Limits
}

// Open открывает хранилище, создавая его при необходимости, и убирает
// остатки оборванных операций.
func Open(root string) (*Store, error) {
	s := &Store{root: root, limits: archive.DefaultLimits}
	if err := os.MkdirAll(filepath.Join(root, modsDir), 0o755); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(filepath.Join(root, tmpDir)); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, tmpDir), 0o755); err != nil {
		return nil, err
	}
	return s, nil
}

// Slug превращает название в идентификатор, годный в имя папки:
// «Numeric UI» → «numeric_ui».
func Slug(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' {
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			gap = false
			b.WriteRune(r)
		} else {
			gap = true
		}
	}
	return strings.Trim(b.String(), ".")
}

func checkID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	if _, err := fsx.SafeJoin("x", id); err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}
	return nil
}

func (s *Store) versionDir(modID, versionID string) string {
	return filepath.Join(s.root, modsDir, modID, versionID)
}

// FilesDir возвращает папку с распакованными файлами версии.
func (s *Store) FilesDir(modID, versionID string) string {
	return filepath.Join(s.versionDir(modID, versionID), filesDir)
}

// Add добавляет в хранилище мод из архива. Исходный архив не трогается.
// Если такая версия уже есть, возвращает ошибку, совместимую с fs.ErrExist.
func (s *Store) Add(archivePath string, info Info) (Version, error) {
	modID := Slug(info.Name)
	if err := checkID(modID); err != nil {
		return Version{}, fmt.Errorf("название мода %q: %w", info.Name, err)
	}
	format, err := archive.Detect(archivePath)
	if err != nil {
		return Version{}, err
	}
	archiveHash, _, err := fsx.HashFile(archivePath)
	if err != nil {
		return Version{}, err
	}

	versionID := Slug(info.Version)
	if versionID == "" {
		// Версия неизвестна: различаем сборки по содержимому архива.
		versionID = strings.TrimPrefix(archiveHash.String(), "sha256:")[:12]
	}
	if err := checkID(versionID); err != nil {
		return Version{}, fmt.Errorf("версия %q: %w", info.Version, err)
	}
	final := s.versionDir(modID, versionID)
	if _, err := os.Stat(final); err == nil {
		return Version{}, fmt.Errorf("%s версии %s: %w", info.Name, versionID, fs.ErrExist)
	}

	stage, err := os.MkdirTemp(filepath.Join(s.root, tmpDir), "add-")
	if err != nil {
		return Version{}, err
	}
	defer os.RemoveAll(stage) // после успешного переименования удалять уже нечего

	v := Version{
		ModID: modID, ID: versionID,
		Name: info.Name, Version: info.Version, Source: info.Source,
		NexusID: info.NexusID, NexusFileID: info.FileID, AsIs: info.AsIs,
		Added:   time.Now().UTC().Truncate(time.Second),
		Archive: "archive." + string(format), ArchiveHash: archiveHash,
	}
	if err := fsx.CopyFile(archivePath, filepath.Join(stage, v.Archive)); err != nil {
		return Version{}, err
	}
	entries, err := archive.Extract(archivePath, filepath.Join(stage, filesDir), s.limits)
	if err != nil {
		return Version{}, err
	}
	if len(entries) == 0 {
		return Version{}, fmt.Errorf("%s: в архиве нет файлов", filepath.Base(archivePath))
	}
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	return s.finish(stage, final, v, paths)
}

// finish считает хеши файлов версии, записывает её описание и одним
// переименованием ставит готовую папку на место.
func (s *Store) finish(stage, final string, v Version, paths []string) (Version, error) {
	v.Files = make([]File, 0, len(paths))
	for _, p := range paths {
		hash, size, err := fsx.HashFile(filepath.Join(stage, filesDir, filepath.FromSlash(p)))
		if err != nil {
			return Version{}, err
		}
		v.Files = append(v.Files, File{Path: p, Size: size, Hash: hash})
	}
	sort.Slice(v.Files, func(i, j int) bool { return v.Files[i].Path < v.Files[j].Path })

	meta, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Version{}, err
	}
	if err := fsx.WriteFile(filepath.Join(stage, metaFile), meta); err != nil {
		return Version{}, err
	}

	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return Version{}, err
	}
	if err := os.Rename(stage, final); err != nil {
		return Version{}, err
	}
	return v, nil
}

// AddFiles добавляет мод из готовых файлов: files — пути относительно root
// через «/». Архива у такой версии нет. Если версия не задана, она
// именуется по хешу содержимого, так что повторное добавление тех же файлов
// даёт ошибку, совместимую с fs.ErrExist, и идентификаторы уже имеющейся версии.
func (s *Store) AddFiles(root string, files []string, info Info) (Version, error) {
	modID := Slug(info.Name)
	if err := checkID(modID); err != nil {
		return Version{}, fmt.Errorf("название мода %q: %w", info.Name, err)
	}
	if len(files) == 0 {
		return Version{}, fmt.Errorf("%s: нет файлов", info.Name)
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)

	// Отпечаток содержимого: пути и хеши всех файлов.
	var listing strings.Builder
	for _, f := range sorted {
		if _, err := fsx.SafeJoin("x", f); err != nil {
			return Version{}, err
		}
		hash, _, err := fsx.HashFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return Version{}, err
		}
		listing.WriteString(f + "\x00" + hash.String() + "\n")
	}
	fingerprint, _, _ := fsx.HashReader(strings.NewReader(listing.String()))

	versionID := Slug(info.Version)
	if versionID == "" {
		versionID = strings.TrimPrefix(fingerprint.String(), "sha256:")[:12]
	}
	if err := checkID(versionID); err != nil {
		return Version{}, fmt.Errorf("версия %q: %w", info.Version, err)
	}
	final := s.versionDir(modID, versionID)
	if _, err := os.Stat(final); err == nil {
		// Вызывающему нужно знать, какая версия уже есть.
		return Version{ModID: modID, ID: versionID}, fmt.Errorf("%s версии %s: %w", info.Name, versionID, fs.ErrExist)
	}

	stage, err := os.MkdirTemp(filepath.Join(s.root, tmpDir), "add-")
	if err != nil {
		return Version{}, err
	}
	defer os.RemoveAll(stage)
	for _, f := range sorted {
		dst := filepath.Join(stage, filesDir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return Version{}, err
		}
		if err := fsx.CopyFile(filepath.Join(root, filepath.FromSlash(f)), dst); err != nil {
			return Version{}, err
		}
	}
	v := Version{
		ModID: modID, ID: versionID,
		Name: info.Name, Version: info.Version, Source: info.Source,
		NexusID: info.NexusID, NexusFileID: info.FileID, AsIs: info.AsIs,
		Added: time.Now().UTC().Truncate(time.Second),
	}
	return s.finish(stage, final, v, sorted)
}

// Get возвращает сведения о версии мода.
func (s *Store) Get(modID, versionID string) (Version, error) {
	if err := errors.Join(checkID(modID), checkID(versionID)); err != nil {
		return Version{}, err
	}
	return s.readMeta(modID, versionID)
}

func (s *Store) readMeta(modID, versionID string) (Version, error) {
	path := filepath.Join(s.versionDir(modID, versionID), metaFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return Version{}, err
	}
	var v Version
	if err := json.Unmarshal(data, &v); err != nil {
		return Version{}, fmt.Errorf("%s: %w", path, err)
	}
	if v.ModID != modID || v.ID != versionID {
		return Version{}, fmt.Errorf("%s: описание относится к %s/%s", path, v.ModID, v.ID)
	}
	return v, nil
}

// List возвращает все моды по алфавиту идентификаторов. Версии, которые
// не удалось прочитать, в список не попадают: о каждой — ошибка в problems.
func (s *Store) List() (mods []Mod, problems []error, err error) {
	modDirs, err := os.ReadDir(filepath.Join(s.root, modsDir))
	if err != nil {
		return nil, nil, err
	}
	for _, md := range modDirs {
		if !md.IsDir() {
			continue
		}
		verDirs, err := os.ReadDir(filepath.Join(s.root, modsDir, md.Name()))
		if err != nil {
			return nil, nil, err
		}
		mod := Mod{ID: md.Name()}
		for _, vd := range verDirs {
			if !vd.IsDir() {
				continue
			}
			v, err := s.readMeta(md.Name(), vd.Name())
			if err != nil {
				problems = append(problems, err)
				continue
			}
			mod.Versions = append(mod.Versions, v)
		}
		if len(mod.Versions) == 0 {
			continue
		}
		sort.SliceStable(mod.Versions, func(i, j int) bool { return mod.Versions[i].Added.Before(mod.Versions[j].Added) })
		mods = append(mods, mod)
	}
	return mods, problems, nil
}

// Remove удаляет версию мода. По умолчанию — в Корзину; если она недоступна,
// возвращается fsx.ErrTrashUnavailable и версия остаётся на месте. С permanent
// версия удаляется насовсем.
func (s *Store) Remove(modID, versionID string, permanent bool) error {
	if err := errors.Join(checkID(modID), checkID(versionID)); err != nil {
		return err
	}
	dir := s.versionDir(modID, versionID)
	if _, err := os.Stat(dir); err != nil {
		return err
	}

	if permanent {
		// Сначала версия одним переименованием исчезает из хранилища, потом
		// стирается: обрыв посреди удаления не оставит половину мода.
		doomed, err := os.MkdirTemp(filepath.Join(s.root, tmpDir), "del-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(doomed)
		if err := os.Rename(dir, filepath.Join(doomed, versionID)); err != nil {
			return err
		}
	} else if err := fsx.Trash(dir); err != nil {
		return err
	}

	// Папка мода без версий не нужна; непустую Remove не тронет.
	os.Remove(filepath.Dir(dir))
	return nil
}

// RemoveMod удаляет мод со всеми версиями.
func (s *Store) RemoveMod(modID string, permanent bool) error {
	if err := checkID(modID); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(s.root, modsDir, modID))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := s.Remove(modID, e.Name(), permanent); err != nil {
			return err
		}
	}
	return nil
}

// Problem — расхождение между описанием версии и файлами на диске.
type Problem struct {
	Path string
	Kind string // "пропал", "изменён", "лишний"
}

// Verify сверяет файлы версии с её описанием.
func (s *Store) Verify(modID, versionID string) ([]Problem, error) {
	v, err := s.Get(modID, versionID)
	if err != nil {
		return nil, err
	}
	root := s.FilesDir(modID, versionID)
	var problems []Problem
	known := make(map[string]bool, len(v.Files))

	for _, f := range v.Files {
		known[f.Path] = true
		hash, _, err := fsx.HashFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			problems = append(problems, Problem{f.Path, "пропал"})
		case err != nil:
			return nil, err
		case hash != f.Hash:
			problems = append(problems, Problem{f.Path, "изменён"})
		}
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); !known[rel] {
			problems = append(problems, Problem{rel, "лишний"})
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return problems, nil
}
