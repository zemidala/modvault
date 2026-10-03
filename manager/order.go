package manager

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/profile"
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
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return nil, game.Ordering{}, err
		}
		o := ordered{entry: e, name: v.Name}
		if l, err := a.layout(v); err == nil {
			infos = append(infos, game.ModInfo{ModID: e.ModID, Enabled: e.Enabled, Layout: l})
			if l.Role == game.RoleNone {
				o.folders = l.Folders
			}
			root := a.store.FilesDir(e.ModID, e.VersionID)
			files[e.ModID] = make(map[string]string, len(l.Paths))
			for src, dst := range l.Paths {
				files[e.ModID][strings.ToLower(dst)] = filepath.Join(root, filepath.FromSlash(src))
			}
		}
		mods = append(mods, o)
	}
	ord := a.game.Ordering(game.OrderContext{
		Dir: a.settings.GameDir, Mods: infos,
		Read: func(modID, gamePath string) ([]byte, error) {
			src, ok := files[modID][strings.ToLower(gamePath)]
			if !ok {
				return nil, os.ErrNotExist
			}
			return os.ReadFile(src)
		},
	})
	return mods, ord, nil
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
}

// sorted возвращает профиль с модами в целевом порядке. Выключенные моды
// и моды без папок (загрузчик, фреймворк, наборы файлов) остаются на своих
// местах: переставляются только те, кого игра грузит по порядку.
func (a *Manager) sorted(p profile.Profile) (profile.Profile, SortPlan, error) {
	mods, ord, err := a.orderInfo(p)
	if err != nil {
		return p, SortPlan{}, err
	}
	res := targetOrder(loadedFolders(mods), ord)
	plan := SortPlan{Cycles: res.Cycles, Auto: ord.Auto, Moves: []string{}}

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
func (a *Manager) orderIssues(p profile.Profile) ([]Issue, string) {
	mods, ord, err := a.orderInfo(p)
	if err != nil {
		return nil, ""
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
		return issues, note
	}
	if n := len(rules.Violations(loaded, ord.Rules)); n > 0 {
		issues = append(issues, Issue{
			Title:  i18n.Sprintf("Порядок загрузки нарушает %d %s модов", n, plural(n, "правило", "правила", "правил")),
			Detail: i18n.T("Авторы модов указали, что должно грузиться раньше, а что позже"),
			Level:  LevelWarn, Action: i18n.T("Отсортировать"), Command: "Sort",
		})
	}
	return issues, ""
}
