package manager

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/rules"
)

// Наборы — то, что в ядре называется профилями: какие моды включены.
// Версия мода у всех наборов одна, наборы отличаются только составом.
// В каждом наборе сами включаются моды, без которых остальные не
// заработают: загрузчик, фреймворк и то, чего требуют выбранные моды.

// SetInfo — набор в списке наборов.
type SetInfo struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
	Enabled int    `json:"enabled"` // включённых модов
}

// SetResult — состояние окна после действия с набором и сообщение.
type SetResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
}

// Sets возвращает наборы по алфавиту.
func (a *Manager) Sets() ([]SetInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return nil, a.openErr
	}
	cur, _, err := a.loadProfile()
	if err != nil {
		return nil, err
	}
	names, err := a.profiles.List()
	if err != nil {
		return nil, err
	}
	enabled := func(p profile.Profile) int {
		n := 0
		for _, e := range p.Entries {
			if e.Enabled {
				n++
			}
		}
		return n
	}
	var out []SetInfo
	found := false
	for _, name := range names {
		p := cur
		if name != cur.Name {
			if p, err = a.profiles.Load(name); err != nil {
				return nil, err
			}
		}
		found = found || name == cur.Name
		out = append(out, SetInfo{Name: name, Current: name == cur.Name, Enabled: enabled(p)})
	}
	if !found { // текущий набор ещё ни разу не сохранялся
		out = append(out, SetInfo{Name: cur.Name, Current: true, Enabled: enabled(cur)})
	}
	return out, nil
}

// setsByMod возвращает для каждого мода наборы, в которых он включён.
// cur — текущий набор: он мог ещё ни разу не сохраняться.
func (a *Manager) setsByMod(cur profile.Profile) map[string][]string {
	out := map[string][]string{}
	names, err := a.profiles.List()
	if err != nil {
		names = nil
	}
	seen := false
	for _, name := range names {
		seen = seen || name == cur.Name
	}
	if !seen {
		names = append(names, cur.Name)
		sort.Strings(names)
	}
	for _, name := range names {
		p := cur
		if name != cur.Name {
			if p, err = a.profiles.Load(name); err != nil {
				continue
			}
		}
		for _, e := range p.Entries {
			if e.Enabled {
				out[e.ModID] = append(out[e.ModID], name)
			}
		}
	}
	return out
}

// withRequired включает в профиле моды ids и всё, без чего они не
// заработают: загрузчик и фреймворк, а также моды, которых требуют уже
// включённые. Возвращает названия модов, включённых сверх просьбы.
func (a *Manager) withRequired(p *profile.Profile, ids []string) ([]string, error) {
	asked := map[string]bool{}
	for _, id := range ids {
		if err := p.SetEnabled(id, true); err != nil {
			return nil, err
		}
		asked[id] = true
	}
	var extra []string
	enable := func(id, name string) {
		if i := p.Index(id); i >= 0 && !p.Entries[i].Enabled {
			p.Entries[i].Enabled = true
			if !asked[id] {
				extra = append(extra, name)
			}
		}
	}
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return nil, err
		}
		if l, err := a.layout(v); err == nil && l.Role != game.RoleNone {
			enable(e.ModID, v.Name)
		}
	}
	// Зависимости тянут свои зависимости: повторяем, пока есть что включать.
	for range p.Entries {
		mods, ord, err := a.orderInfo(*p)
		if err != nil {
			return nil, err
		}
		owner := map[string]ordered{}
		for _, m := range mods {
			for _, f := range m.folders {
				owner[strings.ToLower(f)] = m
			}
		}
		before := len(extra)
		for _, r := range rules.Missing(loadedFolders(mods), ord.Rules) {
			if m, ok := owner[strings.ToLower(r.Other)]; ok {
				enable(m.entry.ModID, m.name)
			}
		}
		if len(extra) == before {
			break
		}
	}
	return extra, nil
}

func requiredNote(extra []string) string {
	if len(extra) == 0 {
		return ""
	}
	return fmt.Sprintf(" Сами включены обязательные: %s.", strings.Join(extra, ", "))
}

// CreateSet создаёт набор. С ids в нём включены только эти моды и то, без
// чего они не заработают; без ids набор повторяет текущий. Текущий набор
// при этом не меняется и остаётся выбранным.
func (a *Manager) CreateSet(name string, ids []string) (SetResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	name = strings.TrimSpace(name)
	if real, err := a.hasMods(); err != nil || !real {
		return SetResult{}, errors.Join(err, errors.New("сначала выберите папку игры: наборы составляются из настоящих модов"))
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return SetResult{}, err
	}
	if _, err := a.profiles.Load(name); err == nil {
		return SetResult{}, fmt.Errorf("набор «%s» уже есть", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return SetResult{}, err
	}
	// Текущий набор должен лежать на диске: иначе после переключения его не найти.
	if err := a.profiles.Save(p); err != nil {
		return SetResult{}, err
	}

	next := profile.Profile{Name: name, Entries: append([]profile.Entry(nil), p.Entries...), Winners: p.Winners}
	msg := fmt.Sprintf("Набор «%s» создан как копия набора «%s».", name, p.Name)
	if len(ids) > 0 {
		for i := range next.Entries {
			next.Entries[i].Enabled = false
		}
		extra, err := a.withRequired(&next, ids)
		if err != nil {
			return SetResult{}, err
		}
		msg = fmt.Sprintf("Набор «%s» создан: %d %s.%s", name, len(ids), plural(len(ids), "мод", "мода", "модов"), requiredNote(extra))
	}
	if err := a.profiles.Save(next); err != nil {
		return SetResult{}, err
	}
	st, err := a.state()
	return SetResult{State: st, Message: msg + " Переключиться на него — в меню «Набор»"}, err
}

// AddToSet включает моды ids в наборе name; остальное в нём не меняется.
func (a *Manager) AddToSet(name string, ids []string) (SetResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, _, err := a.loadProfile()
	if err != nil {
		return SetResult{}, err
	}
	p := cur
	if name != cur.Name {
		if p, err = a.profiles.Load(name); err != nil {
			return SetResult{}, fmt.Errorf("набор «%s» не найден", name)
		}
		// Мод, добавленный в хранилище после создания набора, в нём ещё не числится.
		for _, e := range cur.Entries {
			if p.Index(e.ModID) < 0 {
				p.Entries = append(p.Entries, profile.Entry{ModID: e.ModID, VersionID: e.VersionID})
			}
		}
	}
	extra, err := a.withRequired(&p, ids)
	if err != nil {
		return SetResult{}, err
	}
	if err := a.profiles.Save(p); err != nil {
		return SetResult{}, err
	}
	st, err := a.state()
	msg := fmt.Sprintf("В набор «%s» %s %d %s.%s", name, plural(len(ids), "добавлен", "добавлены", "добавлены"), len(ids), plural(len(ids), "мод", "мода", "модов"), requiredNote(extra))
	return SetResult{State: st, Message: strings.TrimSpace(msg)}, err
}

// disableIn выключает моды ids в профиле и возвращает, сколько их было включено.
func disableIn(p *profile.Profile, ids []string) int {
	n := 0
	for _, id := range ids {
		if i := p.Index(id); i >= 0 && p.Entries[i].Enabled {
			p.Entries[i].Enabled = false
			n++
		}
	}
	return n
}

// RemoveFromSet выключает моды ids в наборе name; остальное в нём не меняется.
func (a *Manager) RemoveFromSet(name string, ids []string) (SetResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, _, err := a.loadProfile()
	if err != nil {
		return SetResult{}, err
	}
	p := cur
	if name != cur.Name {
		if p, err = a.profiles.Load(name); err != nil {
			return SetResult{}, fmt.Errorf("набор «%s» не найден", name)
		}
	}
	removed := disableIn(&p, ids)
	if err := a.profiles.Save(p); err != nil {
		return SetResult{}, err
	}
	st, err := a.state()
	msg := fmt.Sprintf("Из набора «%s» %s %d %s", name, plural(removed, "убран", "убраны", "убраны"), removed, plural(removed, "мод", "мода", "модов"))
	if removed == 0 {
		msg = fmt.Sprintf("В наборе «%s» эти моды и так выключены", name)
	} else if name == cur.Name && a.deployer != nil {
		msg += ". Из игры они уйдут по кнопке «Развернуть»"
	}
	return SetResult{State: st, Message: msg}, err
}

// MoveToSet переносит моды ids из текущего набора в набор name: там они
// включаются вместе с тем, без чего не заработают, а в текущем выключаются.
func (a *Manager) MoveToSet(name string, ids []string) (SetResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, _, err := a.loadProfile()
	if err != nil {
		return SetResult{}, err
	}
	if name == cur.Name {
		return SetResult{}, fmt.Errorf("моды уже в наборе «%s»", name)
	}
	to, err := a.profiles.Load(name)
	if err != nil {
		return SetResult{}, fmt.Errorf("набор «%s» не найден", name)
	}
	for _, e := range cur.Entries {
		if to.Index(e.ModID) < 0 {
			to.Entries = append(to.Entries, profile.Entry{ModID: e.ModID, VersionID: e.VersionID})
		}
	}
	extra, err := a.withRequired(&to, ids)
	if err != nil {
		return SetResult{}, err
	}
	// Сначала новый набор, потом старый: при сбое мод окажется в обоих, а не пропадёт.
	if err := a.profiles.Save(to); err != nil {
		return SetResult{}, err
	}
	disableIn(&cur, ids)
	if err := a.profiles.Save(cur); err != nil {
		return SetResult{}, err
	}
	st, err := a.state()
	msg := fmt.Sprintf("В набор «%s» %s %d %s из набора «%s».%s", name, plural(len(ids), "перенесён", "перенесены", "перенесены"),
		len(ids), plural(len(ids), "мод", "мода", "модов"), cur.Name, requiredNote(extra))
	if a.deployer != nil {
		msg += " Из игры они уйдут по кнопке «Развернуть»"
	}
	return SetResult{State: st, Message: msg}, err
}

// SetEnabledMany включает или выключает несколько модов текущего набора разом.
func (a *Manager) SetEnabledMany(ids []string, enabled bool) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if real, err := a.hasMods(); err != nil || !real {
		return State{}, errors.Join(err, errors.New("это демонстрационные моды"))
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	for _, id := range ids {
		if err := p.SetEnabled(id, enabled); err != nil {
			return State{}, err
		}
	}
	if err := a.profiles.Save(p); err != nil {
		return State{}, err
	}
	return a.state()
}

// SwitchSet делает набор текущим и сразу приводит к нему игру.
func (a *Manager) SwitchSet(name string) (SetResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return SetResult{}, a.openErr
	}
	if _, err := a.profiles.Load(name); err != nil {
		return SetResult{}, fmt.Errorf("набор «%s» не найден", name)
	}
	a.settings.Profile = name
	if err := a.saveSettings(); err != nil {
		return SetResult{}, err
	}

	msg := fmt.Sprintf("Выбран набор «%s»", name)
	switch managers := a.managers(); {
	case a.deployer == nil || a.deployErr != nil:
		msg += ". В игру он не попал: папка игры не выбрана или недоступна"
	case len(managers) > 0:
		msg += fmt.Sprintf(". В игру он не попал: игрой управляет %s", strings.Join(managers, " и "))
	default:
		plan, err := a.plan()
		if err == nil && !plan.Empty() {
			if _, err = a.deployer.Apply(plan); err == nil {
				a.recovery = deploy.NothingToRecover
				a.rememberDeployed()
				msg = fmt.Sprintf("Набор «%s» в игре: %d %s", name, len(plan.Changes), plural(len(plan.Changes), "изменение", "изменения", "изменений"))
			}
		} else if err == nil {
			msg += ": игра уже совпадает с ним"
		}
		if err != nil {
			msg += ". Привести к нему игру не удалось: " + err.Error() + ". Нажмите «Развернуть», когда причина устранена"
		}
	}
	st, err := a.state()
	return SetResult{State: st, Message: msg}, err
}

// RenameSet переименовывает набор.
func (a *Manager) RenameSet(from, to string) (State, error) {
	to = strings.TrimSpace(to)
	if err := a.RenameProfile(from, to); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return State{}, fmt.Errorf("набор «%s» уже есть", to)
		}
		return State{}, err
	}
	return a.State()
}

// DeleteSet удаляет набор; текущий удалить нельзя. Моды остаются в хранилище.
func (a *Manager) DeleteSet(name string) (State, error) {
	if err := a.DeleteProfile(name); err != nil {
		return State{}, err
	}
	return a.State()
}

// shareVersion ставит версию мода во всех остальных наборах: версия у
// мода одна, наборы отличаются только тем, какие моды включены.
func (a *Manager) shareVersion(modID, versionID string) {
	names, err := a.profiles.List()
	if err != nil {
		return
	}
	for _, name := range names {
		if name == a.profileName() {
			continue
		}
		p, err := a.profiles.Load(name)
		if err != nil {
			continue
		}
		if i := p.Index(modID); i >= 0 && p.Entries[i].VersionID != versionID {
			p.Entries[i].VersionID = versionID
			a.profiles.Save(p)
		}
	}
}
