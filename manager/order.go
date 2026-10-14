package manager

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/i18n"
	"github.com/zemidala/modvault/rules"
)

// ordered — мод профиля с тем, что о нём знает игра.
type ordered struct {
	entry   profile.Entry
	name    string
	folders []string // папки, по которым мод грузится; без загрузчика и фреймворка
}

// orderInfo собирает моды профиля и то, что игра знает о порядке загрузки.
func (a *Manager) orderInfo(p profile.Profile) ([]ordered, game.Ordering, error) {
	mods := make([]ordered, 0, len(p.Entries))
	infos := make([]game.ModInfo, 0, len(p.Entries))
	files := map[string]map[string]string{} // мод → путь в игре → файл в хранилище
	links := map[string]store.Version{}     // мод в разработке: файлы — в папке проекта
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return nil, game.Ordering{}, err
		}
		o := ordered{entry: e, name: v.Name}
		if l, err := a.layout(v); err == nil {
			if l.Role == game.RoleNone {
				o.folders = l.Folders
			}
			if v.Link != "" {
				links[e.ModID] = v
				l.Paths = linkModFile(v)
			} else {
				files[e.ModID] = a.storeFiles(e, l)
				l.Paths = withoutLost(l.Paths, e.ModID, p.Winners)
			}
			infos = append(infos, game.ModInfo{ModID: e.ModID, Enabled: e.Enabled, Layout: l})
		}
		mods = append(mods, o)
	}
	ord := a.game.Ordering(game.OrderContext{Dir: a.settings.GameDir, Mods: infos, Read: modReader(files, links)})
	return mods, ord, nil
}

// linkModFile — раскладка для чтения файла .mod мода в разработке. В его
// обычной раскладке только папка (в игру идёт ссылка, а не файлы), а правила,
// версия и автор лежат в .mod в папке проекта.
func linkModFile(v store.Version) map[string]string {
	mod := v.LinkPath + "/" + path.Base(v.LinkPath) + ".mod"
	return map[string]string{path.Base(mod): mod}
}

// withoutLost убирает из раскладки мода файлы, победителем которых выбран
// другой мод: в игре лежит не его вариант. Так игра читает, например, скрипт
// загрузчика того мода, чей файл на самом деле в игре.
func withoutLost(paths map[string]string, modID string, winners map[string]string) map[string]string {
	if len(winners) == 0 {
		return paths
	}
	lost := map[string]bool{}
	for path, winner := range winners {
		if winner != modID {
			lost[strings.ToLower(path)] = true
		}
	}
	out := make(map[string]string, len(paths))
	for src, dst := range paths {
		if !lost[strings.ToLower(dst)] {
			out[src] = dst
		}
	}
	return out
}

// storeFiles — файлы версии в хранилище: путь в игре в нижнем регистре →
// файл на диске.
func (a *Manager) storeFiles(e profile.Entry, l game.Layout) map[string]string {
	root := a.store.FilesDir(e.ModID, e.VersionID)
	out := make(map[string]string, len(l.Paths))
	for src, dst := range l.Paths {
		out[strings.ToLower(dst)] = filepath.Join(root, filepath.FromSlash(src))
	}
	return out
}

// modReader читает файл мода по его пути в игре: из хранилища (files: мод →
// путь в игре в нижнем регистре → файл) или, у мода в разработке, из папки
// проекта.
func modReader(files map[string]map[string]string, links map[string]store.Version) func(modID, gamePath string) ([]byte, error) {
	return func(modID, gamePath string) ([]byte, error) {
		if v, ok := links[modID]; ok {
			if rest, ok := strings.CutPrefix(gamePath, v.LinkPath+"/"); ok {
				return os.ReadFile(filepath.Join(v.Link, filepath.FromSlash(rest)))
			}
		}
		src, ok := files[modID][strings.ToLower(gamePath)]
		if !ok {
			return nil, os.ErrNotExist
		}
		return os.ReadFile(src)
	}
}

// localDetails читает версию и автора из файлов модов, о которых Nexus
// ничего не сообщает: модов в разработке и модов с диска.
func (a *Manager) localDetails(p profile.Profile) map[string]game.Details {
	var infos []game.ModInfo
	files := map[string]map[string]string{}
	links := map[string]store.Version{}
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil || v.NexusID != 0 {
			continue
		}
		l, err := a.layout(v)
		if err != nil {
			continue
		}
		if v.Link != "" {
			links[e.ModID] = v
			l.Paths = linkModFile(v)
		} else {
			files[e.ModID] = a.storeFiles(e, l)
		}
		infos = append(infos, game.ModInfo{ModID: e.ModID, Enabled: e.Enabled, Layout: l})
	}
	if len(infos) == 0 {
		return nil
	}
	return a.game.Details(game.OrderContext{Dir: a.settings.GameDir, Mods: infos, Read: modReader(files, links)})
}

// loadedFolders — папки включённых модов в порядке профиля.
func loadedFolders(mods []ordered) []string {
	var out []string
	for _, m := range mods {
		if m.entry.Enabled {
			out = append(out, m.folders...)
		}
	}
	return out
}

// targetOrder — порядок папок, к которому приводит сортировка. Если
// загрузчик упорядочивает моды сам и его порядок известен, за основу берётся
// он: это то, что игра сделала на самом деле. Новые моды встают по правилам.
func targetOrder(current []string, ord game.Ordering) rules.Result {
	if ord.Auto && len(ord.LastOrder) > 0 {
		rank := make(map[string]int, len(ord.LastOrder))
		for i, f := range ord.LastOrder {
			rank[strings.ToLower(f)] = i
		}
		// Мод, которого в журнале нет, держится за своим соседом сверху.
		keys := make([]float64, len(current))
		last := -1.0
		for i, f := range current {
			if r, ok := rank[strings.ToLower(f)]; ok {
				last = float64(r)
				keys[i] = last
			} else {
				keys[i] = last + 0.5
			}
		}
		idx := make([]int, len(current))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
		byLog := make([]string, len(current))
		for i, j := range idx {
			byLog[i] = current[j]
		}
		current = byLog
	}
	return rules.Sort(current, ord.Rules)
}

// SortPlan — что изменит сортировка.
type SortPlan struct {
	Moves  []string   `json:"moves"` // «Scoreboard: с 12 на 3»
	Cycles [][]string `json:"cycles"`
	// Auto — загрузчик сам расставляет моды; сортировка лишь показывает в
	// списке порядок, который видит игра.
	Auto bool `json:"auto"`
	// Conflicts — конфликты файлов, которые сортировка решит: мод, который
	// по правилам должен перекрывать другой, встанет ниже него.
	Conflicts []string `json:"conflicts"`
}

// sorted возвращает профиль с модами в целевом порядке. Выключенные моды
// и моды без папок (загрузчик, фреймворк, наборы файлов) остаются на своих
// местах: переставляются только те, кого игра грузит по порядку.
func (a *Manager) sorted(p profile.Profile) (profile.Profile, SortPlan, error) {
	mods, ord, err := a.orderInfo(p)
	if err != nil {
		return p, SortPlan{}, err
	}
	extra, solved := a.conflictRules(p, mods)
	ord.Rules = append(slices.Clip(ord.Rules), extra...)
	res := targetOrder(loadedFolders(mods), ord)
	plan := SortPlan{Cycles: res.Cycles, Auto: ord.Auto, Moves: []string{}, Conflicts: solved}

	pos := make(map[string]int, len(res.Order))
	for i, f := range res.Order {
		if _, dup := pos[strings.ToLower(f)]; !dup {
			pos[strings.ToLower(f)] = i
		}
	}
	var slots []int // места профиля, которые участвуют в сортировке
	for i, m := range mods {
		if m.entry.Enabled && len(m.folders) > 0 {
			slots = append(slots, i)
		}
	}
	movable := append([]int(nil), slots...)
	first := func(i int) int {
		best := len(res.Order)
		for _, f := range mods[i].folders {
			best = min(best, pos[strings.ToLower(f)])
		}
		return best
	}
	sort.SliceStable(movable, func(x, y int) bool { return first(movable[x]) < first(movable[y]) })

	out := p
	out.Entries = append([]profile.Entry(nil), p.Entries...)
	for k, slot := range slots {
		from := movable[k]
		out.Entries[slot] = mods[from].entry
		if from != slot {
			plan.Moves = append(plan.Moves, i18n.Sprintf("%s: с %d на %d", mods[from].name, from+1, slot+1))
		}
	}
	return out, plan, nil
}

// conflictRules — правила «ниже» для конфликтов файлов, где известно, чей
// вариант должен остаться в игре (вид ordered: так сказано в правилах модов),
// а победителя пользователь не выбирал. В игре остаётся файл мода, стоящего
// ниже, поэтому такой мод встаёт после остальных участников. Второе
// значение — строки о конфликтах, где победитель от этого сменится.
func (a *Manager) conflictRules(p profile.Profile, mods []ordered) ([]rules.Rule, []string) {
	plan, err := a.plan()
	if err != nil || plan == nil {
		return nil, []string{}
	}
	folder := map[string]string{}
	for _, m := range mods {
		if m.entry.Enabled && len(m.folders) > 0 {
			folder[m.entry.ModID] = m.folders[0]
		}
	}
	var out []rules.Rule
	solved := []string{}
	for _, c := range a.conflicts(plan, p) {
		win, ok := folder[c.Suggested]
		if c.Pinned || c.Kind != ConflictOrdered || !ok {
			continue
		}
		var name string
		var others []string
		changes := false
		for _, m := range c.Mods {
			if m.ID == c.Suggested {
				name, changes = m.Name, !m.Winner
				continue
			}
			if f, ok := folder[m.ID]; ok {
				out = append(out, rules.Rule{Kind: rules.After, Mod: win, Other: f, Source: i18n.T("конфликт файлов")})
				others = append(others, i18n.Quote(m.Name))
			}
		}
		if changes && len(others) > 0 {
			solved = append(solved, i18n.Sprintf("%s перекроет %s: %d %s", i18n.Quote(name), strings.Join(others, ", "),
				c.Total, plural(c.Total, "общий файл", "общих файла", "общих файлов")))
		}
	}
	return out, solved
}

// SortPreview показывает, что изменит сортировка, ничего не меняя.
func (a *Manager) SortPreview() (SortPlan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, _, err := a.loadProfile()
	if err != nil {
		return SortPlan{}, err
	}
	_, plan, err := a.sorted(p)
	return plan, err
}

// Sort расставляет моды профиля по правилам.
func (a *Manager) Sort() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	sorted, _, err := a.sorted(p)
	if err != nil {
		return State{}, err
	}
	if err := a.profiles.Save(sorted); err != nil {
		return State{}, err
	}
	return a.state()
}

// orderIssues — замечания о зависимостях и порядке — и пояснение, кто
// задаёт порядок загрузки.
// Третье значение — зависимости: мод → моды профиля, без которых он не
// работает (по правилам require).
func (a *Manager) orderIssues(p profile.Profile) ([]Issue, string, map[string][]string) {
	mods, ord, err := a.orderInfo(p)
	if err != nil {
		return nil, "", nil
	}
	var issues []Issue
	loaded := loadedFolders(mods)

	// Имя папки → мод профиля, чтобы назвать мод по-человечески и предложить включить.
	owner := map[string]ordered{}
	for _, m := range mods {
		for _, f := range m.folders {
			owner[strings.ToLower(f)] = m
		}
	}
	display := func(folder string) string {
		if m, ok := owner[strings.ToLower(folder)]; ok {
			return m.name
		}
		return folder
	}

	requires := map[string][]string{}
	for _, r := range ord.Rules {
		from, ok1 := owner[strings.ToLower(r.Mod)]
		to, ok2 := owner[strings.ToLower(r.Other)]
		if r.Kind != rules.Requires || !ok1 || !ok2 || from.entry.ModID == to.entry.ModID || slices.Contains(requires[from.entry.ModID], to.entry.ModID) {
			continue
		}
		requires[from.entry.ModID] = append(requires[from.entry.ModID], to.entry.ModID)
	}

	for _, r := range rules.Missing(loaded, ord.Rules) {
		issue := Issue{
			Title:  i18n.Sprintf("«%s» не заработает: ему нужен мод %s", display(r.Mod), r.Other),
			Detail: i18n.T("Так сказано в ") + r.Source + i18n.T(". Этого мода нет в хранилище: добавьте его"),
			Level:  LevelWarn,
		}
		if m, ok := owner[strings.ToLower(r.Other)]; ok && !m.entry.Enabled {
			issue.Title = i18n.Sprintf("«%s» не заработает: ему нужен «%s», а он выключен", display(r.Mod), m.name)
			issue.Detail = i18n.T("Так сказано в ") + r.Source
			issue.Action, issue.Command, issue.Arg = i18n.Sprintf("Включить «%s»", m.name), "EnableMod", m.entry.ModID
		}
		issues = append(issues, issue)
	}

	res := targetOrder(loaded, ord)
	for _, c := range res.Cycles {
		names := make([]string, len(c))
		for i, f := range c {
			names[i] = display(f)
		}
		issues = append(issues, Issue{
			Title:  i18n.T("Правила модов противоречат друг другу: ") + strings.Join(names, ", "),
			Detail: i18n.T("Каждый из них должен грузиться после другого. Между собой они остаются в прежнем порядке"),
			Level:  LevelWarn,
		})
	}

	if ord.Auto {
		note := i18n.T("Порядок загрузки задаёт загрузчик модов: при запуске игры он сам расставляет их по правилам.")
		if !ord.LastOrderTime.IsZero() {
			note += i18n.T(" «Отсортировать по правилам» покажет порядок последнего запуска игры (") + ord.LastOrderTime.Format("02.01.2006 15:04") + ")."
		}
		return issues, note, requires
	}
	if n := len(rules.Violations(loaded, ord.Rules)); n > 0 {
		issues = append(issues, Issue{
			Title:  i18n.Sprintf("Порядок загрузки нарушает %d %s модов", n, plural(n, "правило", "правила", "правил")),
			Detail: i18n.T("Авторы модов указали, что должно грузиться раньше, а что позже"),
			Level:  LevelWarn, Action: i18n.T("Отсортировать"), Command: "Sort",
		})
	}
	return issues, "", requires
}
