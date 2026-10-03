package manager

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

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/manifest"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/i18n"
	"github.com/zemidala/modvault/vortex"
)

// Усыновление: Modvault принимает моды, развёрнутые Vortex, не трогая
// файлы игры, и отсоединяет Vortex. Возврат кладёт всё обратно так, как
// оставил Vortex: файлы снова становятся ссылками на его хранилище.
//
// Всё, что нужно для возврата, лежит в папке состояния игры:
//
//	adopted/record.json              когда и откуда принято
//	adopted/vortex.deployment.json   учёт Vortex
//	adopted/files/<путь>             служебные файлы игры на момент усыновления
const (
	adoptedDir = "adopted"
	recordFile = "record.json"
)

type adoptRecord struct {
	Adopted    time.Time `json:"adopted"`
	Generation string    `json:"generation"`
	Staging    string    `json:"staging"`
	// Files — служебные файлы игры (порядок загрузки, база бандлов), которые
	// при возврате должны стать такими, какими были.
	Files []string `json:"files"`
}

func (a *Manager) adoptedPath(parts ...string) string {
	return filepath.Join(append([]string{a.gameState(a.settings.GameDir), adoptedDir}, parts...)...)
}

func (a *Manager) loadRecord() (*adoptRecord, error) {
	data, err := os.ReadFile(a.adoptedPath(recordFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r adoptRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", recordFile, err)
	}
	return &r, nil
}

// managers — другие менеджеры модов в папке игры, кроме тех, что
// пользователь разрешил не учитывать.
func (a *Manager) managers() []string {
	var out []string
	for _, m := range a.game.Managers(a.settings.GameDir) {
		ignored := false
		for _, i := range a.settings.IgnoredManagers {
			ignored = ignored || strings.EqualFold(i, m)
		}
		if !ignored {
			out = append(out, m)
		}
	}
	return out
}

// Adopted сообщает, что Modvault управляет игрой вместо Vortex.
func (a *Manager) Adopted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, err := a.loadRecord()
	return err == nil && r != nil
}

// AdoptReport — что принято у Vortex или будет принято.
type AdoptReport struct {
	Mods      int      `json:"mods"`      // модов в хранилище Vortex
	Enabled   int      `json:"enabled"`   // из них развёрнуто
	Files     int      `json:"files"`     // файлов в игре
	Pinned    int      `json:"pinned"`    // конфликтов, где победитель закреплён как у Vortex
	Originals int      `json:"originals"` // сохранённых оригиналов файлов игры
	Staging   string   `json:"staging"`
	Others    []string `json:"others"`   // другие менеджеры, которые перестанут учитываться
	Problems  []string `json:"problems"` // что в игре расходится с хранилищем Vortex
}

// adoptedMod — мод из хранилища Vortex.
type adoptedMod struct {
	source  string
	files   []string
	info    store.Info
	version store.Version
	layout  game.Layout
	enabled bool
}

// Adopt принимает моды Vortex. С dryRun только сообщает, что будет сделано.
func (a *Manager) Adopt(dryRun bool) (AdoptReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rep, err := a.adopt(dryRun)
	if !dryRun {
		if err == nil {
			a.note(EventVortex, i18n.Sprintf("Управление перенято у Vortex: модов %d, файлов в игре %d", rep.Mods, rep.Files))
		}
		a.noteError(i18n.T("Перенять у Vortex"), err)
	}
	return rep, err
}

func (a *Manager) adopt(dryRun bool) (AdoptReport, error) {
	var rep AdoptReport
	if a.openErr != nil {
		return rep, a.openErr
	}
	if a.deployer == nil || a.deployErr != nil {
		return rep, errors.Join(i18n.NewError("сначала выберите папку игры"), a.deployErr)
	}
	gameDir := a.settings.GameDir
	if rec, err := a.loadRecord(); err != nil || rec != nil {
		return rep, errors.Join(err, i18n.NewError("моды Vortex уже приняты"))
	}
	if m, err := a.deployer.Manifest(); err != nil || m.Len() > 0 {
		return rep, errors.Join(err, i18n.NewError("Modvault уже разворачивал моды в эту игру: снимите их, прежде чем принимать моды Vortex"))
	}

	dep, err := vortex.ReadDeployment(gameDir)
	if err != nil {
		return rep, i18n.Errorf("учёт Vortex: %w", err)
	}
	staging, err := vortex.FindStaging(gameDir, dep)
	if err != nil {
		return rep, err
	}
	rep.Staging = staging
	for _, m := range a.managers() {
		if m != "Vortex" {
			rep.Others = append(rep.Others, m)
		}
	}

	mods, err := readStaging(staging, dep)
	if err != nil {
		return rep, err
	}
	rep.Mods = len(mods)
	for _, m := range mods {
		if m.enabled {
			rep.Enabled++
		}
	}
	rep.Files = len(dep.Files)

	// В хранилище Modvault — копии, чтобы не зависеть от Vortex.
	for _, m := range mods {
		if dryRun {
			m.layout = a.game.Describe(m.files)
			continue
		}
		root := filepath.Join(staging, m.source)
		v, err := a.store.AddFiles(root, m.files, m.info)
		if errors.Is(err, fs.ErrExist) {
			// Принят раньше. Если с тех пор файлы в Vortex поменялись при той же
			// версии, новая сборка ляжет отдельной версией по содержимому.
			if v, err = a.store.Get(v.ModID, v.ID); err == nil && !sameFiles(v, root, m.files) {
				info := m.info
				info.Version = ""
				v, err = a.store.AddFiles(root, m.files, info)
				if errors.Is(err, fs.ErrExist) {
					v, err = a.store.Get(v.ModID, v.ID)
				}
			}
		}
		if err != nil {
			return rep, fmt.Errorf("%s: %w", m.source, err)
		}
		m.version = v
		if m.layout, err = a.layout(v); err != nil {
			return rep, fmt.Errorf("%s: %w", m.source, err)
		}
	}

	bySource := make(map[string]*adoptedMod, len(mods))
	for _, m := range mods {
		bySource[m.source] = m
	}
	order := adoptOrder(mods, readLoadOrder(gameDir))

	// Учёт: каждый файл, развёрнутый Vortex, — файл своего мода.
	var entries []deploy.AdoptEntry
	vortexWinner := map[string]string{} // путь → папка мода, чей файл положил Vortex
	for _, f := range dep.Files {
		m := bySource[f.Source]
		if m == nil {
			return rep, i18n.Errorf("Vortex развернул %s из мода %s, которого нет в его хранилище", f.RelPath, f.Source)
		}
		src := filepath.Join(staging, f.Source, filepath.FromSlash(f.RelPath))
		gameFile := filepath.Join(gameDir, filepath.FromSlash(f.RelPath))
		want, size, err := fsx.HashFile(src)
		if err != nil {
			return rep, err
		}
		if got, _, err := fsx.HashFile(gameFile); err != nil || got != want {
			rep.Problems = append(rep.Problems, f.RelPath+i18n.T(": файл в игре отличается от хранилища Vortex или пропал"))
		}
		original := gameFile + ".vortex_backup"
		if _, err := os.Stat(original); err != nil {
			original = ""
		} else {
			rep.Originals++
		}
		vortexWinner[strings.ToLower(f.RelPath)] = f.Source
		entries = append(entries, deploy.AdoptEntry{
			Entry: manifest.Entry{
				Path: f.RelPath, ModID: m.version.ModID, VersionID: m.version.ID,
				Hash: want, Size: size, Method: manifest.MethodLink,
			},
			Original: original,
		})
	}

	// Профиль в порядке Vortex; победители конфликтов — как выбрал Vortex.
	p := profile.Profile{Name: a.profileName(), Winners: map[string]string{}}
	var sources []deploy.Source
	for _, m := range order {
		if !dryRun {
			p.Entries = append(p.Entries, profile.Entry{ModID: m.version.ModID, VersionID: m.version.ID, Enabled: m.enabled})
		}
		if m.enabled {
			s := deploy.Source{ModID: m.source}
			for _, f := range m.files {
				s.Files = append(s.Files, deploy.File{Path: m.layout.Paths[f]})
			}
			sources = append(sources, s)
		}
	}
	_, conflicts := deploy.Desired(sources, nil)
	for _, c := range conflicts {
		winner := vortexWinner[strings.ToLower(c.Path)]
		if winner != "" && winner != c.Winner {
			p.Winners[c.Path] = bySource[winner].version.ModID
			rep.Pinned++
		}
	}

	// Служебные файлы игры: сохранить их нынешний вид для возврата и, где
	// другой инструмент оставил оригинал, принять их под учёт.
	generated, err := a.adoptGenerated(order)
	if err != nil {
		return rep, err
	}
	for _, g := range generated {
		if g.entry != nil {
			entries = append(entries, *g.entry)
			rep.Originals++
		}
	}
	if dryRun {
		return rep, nil
	}

	// Записываем всё, что нужно для возврата, до того как что-то менять.
	gen := "adopted-" + time.Now().UTC().Format("20060102-150405")
	rec := adoptRecord{Adopted: time.Now().UTC().Truncate(time.Second), Generation: gen, Staging: staging}
	if err := os.MkdirAll(a.adoptedPath("files"), 0o755); err != nil {
		return rep, err
	}
	if err := fsx.CopyFile(filepath.Join(gameDir, vortex.DeploymentFile), a.adoptedPath(vortex.DeploymentFile)); err != nil {
		return rep, err
	}
	for _, g := range generated {
		if g.current == "" {
			continue
		}
		dst := a.adoptedPath("files", filepath.FromSlash(g.path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return rep, err
		}
		if err := fsx.CopyFile(g.current, dst); err != nil {
			return rep, err
		}
		rec.Files = append(rec.Files, g.path)
	}

	// Прежний профиль: моды, которых нет у Vortex, остаются выключенными в конце.
	if old, _, err := a.loadProfile(); err == nil {
		for _, e := range old.Entries {
			if p.Index(e.ModID) < 0 {
				e.Enabled = false
				p.Entries = append(p.Entries, e)
			}
		}
	}
	if err := a.profiles.Save(p); err != nil {
		return rep, err
	}
	if err := a.deployer.Adopt(gen, entries); err != nil {
		return rep, err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return rep, err
	}
	if err := fsx.WriteFile(a.adoptedPath(recordFile), data); err != nil {
		return rep, err
	}

	// Единственное изменение в игре: учёт Vortex уходит к нам. Без него Vortex
	// считает, что ничего не разворачивал, и не тронет файлы.
	if err := os.Remove(filepath.Join(gameDir, vortex.DeploymentFile)); err != nil {
		return rep, err
	}
	a.settings.IgnoredManagers = append(a.settings.IgnoredManagers, rep.Others...)
	return rep, a.saveSettings()
}

// sameFiles сообщает, что версия в хранилище совпадает с файлами в root.
func sameFiles(v store.Version, root string, files []string) bool {
	if len(v.Files) != len(files) {
		return false
	}
	for _, f := range v.Files {
		hash, _, err := fsx.HashFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil || hash != f.Hash {
			return false
		}
	}
	return true
}

// readStaging перечисляет моды в хранилище Vortex: развёрнутые и выключенные.
func readStaging(staging string, dep *vortex.Deployment) ([]*adoptedMod, error) {
	deployed := map[string]bool{}
	for _, s := range dep.Sources() {
		deployed[s] = true
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return nil, err
	}
	var mods []*adoptedMod
	for _, e := range entries {
		if !e.IsDir() || vortex.IsMarker(e.Name()) {
			continue
		}
		root := filepath.Join(staging, e.Name())
		var files []string
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || vortex.IsMarker(d.Name()) {
				return err
			}
			rel, err := filepath.Rel(root, p)
			files = append(files, filepath.ToSlash(rel))
			return err
		})
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			continue
		}
		sort.Strings(files)
		mods = append(mods, &adoptedMod{source: e.Name(), files: files, info: vortex.ParseSource(e.Name()), enabled: deployed[e.Name()]})
	}
	for s := range deployed {
		found := false
		for _, m := range mods {
			found = found || m.source == s
		}
		if !found {
			return nil, i18n.Errorf("мода %s, развёрнутого Vortex, нет в его хранилище %s", s, staging)
		}
	}
	return mods, nil
}

// readLoadOrder читает порядок загрузки из игры: папки модов по строкам.
func readLoadOrder(gameDir string) []string {
	data, err := os.ReadFile(filepath.Join(gameDir, "mods", "mod_load_order.txt"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			out = append(out, line)
		}
	}
	return out
}

// adoptOrder выстраивает моды: загрузчик и фреймворк, затем моды в порядке
// загрузки, затем прочие включённые, затем выключенные.
func adoptOrder(mods []*adoptedMod, loadOrder []string) []*adoptedMod {
	rank := map[string]int{}
	for i, folder := range loadOrder {
		if _, ok := rank[strings.ToLower(folder)]; !ok {
			rank[strings.ToLower(folder)] = i
		}
	}
	key := func(m *adoptedMod) (int, int) {
		if !m.enabled {
			return 3, 0
		}
		if m.layout.Role != "" {
			return 0, 0
		}
		best := -1
		for _, f := range m.layout.Folders {
			if r, ok := rank[strings.ToLower(f)]; ok && (best < 0 || r < best) {
				best = r
			}
		}
		if best >= 0 {
			return 1, best
		}
		return 2, 0
	}
	out := append([]*adoptedMod(nil), mods...)
	sort.SliceStable(out, func(i, j int) bool {
		gi, ri := key(out[i])
		gj, rj := key(out[j])
		if gi != gj {
			return gi < gj
		}
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i].source) < strings.ToLower(out[j].source)
	})
	return out
}

type adoptedGenerated struct {
	path    string
	current string             // файл в игре сейчас; пусто — его нет
	entry   *deploy.AdoptEntry // принять под учёт; nil — не принимать
}

// adoptGenerated разбирается со служебными файлами игры. Каждый, что есть
// в игре, запоминается для возврата. Если другой инструмент оставил
// оригинал и наш собранный из него файл совпадает с нынешним байт в байт,
// файл принимается под учёт, а оригинал становится резервной копией.
func (a *Manager) adoptGenerated(order []*adoptedMod) ([]adoptedGenerated, error) {
	gameDir := a.settings.GameDir
	originals := a.game.Originals(gameDir)
	var infos []game.ModInfo
	for _, m := range order {
		infos = append(infos, game.ModInfo{ModID: m.version.ModID, Enabled: m.enabled, Layout: m.layout})
	}
	generated, _, err := a.game.Generate(game.Context{
		Dir: gameDir, WorkDir: filepath.Join(a.gameState(gameDir), "generated"), Mods: infos,
		Original: func(rel string) (string, error) {
			if o, ok := originals[rel]; ok {
				return o, nil
			}
			return filepath.Join(gameDir, filepath.FromSlash(rel)), nil
		},
	})
	if err != nil {
		return nil, err
	}

	var out []adoptedGenerated
	for _, g := range generated {
		cur := filepath.Join(gameDir, filepath.FromSlash(g.Path))
		item := adoptedGenerated{path: g.Path}
		if _, err := os.Stat(cur); err == nil {
			item.current = cur
		}
		if o, ok := originals[g.Path]; ok && item.current != "" {
			want, size, err := fsx.HashFile(g.Src)
			if err != nil {
				return nil, err
			}
			if got, _, err := fsx.HashFile(cur); err == nil && got == want {
				item.entry = &deploy.AdoptEntry{
					Entry:    manifest.Entry{Path: g.Path, ModID: generatedMod, VersionID: "1", Hash: want, Size: size, Method: manifest.MethodCopy},
					Original: o,
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// ReleaseReport — что вернётся Vortex.
type ReleaseReport struct {
	Changes int    `json:"changes"` // файлов в игре, которые изменятся
	Staging string `json:"staging"`
}

// Release возвращает управление Vortex: игра становится такой, какой её
// оставил Vortex, учёт Modvault для неё забывается. Моды остаются в
// хранилище Modvault. С dryRun только сообщает, что будет сделано.
func (a *Manager) Release(dryRun bool) (ReleaseReport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rep, err := a.release(dryRun)
	if !dryRun {
		if err == nil {
			a.note(EventVortex, i18n.T("Управление возвращено Vortex"))
		}
		a.noteError(i18n.T("Вернуть Vortex"), err)
	}
	return rep, err
}

func (a *Manager) release(dryRun bool) (ReleaseReport, error) {
	var rep ReleaseReport
	if a.deployer == nil || a.deployErr != nil {
		return rep, errors.Join(i18n.NewError("сначала выберите папку игры"), a.deployErr)
	}
	rec, err := a.loadRecord()
	if err != nil {
		return rep, err
	}
	if rec == nil {
		return rep, i18n.NewError("моды Vortex не принимались: возвращать нечего")
	}
	rep.Staging = rec.Staging
	data, err := os.ReadFile(a.adoptedPath(vortex.DeploymentFile))
	if err != nil {
		return rep, err
	}
	dep, err := vortex.ParseDeployment(data)
	if err != nil {
		return rep, err
	}

	// Желаемое: ровно то, что развернул Vortex, — ссылками на его хранилище,
	// и служебные файлы такими, какими они были при усыновлении.
	index := map[string]int{} // папка мода Vortex → место в sources
	var sources []deploy.Source
	for _, f := range dep.Files {
		src := filepath.Join(rec.Staging, f.Source, filepath.FromSlash(f.RelPath))
		hash, size, err := fsx.HashFile(src)
		if err != nil {
			return rep, i18n.Errorf("хранилище Vortex: %w", err)
		}
		i, ok := index[f.Source]
		if !ok {
			i = len(sources)
			index[f.Source] = i
			sources = append(sources, deploy.Source{ModID: "vortex:" + f.Source, VersionID: "vortex"})
		}
		sources[i].Files = append(sources[i].Files, deploy.File{Path: f.RelPath, Src: src, Hash: hash, Size: size})
	}
	if len(rec.Files) > 0 {
		s := deploy.Source{ModID: generatedMod, VersionID: "adopted"}
		for _, rel := range rec.Files {
			src := a.adoptedPath("files", filepath.FromSlash(rel))
			hash, size, err := fsx.HashFile(src)
			if err != nil {
				return rep, err
			}
			s.Files = append(s.Files, deploy.File{Path: rel, Src: src, Hash: hash, Size: size})
		}
		sources = append(sources, s)
	}

	plan, err := a.deployer.Plan(sources, nil)
	if err != nil {
		return rep, err
	}
	rep.Changes = len(plan.Changes)
	if dryRun {
		return rep, nil
	}
	if !plan.Empty() {
		if _, err := a.deployer.Apply(plan); err != nil {
			return rep, err
		}
	}
	if after, err := a.deployer.Plan(sources, nil); err != nil || !after.Empty() {
		return rep, errors.Join(err, i18n.NewError("игра не совпала с состоянием Vortex после возврата"))
	}

	// Vortex снова видит свой учёт; Modvault забывает свой.
	if err := fsx.WriteFile(filepath.Join(a.settings.GameDir, vortex.DeploymentFile), data); err != nil {
		return rep, err
	}
	if err := a.deployer.Forget(); err != nil {
		return rep, err
	}
	if err := os.RemoveAll(a.adoptedPath()); err != nil {
		return rep, err
	}
	a.settings.IgnoredManagers = nil
	a.returnNxm() // ссылки с сайта снова открывает тот, кто открывал до нас
	return rep, a.saveSettings()
}
