package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/journal"
	"github.com/zemidala/modvault/core/manifest"
)

// Папка состояния одной установки игры:
//
//	manifest.json            что программа положила в игру
//	journal.json, .log       незавершённое развёртывание
//	backups/<путь>           чужие файлы, которые мод временно заменил
//	stash/<id>/<путь>        свои файлы, снятые текущим развёртыванием
//	displaced/<id>/<путь>    файлы, изменённые вне программы; не удаляются
//	stale/<id>/<путь>        устаревшие резервные копии, снятые текущим развёртыванием
const (
	manifestFile = "manifest.json"
	backupsDir   = "backups"
	stashDir     = "stash"
	displacedDir = "displaced"
	staleDir     = "stale"
)

// Шаги развёртывания. Каждый шаг обратим: откат делает обратное действие,
// сверяясь с тем, что реально лежит на диске.
const (
	opBackup   = "backup"   // чужой файл из игры → backups
	opStash    = "stash"    // свой файл из игры → stash
	opDisplace = "displace" // свой, но изменённый файл → displaced
	opPlace    = "place"    // файл мода → игра
	opRestore  = "restore"  // чужой файл из backups → обратно в игру
	opStale    = "stale"    // устаревшая резервная копия → stale
)

type step struct {
	Op     string   `json:"op"`
	Path   string   `json:"path"`
	Src    string   `json:"src,omitempty"`
	Hash   fsx.Hash `json:"hash,omitzero"`
	Method string   `json:"method,omitempty"`
}

// header — то, что журнал хранит о развёртывании.
type header struct {
	Steps []step   `json:"steps"`
	Dirs  []string `json:"dirs"` // папки, которые развёртывание создаст в игре
}

// testHook позволяет тестам оборвать процесс в выбранной точке.
var testHook func(point string, i int)

func hook(point string, i int) {
	if testHook != nil {
		testHook(point, i)
	}
}

// Deployer развёртывает моды в одну папку игры.
type Deployer struct {
	game  string
	state string
	// ForceCopy запрещает жёсткие ссылки.
	ForceCopy bool
}

// Recovery — что Open сделал с прерванным развёртыванием.
type Recovery int

const (
	NothingToRecover Recovery = iota
	RolledBack                // развёртывание откачено, игра в прежнем состоянии
	Completed                 // развёртывание успело завершиться, убраны остатки
)

// Open готовит развёртывание в папку игры game; state — папка состояния
// этой установки. Если прошлое развёртывание оборвалось, Open его откатывает.
// Если откат не удался, ошибка возвращается вместе с Deployer: новое
// развёртывание начать нельзя, пока следующий Open не доведёт откат.
func Open(game, state string) (*Deployer, Recovery, error) {
	info, err := os.Stat(game)
	if err != nil {
		return nil, NothingToRecover, err
	}
	if !info.IsDir() {
		return nil, NothingToRecover, fmt.Errorf("%s — не папка", game)
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		return nil, NothingToRecover, err
	}
	d := &Deployer{game: game, state: state}
	rec, err := d.recover()
	return d, rec, err
}

// Game возвращает папку игры.
func (d *Deployer) Game() string { return d.game }

// Manifest читает учёт развёрнутых файлов.
func (d *Deployer) Manifest() (*manifest.Manifest, error) {
	return manifest.Load(filepath.Join(d.state, manifestFile))
}

// Original возвращает путь к оригиналу файла игры rel — тому, что лежало бы
// в игре без модов: резервную копию, если файл сейчас заменён модом, иначе
// сам файл в игре. Если оригинала нет (файл целиком принёс мод) — ошибка,
// совместимая с fs.ErrNotExist.
func (d *Deployer) Original(rel string) (string, error) {
	game, err := d.gamePath(rel)
	if err != nil {
		return "", err
	}
	m, err := d.Manifest()
	if err != nil {
		return "", err
	}
	e, ok := m.Get(rel)
	if !ok {
		return game, nil
	}
	if exists(game) {
		ours, err := d.matches(game, e)
		if err != nil {
			return "", err
		}
		if !ours {
			return game, nil // файл заменили вне программы: он и есть оригинал
		}
	}
	if !e.Backup {
		return "", &os.PathError{Op: "original", Path: game, Err: fs.ErrNotExist}
	}
	return d.backupPath(e.Path)
}

func (d *Deployer) gamePath(rel string) (string, error) {
	return fsx.SafeJoin(d.game, rel)
}

func (d *Deployer) backupPath(rel string) (string, error) {
	return fsx.SafeJoin(filepath.Join(d.state, backupsDir), rel)
}

func (d *Deployer) asidePath(dir, id, rel string) (string, error) {
	return fsx.SafeJoin(filepath.Join(d.state, dir, id), rel)
}

// ChangeKind — что развёртывание сделает с файлом.
type ChangeKind string

const (
	Add     ChangeKind = "add"
	Replace ChangeKind = "replace"
	Remove  ChangeKind = "remove"
)

// Change — изменение одного файла в игре.
type Change struct {
	Kind  ChangeKind
	Path  string
	ModID string // чей файл ляжет; для Remove — чей уберётся
	OldID string // для Replace — чей файл лежал
}

// Drift — расхождение между учётом и диском: файл тронули вне программы.
type Drift struct {
	Path  string
	ModID string
	// Missing — файла нет; иначе он изменён. Изменённый файл при развёртывании
	// не удаляется, а переносится в папку displaced.
	Missing bool
	// Updated — на месте файла мода лежит новый файл игры: её обновили или
	// проверили файлы в Steam. Он становится оригиналом, а прежняя резервная
	// копия устарела и не вернётся никогда.
	Updated bool
}

// Plan — что нужно сделать, чтобы игра совпала с профилем.
type Plan struct {
	Changes   []Change
	Conflicts []Conflict
	Drift     []Drift

	steps   []step
	dirs    []string
	entries []manifest.Entry
	method  string
}

// Empty сообщает, что игра уже совпадает с профилем.
func (p *Plan) Empty() bool { return len(p.steps) == 0 }

// Plan сравнивает желаемое состояние с учётом и с тем, что лежит на диске.
func (d *Deployer) Plan(sources []Source, winners map[string]string) (*Plan, error) {
	m, err := d.Manifest()
	if err != nil {
		return nil, err
	}
	desired, conflicts := Desired(sources, winners)
	p := &Plan{Conflicts: conflicts}

	keys := map[string]bool{}
	for k := range desired {
		keys[k] = true
	}
	for _, e := range m.Entries() {
		keys[key(e.Path)] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	newDirs := map[string]bool{}
	for _, k := range sorted {
		t, hasT := desired[k]
		e, hasE := m.Get(k)
		rel := e.Path
		if hasT {
			rel = t.Path
		}
		abs, err := d.gamePath(rel)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(abs)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if exists && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: на месте файла лежит папка или ссылка", rel)
		}

		ours := false
		if hasE && exists {
			if ours, err = d.matches(abs, e); err != nil {
				return nil, err
			}
			if !ours {
				p.Drift = append(p.Drift, Drift{Path: e.Path, ModID: e.ModID, Updated: e.Backup})
			}
		}
		if hasE && !exists {
			p.Drift = append(p.Drift, Drift{Path: e.Path, ModID: e.ModID, Missing: true})
		}

		// Снять то, что лежит сейчас: своё — во временную папку, изменённое
		// чужими руками — в displaced, где оно останется.
		takeAway := func() {
			if !exists {
				return
			}
			op := opStash
			if !ours {
				op = opDisplace
			}
			p.steps = append(p.steps, step{Op: op, Path: e.Path})
		}

		// Игра заменила наш файл своим новым: старая резервная копия больше
		// не оригинал. Её снимаем, а новый файл игры считаем оригиналом.
		if hasE && exists && !ours && e.Backup {
			p.steps = append(p.steps, step{Op: opStale, Path: e.Path})
			if hasT {
				p.steps = append(p.steps, step{Op: opBackup, Path: t.Path})
				if err := d.addPlace(p, t, true, newDirs); err != nil {
					return nil, err
				}
				p.Changes = append(p.Changes, Change{Kind: Replace, Path: t.Path, ModID: t.ModID, OldID: e.ModID})
			} else {
				p.Changes = append(p.Changes, Change{Kind: Remove, Path: e.Path, ModID: e.ModID})
			}
			continue
		}

		switch {
		case hasT && hasE:
			if ours && e.ModID == t.ModID && e.VersionID == t.VersionID && e.Hash == t.Hash {
				p.entries = append(p.entries, e)
				continue
			}
			takeAway()
			if err := d.addPlace(p, t, e.Backup, newDirs); err != nil {
				return nil, err
			}
			p.Changes = append(p.Changes, Change{Kind: Replace, Path: t.Path, ModID: t.ModID, OldID: e.ModID})

		case hasT:
			backup := false
			if exists {
				b, err := d.backupPath(t.Path)
				if err != nil {
					return nil, err
				}
				if _, err := os.Lstat(b); err == nil {
					return nil, fmt.Errorf("%s: резервная копия уже есть, а файл не учтён — разберитесь с папкой %s", t.Path, b)
				}
				p.steps = append(p.steps, step{Op: opBackup, Path: t.Path})
				backup = true
			}
			if err := d.addPlace(p, t, backup, newDirs); err != nil {
				return nil, err
			}
			p.Changes = append(p.Changes, Change{Kind: Add, Path: t.Path, ModID: t.ModID})

		case hasE:
			takeAway()
			if e.Backup {
				p.steps = append(p.steps, step{Op: opRestore, Path: e.Path})
			}
			p.Changes = append(p.Changes, Change{Kind: Remove, Path: e.Path, ModID: e.ModID})
		}
	}

	for dir := range newDirs {
		p.dirs = append(p.dirs, dir)
	}
	sort.Strings(p.dirs)
	return p, nil
}

// addPlace добавляет в план укладку файла и запоминает папки, которых ещё нет.
func (d *Deployer) addPlace(p *Plan, t Target, backup bool, newDirs map[string]bool) error {
	if p.method == "" {
		p.method = manifest.MethodCopy
		if !d.ForceCopy && fsx.CanLink(filepath.Dir(t.Src), d.game) == nil {
			p.method = manifest.MethodLink
		}
	}
	for dir := path.Dir(t.Path); dir != "." && dir != "/"; dir = path.Dir(dir) {
		abs, err := d.gamePath(dir)
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err == nil {
			break
		}
		newDirs[dir] = true
	}
	p.steps = append(p.steps, step{Op: opPlace, Path: t.Path, Src: t.Src, Hash: t.Hash, Method: p.method})
	p.entries = append(p.entries, manifest.Entry{
		Path: t.Path, ModID: t.ModID, VersionID: t.VersionID, Hash: t.Hash, Size: t.Size,
		Method: p.method, Backup: backup,
	})
	return nil
}

// matches сообщает, лежит ли в игре ровно тот файл, что записан в учёте.
func (d *Deployer) matches(abs string, e manifest.Entry) (bool, error) {
	hash, _, err := fsx.HashFile(abs)
	if err != nil {
		return false, err
	}
	return hash == e.Hash, nil
}

// Result — итог развёртывания.
type Result struct {
	Changes int
	// Displaced — папка, куда перенесены файлы, изменённые вне программы;
	// пусто, если таких не было.
	Displaced string
}

// Apply выполняет план. При ошибке игра откатывается к прежнему состоянию,
// и возвращается ошибка; если откат тоже не удался, об этом сказано в ошибке,
// а следующий Open повторит откат.
func (d *Deployer) Apply(p *Plan) (Result, error) {
	m, err := d.Manifest()
	if err != nil {
		return Result{}, err
	}
	id := time.Now().UTC().Format("20060102-150405.000000000")
	h := header{Steps: p.steps, Dirs: p.dirs}
	j, err := journal.Begin(d.state, id, h)
	if errors.Is(err, journal.ErrPending) {
		return Result{}, errors.New("прошлое развёртывание не откачено до конца; перезапустите программу")
	}
	if err != nil {
		return Result{}, err
	}

	methods := map[string]string{}
	for i, s := range p.steps {
		hook("before", i)
		method, err := d.do(id, s)
		if err == nil {
			hook("after", i)
			err = j.Done()
		}
		if err != nil {
			j.Close()
			err = fmt.Errorf("%s: %w", s.Path, err)
			if rerr := d.rollback(id, h, i); rerr != nil {
				return Result{}, fmt.Errorf("%w; откатить не удалось: %v", err, rerr)
			}
			if cerr := journal.Clear(d.state); cerr != nil {
				return Result{}, errors.Join(err, cerr)
			}
			return Result{}, fmt.Errorf("%w; игра возвращена в прежнее состояние", err)
		}
		if s.Op == opPlace {
			methods[key(s.Path)] = method
		}
	}

	// Новый учёт с меткой этого развёртывания: по метке восстановление
	// поймёт, что развёртывание завершилось.
	next := manifest.New(filepath.Join(d.state, manifestFile))
	now := time.Now().UTC().Truncate(time.Second)
	for _, e := range p.entries {
		if method, ok := methods[key(e.Path)]; ok {
			e.Method, e.Deployed = method, now
		}
		if err := next.Set(e); err != nil {
			return Result{}, err
		}
	}
	for _, dir := range append(m.Dirs(), p.dirs...) {
		if err := next.AddDir(dir); err != nil {
			return Result{}, err
		}
	}
	next.SetGeneration(id)
	if err := next.Save(); err != nil {
		j.Close()
		if rerr := d.rollback(id, h, len(p.steps)); rerr != nil {
			return Result{}, fmt.Errorf("запись учёта: %w; откатить не удалось: %v", err, rerr)
		}
		journal.Clear(d.state)
		return Result{}, fmt.Errorf("запись учёта: %w; игра возвращена в прежнее состояние", err)
	}
	hook("commit", len(p.steps))
	if err := j.Finish(); err != nil {
		return Result{}, err
	}

	res := Result{Changes: len(p.Changes)}
	if displaced := filepath.Join(d.state, displacedDir, id); exists(displaced) {
		res.Displaced = displaced
	}
	return res, d.cleanup(id)
}

// cleanup убирает то, что после завершённого развёртывания не нужно:
// снятые свои файлы и опустевшие папки, которые программа создала в игре.
func (d *Deployer) cleanup(id string) error {
	for _, dir := range []string{stashDir, staleDir} {
		if err := os.RemoveAll(filepath.Join(d.state, dir, id)); err != nil {
			return err
		}
	}
	m, err := d.Manifest()
	if err != nil {
		return err
	}
	changed := false
	for _, dir := range m.Dirs() {
		abs, err := d.gamePath(dir)
		if err != nil {
			return err
		}
		err = os.Remove(abs)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			m.RemoveDir(dir)
			changed = true
		}
		// Непустая папка ещё нужна.
	}
	if changed {
		return m.Save()
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// do выполняет шаг и возвращает способ, которым лёг файл.
func (d *Deployer) do(id string, s step) (string, error) {
	game, err := d.gamePath(s.Path)
	if err != nil {
		return "", err
	}
	switch s.Op {
	case opBackup:
		return "", moveInto(game, d.backupPath, s.Path)
	case opStash:
		return "", moveInto(game, func(rel string) (string, error) { return d.asidePath(stashDir, id, rel) }, s.Path)
	case opDisplace:
		return "", moveInto(game, func(rel string) (string, error) { return d.asidePath(displacedDir, id, rel) }, s.Path)
	case opStale:
		backup, err := d.backupPath(s.Path)
		if err != nil {
			return "", err
		}
		return "", moveInto(backup, func(rel string) (string, error) { return d.asidePath(staleDir, id, rel) }, s.Path)
	case opRestore:
		backup, err := d.backupPath(s.Path)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(game), 0o755); err != nil {
			return "", err
		}
		return "", fsx.Move(backup, game)
	case opPlace:
		if err := os.MkdirAll(filepath.Dir(game), 0o755); err != nil {
			return "", err
		}
		if exists(game) {
			return "", &os.PathError{Op: "place", Path: game, Err: fs.ErrExist}
		}
		if s.Method == manifest.MethodLink {
			err := fsx.Link(s.Src, game)
			if !errors.Is(err, fsx.ErrCrossVolume) && !errors.Is(err, fsx.ErrLinkUnsupported) {
				return manifest.MethodLink, err
			}
		}
		return manifest.MethodCopy, fsx.CopyFile(s.Src, game)
	}
	return "", fmt.Errorf("неизвестный шаг %q", s.Op)
}

// moveInto переносит файл из игры в папку, путь в которой даёт where.
func moveInto(game string, where func(string) (string, error), rel string) error {
	dst, err := where(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return fsx.Move(game, dst)
}

// rollback отменяет шаги с done-1 до 0 в обратном порядке. Шаг с номером
// done мог начаться без отметки — его отмена сверяется с диском.
func (d *Deployer) rollback(id string, h header, done int) error {
	var errs []error
	last := min(done, len(h.Steps)-1)
	for i := last; i >= 0; i-- {
		if err := d.undo(id, h.Steps[i], i == done); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h.Steps[i].Path, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	// Остатки оборванного копирования и папки, созданные этим развёртыванием.
	for _, s := range h.Steps {
		if s.Op == opPlace {
			if game, err := d.gamePath(s.Path); err == nil {
				fsx.CleanTemp(filepath.Dir(game))
			}
		}
	}
	dirs := append([]string(nil), h.Dirs...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		if abs, err := d.gamePath(dir); err == nil {
			os.Remove(abs) // непустую папку Remove не тронет
		}
	}
	os.RemoveAll(filepath.Join(d.state, stashDir, id))
	os.RemoveAll(filepath.Join(d.state, staleDir, id))
	removeIfEmpty(filepath.Join(d.state, displacedDir, id))
	return nil
}

// removeIfEmpty убирает пустые папки дерева dir.
func removeIfEmpty(dir string) {
	var dirs []string
	filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err == nil && e.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i])
	}
}

// undo отменяет один шаг. unsure — шаг мог и не выполниться.
func (d *Deployer) undo(id string, s step, unsure bool) error {
	game, err := d.gamePath(s.Path)
	if err != nil {
		return err
	}
	// from — откуда шаг унёс файл, aside — куда.
	from, aside := game, ""
	switch s.Op {
	case opBackup, opRestore:
		aside, err = d.backupPath(s.Path)
	case opStash:
		aside, err = d.asidePath(stashDir, id, s.Path)
	case opDisplace:
		aside, err = d.asidePath(displacedDir, id, s.Path)
	case opStale:
		if from, err = d.backupPath(s.Path); err == nil {
			aside, err = d.asidePath(staleDir, id, s.Path)
		}
	case opPlace:
		if !exists(game) {
			return nil
		}
		hash, _, err := fsx.HashFile(game)
		if err != nil {
			return err
		}
		if hash != s.Hash {
			if unsure {
				return nil // на месте не наш файл: шаг не успел выполниться
			}
			return fmt.Errorf("на месте положенного файла лежит другой")
		}
		return os.Remove(game)
	default:
		return fmt.Errorf("неизвестный шаг %q", s.Op)
	}
	if err != nil {
		return err
	}

	if s.Op == opRestore {
		// Шаг вернул чужой файл в игру; отмена уносит его обратно.
		switch {
		case !exists(game):
			return nil
		case exists(aside):
			return os.Remove(game) // копия между томами не успела убрать оригинал
		}
		if err := os.MkdirAll(filepath.Dir(aside), 0o755); err != nil {
			return err
		}
		return fsx.Move(game, aside)
	}

	// Шаг унёс файл; отмена возвращает его.
	switch {
	case !exists(aside):
		return nil
	case exists(from):
		if unsure {
			return os.Remove(aside) // копия между томами не успела убрать оригинал
		}
		return fmt.Errorf("файл уже на месте, а копия в %s осталась", aside)
	}
	if err := os.MkdirAll(filepath.Dir(from), 0o755); err != nil {
		return err
	}
	return fsx.Move(aside, from)
}

// recover разбирается с развёртыванием, оборвавшимся в прошлый раз.
func (d *Deployer) recover() (Recovery, error) {
	pending, err := journal.Load(d.state)
	if err != nil || pending == nil {
		return NothingToRecover, err
	}
	m, err := d.Manifest()
	if err != nil {
		return NothingToRecover, err
	}
	if m.Generation() == pending.ID {
		// Учёт успел записаться: развёртывание завершено, осталось прибраться.
		if err := journal.Clear(d.state); err != nil {
			return NothingToRecover, err
		}
		return Completed, d.cleanup(pending.ID)
	}

	var h header
	if err := json.Unmarshal(pending.Data, &h); err != nil {
		return NothingToRecover, fmt.Errorf("журнал развёртывания повреждён: %w", err)
	}
	if err := d.rollback(pending.ID, h, pending.Done); err != nil {
		return NothingToRecover, fmt.Errorf("откат прерванного развёртывания: %w", err)
	}
	if err := journal.Clear(d.state); err != nil {
		return NothingToRecover, err
	}
	return RolledBack, nil
}
