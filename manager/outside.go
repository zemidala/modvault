package manager

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/i18n"
)

// Моды вне Modvault: папки в папке модов игры, которые программа туда не
// клала. Загрузчик грузит их наравне с остальными, поэтому окно их
// показывает и предлагает взять под управление.

// Виды папок вне Modvault.
const (
	outsideManual   = "manual"   // мод положили в игру вручную
	outsideLink     = "link"     // ссылка на папку — обычно мод в разработке
	outsideLeftover = "leftover" // пустая папка, оставшаяся от Vortex
)

// vortexLeftover — метка, которую Vortex кладёт в каждую папку, созданную им.
const vortexLeftover = "__folder_managed_by_vortex"

// outsidePrefix отличает строки модов вне Modvault от модов хранилища.
const outsidePrefix = "outside:"

type outside struct {
	Folder string // имя папки в папке модов
	Kind   string
	Target string // куда ведёт ссылка
}

// modsPath — путь в игре к папке мода folder, через «/».
func (a *Manager) modsPath(folder string) string {
	return path.Join(a.game.ModsDir(), folder)
}

// outsideFolders перечисляет папки модов игры, которые Modvault туда не
// клал и не ведёт как мод в разработке.
func (a *Manager) outsideFolders() []outside {
	dir := a.game.ModsDir()
	// Пока игрой управляет другой менеджер, все её моды — его, а не «вне Modvault».
	if dir == "" || a.deployer == nil || a.deployErr != nil || len(a.managers()) > 0 {
		return nil
	}
	owned := map[string]bool{}
	if m, err := a.deployer.Manifest(); err == nil {
		for _, e := range m.Entries() {
			if rest, ok := strings.CutPrefix(e.Path, dir+"/"); ok {
				if folder, _, nested := strings.Cut(rest, "/"); nested {
					owned[strings.ToLower(folder)] = true
				}
			}
		}
	}
	for _, v := range a.linkedVersions() {
		owned[strings.ToLower(path.Base(v.LinkPath))] = true
	}
	root := filepath.Join(a.settings.GameDir, filepath.FromSlash(dir))
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []outside
	for _, e := range entries {
		name := e.Name()
		if owned[strings.ToLower(name)] {
			continue
		}
		full := filepath.Join(root, name)
		if target, ok := fsx.LinkTarget(full); ok {
			out = append(out, outside{Folder: name, Kind: outsideLink, Target: target})
			continue
		}
		if !e.IsDir() {
			continue
		}
		files, marks := 0, 0
		filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files++
				if d.Name() == vortexLeftover {
					marks++
				}
			}
			return nil
		})
		switch {
		case files == 0:
		case files == marks:
			out = append(out, outside{Folder: name, Kind: outsideLeftover})
		default:
			out = append(out, outside{Folder: name, Kind: outsideManual})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Folder) < strings.ToLower(out[j].Folder) })
	return out
}

// findOutside ищет папку вне Modvault по имени или идентификатору строки.
func (a *Manager) findOutside(id, kind string) (outside, error) {
	folder := strings.TrimPrefix(id, outsidePrefix)
	for _, o := range a.outsideFolders() {
		if o.Folder == folder && o.Kind == kind {
			return o, nil
		}
	}
	return outside{}, i18n.Errorf("папки «%s» вне Modvault уже нет", folder)
}

// outsideRows — строки списка для модов вне Modvault.
func outsideRows(list []outside) []Mod {
	var rows []Mod
	for _, o := range list {
		if o.Kind == outsideLeftover {
			continue
		}
		row := Mod{
			ID: outsidePrefix + o.Folder, Name: o.Folder, Version: "—", Enabled: true,
			Sets: []string{}, Outside: o.Kind, Link: o.Target,
			State: i18n.T("В игре, вне Modvault"), Level: LevelOK,
			Source: i18n.T("положен в игру вручную"),
		}
		if o.Kind == outsideLink {
			row.Source = i18n.Sprintf("ссылка на %s", o.Target)
		}
		rows = append(rows, row)
	}
	return rows
}

// leftoverIssue — замечание о пустых папках, оставшихся от Vortex.
func leftoverIssue(list []outside) (Issue, bool) {
	var names []string
	for _, o := range list {
		if o.Kind == outsideLeftover {
			names = append(names, o.Folder)
		}
	}
	if len(names) == 0 {
		return Issue{}, false
	}
	n := len(names)
	return Issue{
		Title:   i18n.Sprintf("%d %s от Vortex", n, plural(n, "пустая папка", "пустые папки", "пустых папок")),
		Detail:  i18n.Sprintf("%s. В них только метки Vortex, файлов модов нет: он оставил их, когда снимал моды. Игре они не нужны", strings.Join(names, ", ")),
		Level:   LevelWarn,
		Key:     "leftovers",
		Action:  i18n.T("Убрать в Корзину"),
		Command: "CleanLeftovers",
	}, true
}

// linkedVersions — моды в разработке из хранилища.
func (a *Manager) linkedVersions() []store.Version {
	mods, _, err := a.store.List()
	if err != nil {
		return nil
	}
	var out []store.Version
	for _, m := range mods {
		if v := m.Latest(); v.Link != "" {
			out = append(out, v)
		}
	}
	return out
}

// linkChange — что сделать со ссылкой мода в разработке, чтобы игра
// совпала с набором.
type linkChange struct {
	ModID, Name string
	Path        string // в игре, через «/»
	Target      string
	Kind        deploy.ChangeKind
	Blocked     bool // по этому пути лежит обычная папка: ссылку положить некуда
}

// linkChanges сравнивает ссылки модов в разработке с набором p.
func (a *Manager) linkChanges(p profile.Profile) []linkChange {
	if a.deployer == nil || a.deployErr != nil {
		return nil
	}
	var out []linkChange
	for _, v := range a.linkedVersions() {
		want := false
		if i := p.Index(v.ModID); i >= 0 {
			want = p.Entries[i].Enabled
		}
		full := filepath.Join(a.settings.GameDir, filepath.FromSlash(v.LinkPath))
		cur, isLink := fsx.LinkTarget(full)
		_, statErr := os.Lstat(full)
		exists := statErr == nil
		c := linkChange{ModID: v.ModID, Name: v.Name, Path: v.LinkPath, Target: v.Link}
		switch {
		case want && isLink && fsx.SamePath(cur, v.Link):
			continue
		case want && !exists:
			c.Kind = deploy.Add
		case want && isLink:
			c.Kind = deploy.Replace
		case want:
			c.Kind, c.Blocked = deploy.Add, true
		case isLink && fsx.SamePath(cur, v.Link):
			c.Kind = deploy.Remove
		default:
			continue
		}
		out = append(out, c)
	}
	return out
}

// applyLinks ставит и снимает ссылки модов в разработке. Каждое действие —
// одна операция файловой системы, поэтому журнал им не нужен.
func (a *Manager) applyLinks(changes []linkChange) (int, error) {
	done := 0
	for _, c := range changes {
		if c.Blocked {
			continue
		}
		full := filepath.Join(a.settings.GameDir, filepath.FromSlash(c.Path))
		if c.Kind != deploy.Add {
			if err := fsx.RemoveLink(full); err != nil {
				return done, err
			}
		}
		if c.Kind != deploy.Remove {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return done, err
			}
			if err := fsx.LinkDir(c.Target, full); err != nil {
				return done, err
			}
		}
		done++
	}
	return done, nil
}

// linkLines описывает изменения ссылок для плана развёртывания.
func linkLines(changes []linkChange) []string {
	var out []string
	for _, c := range changes {
		switch {
		case c.Blocked:
		case c.Kind == deploy.Add:
			out = append(out, i18n.Sprintf("%s — положить ссылку на папку проекта", c.Name))
		case c.Kind == deploy.Replace:
			out = append(out, i18n.Sprintf("%s — заменить ссылку на папку проекта", c.Name))
		default:
			out = append(out, i18n.Sprintf("%s — убрать ссылку на папку проекта", c.Name))
		}
	}
	return out
}

// linkCount — сколько изменений ссылок развёртывание действительно сделает.
func linkCount(changes []linkChange) int {
	n := 0
	for _, c := range changes {
		if !c.Blocked {
			n++
		}
	}
	return n
}

// blockedIssues — ссылки, которые положить некуда: путь занят папкой.
func blockedIssues(changes []linkChange) []Issue {
	var out []Issue
	for _, c := range changes {
		if c.Blocked {
			out = append(out, Issue{
				Title:  i18n.Sprintf("Ссылку на «%s» положить некуда", c.Name),
				Detail: i18n.Sprintf("В игре по пути %s лежит обычная папка. Уберите её — и при развёртывании на её месте появится ссылка на %s", c.Path, c.Target),
				Level:  LevelWarn,
			})
		}
	}
	return out
}

// enableIn включает мод в наборе; новый мод набор мог уже подхватить сам.
func enableIn(p *profile.Profile, v store.Version) error {
	if p.Index(v.ModID) >= 0 {
		return p.SetEnabled(v.ModID, true)
	}
	return p.Add(v.ModID, v.ID)
}

// OutsideResult — состояние окна после действия с модом вне Modvault.
type OutsideResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
}

// TakeOutside берёт в Modvault мод, положенный в игру вручную: его файлы
// переходят в хранилище, мод включается в текущем наборе и остаётся в игре
// таким же — теперь его можно выключать, переставлять и включать в наборы.
func (a *Manager) TakeOutside(id string) (OutsideResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.bisecting(); err != nil {
		return OutsideResult{}, err
	}
	o, err := a.findOutside(id, outsideManual)
	if err != nil {
		return OutsideResult{}, err
	}
	deployed, ok := a.deployedProfile()
	if !ok {
		return OutsideResult{}, i18n.NewError("сначала разверните набор: так программа знает, что уже лежит в игре")
	}
	game := a.settings.GameDir
	rel := a.modsPath(o.Folder)
	folder := filepath.Join(game, filepath.FromSlash(rel))
	var files []string
	err = filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == vortexLeftover {
			return err
		}
		r, err := filepath.Rel(game, p)
		files = append(files, filepath.ToSlash(r))
		return err
	})
	if err != nil {
		return OutsideResult{}, err
	}
	modID := store.Slug(o.Folder)
	if mods, _, err := a.store.List(); err == nil {
		for _, m := range mods {
			if m.ID == modID {
				return OutsideResult{}, i18n.Errorf("в хранилище уже есть мод «%s»: возьмите папку под другим именем или удалите тот мод", m.Latest().Name)
			}
		}
	}
	v, err := a.store.AddFiles(game, files, store.Info{Name: o.Folder, AsIs: true})
	if err != nil {
		return OutsideResult{}, err
	}
	undoStore := func() { a.store.RemoveMod(v.ModID, true) }

	p, _, err := a.loadProfile()
	if err == nil {
		err = enableIn(&p, v)
	}
	if err == nil {
		err = a.profiles.Save(p)
	}
	if err != nil {
		undoStore()
		return OutsideResult{}, err
	}
	undoProfile := func() {
		if p.Remove(v.ModID) == nil {
			a.profiles.Save(p)
		}
		undoStore()
	}

	// Папку убираем с дороги, а развёртывание кладёт на её место те же файлы
	// из хранилища. Сбой — и папка возвращается.
	aside := filepath.Join(a.gameState(game), "taking", o.Folder)
	os.RemoveAll(aside)
	if err := os.MkdirAll(filepath.Dir(aside), 0o755); err != nil {
		undoProfile()
		return OutsideResult{}, err
	}
	if err := os.Rename(folder, aside); err != nil {
		undoProfile()
		return OutsideResult{}, i18n.Errorf("папку %s не удалось освободить: %w", folder, err)
	}
	enableIn(&deployed, v)
	sources, _, err := a.sources(deployed)
	var plan *deploy.Plan
	if err == nil {
		plan, err = a.deployer.Plan(sources, deployed.Winners)
	}
	if err == nil {
		_, err = a.deployer.Apply(plan)
	}
	if err != nil {
		if rerr := os.Rename(aside, folder); rerr != nil {
			err = errors.Join(err, i18n.Errorf("папка мода осталась в %s", aside))
		}
		undoProfile()
		return OutsideResult{}, err
	}
	a.recovery = deploy.NothingToRecover
	a.rememberProfile(deployed)
	fsx.Trash(aside) // файлы мода уже в хранилище и в игре

	msg := i18n.Sprintf("«%s» теперь в Modvault: файлы в хранилище, мод включён в наборе «%s» и остаётся в игре", v.Name, p.Name)
	a.note(EventInstall, msg)
	st, err := a.state()
	return OutsideResult{State: st, Message: msg}, err
}

// LinkOutside подключает ссылку на папку как мод в разработке: в игре
// остаётся ссылка на папку проекта, правки сразу видны в игре, а мод можно
// выключать и включать в наборы.
func (a *Manager) LinkOutside(id string) (OutsideResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.bisecting(); err != nil {
		return OutsideResult{}, err
	}
	o, err := a.findOutside(id, outsideLink)
	if err != nil {
		return OutsideResult{}, err
	}
	v, err := a.store.AddLink(o.Folder, a.modsPath(o.Folder), o.Target)
	if err != nil {
		return OutsideResult{}, err
	}
	p, _, err := a.loadProfile()
	if err == nil {
		err = enableIn(&p, v)
	}
	if err == nil {
		err = a.profiles.Save(p)
	}
	if err != nil {
		a.store.RemoveMod(v.ModID, true)
		return OutsideResult{}, err
	}
	// Мод уже в игре: служебные файлы (порядок загрузки) догоняют его сразу.
	if deployed, ok := a.deployedProfile(); ok && enableIn(&deployed, v) == nil {
		if sources, _, err := a.sources(deployed); err == nil {
			if plan, err := a.deployer.Plan(sources, deployed.Winners); err == nil && !plan.Empty() {
				if _, err := a.deployer.Apply(plan); err == nil {
					a.recovery = deploy.NothingToRecover
					a.rememberProfile(deployed)
				}
			}
		}
	}
	msg := i18n.Sprintf("«%s» подключён как мод в разработке: в игре ссылка на %s. Его можно выключать и включать в наборы", v.Name, o.Target)
	a.note(EventInstall, msg)
	st, err := a.state()
	return OutsideResult{State: st, Message: msg}, err
}

// CleanLeftovers убирает в Корзину пустые папки, оставшиеся от Vortex.
func (a *Manager) CleanLeftovers() (OutsideResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	var errs []error
	for _, o := range a.outsideFolders() {
		if o.Kind != outsideLeftover {
			continue
		}
		if err := fsx.Trash(filepath.Join(a.settings.GameDir, filepath.FromSlash(a.modsPath(o.Folder)))); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	msg := i18n.Sprintf("Убрано в Корзину: %d %s от Vortex", n, plural(n, "пустая папка", "пустые папки", "пустых папок"))
	if n > 0 {
		a.note(EventRemove, msg)
	}
	st, err := a.state()
	return OutsideResult{State: st, Message: msg}, errors.Join(append(errs, err)...)
}

// OutsideNames — названия модов вне Modvault: игра грузит их всегда.
func (a *Manager) OutsideNames() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, o := range a.outsideFolders() {
		if o.Kind != outsideLeftover {
			out = append(out, o.Folder)
		}
	}
	return out
}

// LinkTargets — папки, на которые ведут ссылки модов в разработке и ссылки
// вне Modvault: только их окно открывает в Проводнике.
func (a *Manager) LinkTargets() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, v := range a.linkedVersions() {
		out = append(out, v.Link)
	}
	for _, o := range a.outsideFolders() {
		if o.Target != "" {
			out = append(out, o.Target)
		}
	}
	return out
}
