package manager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/i18n"
	"github.com/zemidala/modvault/rules"
)

// Разбор конфликтов файлов. Конфликт — несколько модов кладут в игру один
// и тот же файл; в игре остаётся только вариант победителя. Программа не
// только сообщает о конфликте, но и объясняет, что он значит, и советует,
// как поступить.

// Виды конфликтов, от безобидных к требующим решения.
const (
	ConflictIdentical = "identical" // файлы одинаковые: неважно, чей останется
	ConflictDuplicate = "duplicate" // моды ставятся в одну папку: два варианта одного мода
	ConflictCovered   = "covered"   // один мод целиком перекрыт другим
	ConflictOrdered   = "ordered"   // автор сам указал, кто грузится позже
	ConflictOverlap   = "overlap"   // моды частично меняют одни и те же файлы
)

// maxConflictFiles — сколько путей файлов показывать в разборе.
const maxConflictFiles = 40

// ConflictMod — участник конфликта.
type ConflictMod struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Files — сколько файлов мод кладёт в игру; Shared — сколько из них
	// спорные; Lost — сколько спорных достаётся не ему.
	Files  int `json:"files"`
	Shared int `json:"shared"`
	Lost   int `json:"lost"`
	// Covered — в игре от мода ничего не остаётся: все его файлы спорные,
	// и все достаются другим.
	Covered bool `json:"covered"`
	Winner  bool `json:"winner"`  // побеждает сейчас
	Default bool `json:"default"` // побеждал бы по порядку загрузки
}

// ConflictInfo — разбор одного конфликта: набора модов с общими файлами.
type ConflictInfo struct {
	Key   string        `json:"key"`
	Mods  []ConflictMod `json:"mods"`
	Files []string      `json:"files"` // спорные файлы, не больше maxConflictFiles
	Total int           `json:"total"` // сколько спорных файлов всего
	Kind  string        `json:"kind"`
	// Advice — совет, как поступить; Suggested — мод, которого стоит
	// выбрать победителем (пусто — совета о победителе нет); Disable —
	// мод, который стоит выключить.
	Advice    string `json:"advice"`
	Suggested string `json:"suggested"`
	Disable   string `json:"disable"`
	// Pinned — победитель выбран пользователем; Resolved — решать нечего:
	// победитель выбран или файлы одинаковые.
	Pinned   bool `json:"pinned"`
	Resolved bool `json:"resolved"`
}

// conflicts разбирает конфликты плана развёртывания.
func (a *Manager) conflicts(plan *deploy.Plan, p profile.Profile) []ConflictInfo {
	type group struct {
		mods  []string
		paths []string
		wins  map[string]int // мод → сколько спорных файлов достаётся ему
	}
	var order []string
	groups := map[string]*group{}
	for _, c := range plan.Conflicts {
		if c.Winner == generatedMod {
			continue // служебный файл заменяет образец из архива — так и задумано
		}
		k := conflictKey(c.Mods)
		g, ok := groups[k]
		if !ok {
			g = &group{mods: c.Mods, wins: map[string]int{}}
			groups[k] = g
			order = append(order, k)
		}
		g.paths = append(g.paths, c.Path)
		g.wins[c.Winner]++
	}
	if len(order) == 0 {
		return nil
	}

	// Что каждый мод кладёт в игру: путь → хеш. Заодно — свои папки мода:
	// те, в которые он кладёт файл описания «папка/папка.mod». Файлы,
	// подложенные в чужую папку, своей её не делают.
	files := map[string]map[string]fsx.Hash{}
	folders := map[string][]string{}
	if sources, _, err := a.sources(p); err == nil {
		for _, s := range sources {
			m := make(map[string]fsx.Hash, len(s.Files))
			for _, f := range s.Files {
				m[strings.ToLower(f.Path)] = f.Hash
				parts := strings.Split(f.Path, "/")
				if len(parts) == 3 && strings.EqualFold(parts[0], "mods") && strings.EqualFold(parts[2], parts[1]+".mod") {
					folders[s.ModID] = append(folders[s.ModID], parts[1])
				}
			}
			files[s.ModID] = m
		}
	}
	// Правила авторов модов: кто должен грузиться позже.
	var modRules []rules.Rule
	if _, ord, err := a.orderInfo(p); err == nil {
		modRules = ord.Rules
	}
	pinned := map[string]bool{}
	for path := range p.Winners {
		pinned[strings.ToLower(path)] = true
	}
	names := a.modNames()

	out := make([]ConflictInfo, 0, len(order))
	for _, k := range order {
		g := groups[k]
		sort.Strings(g.paths)
		info := ConflictInfo{Key: k, Total: len(g.paths), Files: g.paths, Pinned: true}
		if len(info.Files) > maxConflictFiles {
			info.Files = info.Files[:maxConflictFiles]
		}
		identical := true
		for _, path := range g.paths {
			info.Pinned = info.Pinned && pinned[strings.ToLower(path)]
			first := files[g.mods[0]][strings.ToLower(path)]
			for _, id := range g.mods[1:] {
				identical = identical && files[id][strings.ToLower(path)] == first
			}
		}
		// Главный победитель — тот, кому достаётся больше всего спорных файлов.
		top := g.mods[len(g.mods)-1]
		for _, id := range g.mods {
			if g.wins[id] > g.wins[top] {
				top = id
			}
		}
		for i, id := range g.mods {
			m := ConflictMod{
				ID: id, Name: names(id), Files: len(files[id]), Shared: len(g.paths),
				Lost: len(g.paths) - g.wins[id], Winner: id == top, Default: i == len(g.mods)-1,
			}
			m.Covered = m.Files > 0 && m.Files == m.Shared && m.Lost == m.Shared
			info.Mods = append(info.Mods, m)
		}
		a.advise(&info, identical, folders, modRules)
		info.Resolved = info.Pinned || info.Kind == ConflictIdentical
		out = append(out, info)
	}
	return out
}

// advise определяет вид конфликта и составляет совет.
func (a *Manager) advise(info *ConflictInfo, identical bool, folders map[string][]string, modRules []rules.Rule) {
	quote := func(m ConflictMod) string { return i18n.Quote(m.Name) }
	var winner ConflictMod
	for _, m := range info.Mods {
		if m.Winner {
			winner = m
		}
	}
	files := fmt.Sprintf("%d %s", info.Total, plural(info.Total, "общий файл", "общих файла", "общих файлов"))

	if identical {
		info.Kind = ConflictIdentical
		info.Advice = i18n.T("Файлы у модов одинаковые, байт в байт. Конфликт безвреден: чей бы файл ни остался, игра получит одно и то же. Делать ничего не нужно.")
		return
	}

	// Моды ставятся в одну и ту же папку — это варианты одного мода.
	if folder := sharedFolder(info.Mods, folders); folder != "" {
		info.Kind = ConflictDuplicate
		info.Advice = i18n.Sprintf("Эти моды ставятся в одну папку «%s» — похоже, это два варианта одного и того же мода (разные версии или переделка). Вместе они не работают: в игре окажется смесь файлов. Оставьте один, второй выключите.", folder)
		for _, m := range info.Mods {
			if !m.Winner {
				info.Disable = m.ID
			}
		}
		info.Suggested = winner.ID
		return
	}

	// Автор одного из модов сам указал порядок: кто грузится позже, тот и
	// должен перекрывать.
	if later, earlier, ok := ruleOrder(info.Mods, folders, modRules); ok {
		info.Kind = ConflictOrdered
		info.Suggested = later.ID
		info.Advice = i18n.Sprintf("Автор указал, что %s грузится после %s — значит, он рассчитан на то, чтобы перекрывать его файлы. Победителем стоит выбрать %s.", quote(later), quote(earlier), quote(later))
		if later.Winner {
			info.Advice += i18n.T(" Сейчас так и есть: достаточно подтвердить.")
		}
		return
	}

	for _, m := range info.Mods {
		if m.Covered {
			info.Kind = ConflictCovered
			info.Disable = m.ID
			info.Advice = i18n.Sprintf("%s перекрыт целиком: все его файлы (%d) заменяет %s, в игре от него ничего не остаётся. Если нужен %s — выберите его победителем; если нет — его можно выключить, ничего не изменится.",
				quote(m), m.Files, quote(winner), quote(m))
			return
		}
	}

	info.Kind = ConflictOverlap
	var shares []string
	for _, m := range info.Mods {
		shares = append(shares, i18n.Sprintf("%s — %d из %d", quote(m), m.Shared, m.Files))
	}
	info.Advice = i18n.Sprintf("Моды меняют %s (%s). Остальные их файлы не пересекаются, оба мода продолжат работать, но в спорных файлах останется вариант победителя. Обычно победить должен мод, который дополняет или исправляет другой (патч, перевод, надстройка); если не знаете — оставьте по порядку загрузки и проверьте в игре.",
		files, strings.Join(shares, ", "))
}

// sharedFolder возвращает папку, если все участники конфликта — моды одной
// и той же папки: у каждого свои папки ровно те же, что у остальных.
func sharedFolder(mods []ConflictMod, folders map[string][]string) string {
	if len(mods) == 0 || len(folders[mods[0].ID]) == 0 {
		return ""
	}
	first := folders[mods[0].ID]
	for _, m := range mods[1:] {
		other := folders[m.ID]
		if len(other) != len(first) {
			return ""
		}
		for _, f := range first {
			found := false
			for _, o := range other {
				found = found || strings.EqualFold(f, o)
			}
			if !found {
				return ""
			}
		}
	}
	return first[0]
}

// ruleOrder ищет среди правил авторов указание, что один участник конфликта
// грузится после другого.
func ruleOrder(mods []ConflictMod, folders map[string][]string, modRules []rules.Rule) (later, earlier ConflictMod, ok bool) {
	owner := map[string]ConflictMod{}
	for _, m := range mods {
		for _, f := range folders[m.ID] {
			owner[strings.ToLower(f)] = m
		}
	}
	for _, r := range modRules {
		x, okX := owner[strings.ToLower(r.Mod)]
		y, okY := owner[strings.ToLower(r.Other)]
		if !okX || !okY || x.ID == y.ID {
			continue
		}
		switch r.Kind {
		case rules.After:
			return x, y, true
		case rules.Before:
			return y, x, true
		}
	}
	return ConflictMod{}, ConflictMod{}, false
}

// Conflicts возвращает разбор всех конфликтов файлов текущего набора.
func (a *Manager) Conflicts() ([]ConflictInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, err := a.plan()
	if err != nil {
		return nil, err
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return nil, err
	}
	out := a.conflicts(plan, p)
	if out == nil {
		out = []ConflictInfo{}
	}
	return out, nil
}

// Conflict возвращает разбор одного конфликта.
func (a *Manager) Conflict(key string) (ConflictInfo, error) {
	list, err := a.Conflicts()
	if err != nil {
		return ConflictInfo{}, err
	}
	for _, c := range list {
		if c.Key == key {
			return c, nil
		}
	}
	return ConflictInfo{}, i18n.NewError("этого конфликта больше нет")
}

// conflictIssues — замечания о конфликтах, которые ждут решения. Решённые
// (победитель выбран) и безвредные (файлы одинаковые) внимания не требуют:
// они видны в разборе конфликтов.
func conflictIssues(list []ConflictInfo) []Issue {
	var out []Issue
	for _, c := range list {
		if c.Resolved {
			continue
		}
		modNames := make([]string, len(c.Mods))
		for i, m := range c.Mods {
			modNames[i] = m.Name
		}
		out = append(out, Issue{
			Title:  i18n.Sprintf("%s меняют одни и те же файлы (%d)", strings.Join(modNames, i18n.T(" и ")), c.Total),
			Detail: c.Advice,
			Level:  LevelWarn, Action: i18n.T("Разобрать конфликт"), Command: "ChooseWinner", Arg: c.Key,
		})
	}
	return out
}

// conflictStatus — пункт строки состояния о конфликтах; ok ложно, если их нет.
func conflictStatus(list []ConflictInfo) (StatusItem, bool) {
	if len(list) == 0 {
		return StatusItem{}, false
	}
	open := 0
	for _, c := range list {
		if !c.Resolved {
			open++
		}
	}
	item := StatusItem{Label: i18n.T("Конфликты"), Value: i18n.Sprintf("%d · все разобраны", len(list)), Level: LevelOK, Command: "ShowConflicts"}
	if open > 0 {
		item.Value, item.Level = i18n.Sprintf("%d · ждут решения: %d", len(list), open), LevelWarn
	}
	return item, true
}

// UnpinWinner снимает выбор победителя: спорные файлы снова достаются
// моду, стоящему ниже в порядке загрузки, а конфликт снова ждёт решения.
func (a *Manager) UnpinWinner(key string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	plan, err := a.plan()
	if err != nil {
		return State{}, err
	}
	conflicts := conflictsOf(plan, key)
	if len(conflicts) == 0 {
		return State{}, i18n.NewError("этого конфликта больше нет")
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	for _, c := range conflicts {
		for path := range p.Winners {
			if strings.EqualFold(path, c.Path) {
				delete(p.Winners, path)
			}
		}
	}
	if err := a.profiles.Save(p); err != nil {
		return State{}, err
	}
	return a.state()
}
