package manager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/i18n"
)

func (a *Manager) profileName() string {
	if a.settings.Profile != "" {
		return a.settings.Profile
	}
	return mainProfile
}

// Profiles возвращает названия профилей и текущий профиль.
func (a *Manager) Profiles() ([]string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return nil, "", a.openErr
	}
	names, err := a.profiles.List()
	current := a.profileName()
	found := false
	for _, n := range names {
		found = found || n == current
	}
	if !found {
		names = append(names, current)
	}
	return names, current, err
}

// UseProfile делает профиль текущим; если его нет, создаёт пустым.
func (a *Manager) UseProfile(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return a.openErr
	}
	if _, err := a.profiles.Load(name); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := a.profiles.Save(profile.Profile{Name: name}); err != nil {
			return err
		}
	}
	a.settings.Profile = name
	return a.saveSettings()
}

// CopyProfile создаёт профиль to с тем же набором модов, что в from.
func (a *Manager) CopyProfile(from, to string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, _, err := a.loadProfile(); err != nil { // текущий профиль должен быть на диске
		return err
	}
	return a.profiles.Copy(from, to)
}

// RenameProfile переименовывает профиль.
func (a *Manager) RenameProfile(from, to string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, _, err := a.loadProfile(); err != nil {
		return err
	}
	if err := a.profiles.Rename(from, to); err != nil {
		return err
	}
	if a.profileName() == from {
		a.settings.Profile = to
		return a.saveSettings()
	}
	return nil
}

// DeleteProfile удаляет профиль; текущий удалить нельзя.
func (a *Manager) DeleteProfile(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if name == a.profileName() {
		return i18n.Errorf("«%s» — текущий набор: сначала переключитесь на другой", name)
	}
	return a.profiles.Delete(name)
}

// Move переставляет мод на позицию to (с нуля) в порядке загрузки.
func (a *Manager) Move(id string, to int) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, _, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	if err := p.Move(id, to); err != nil {
		return State{}, err
	}
	if err := a.profiles.Save(p); err != nil {
		return State{}, err
	}
	return a.state()
}

// MoveMods переставляет моды ids в порядке загрузки: ставит их перед модом
// target или, с after, сразу за ним. Между собой они остаются в прежнем
// порядке.
func (a *Manager) MoveMods(ids []string, target string, after bool) (SetResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if real, err := a.hasMods(); err != nil || !real {
		return SetResult{}, errors.Join(err, i18n.NewError("это демонстрационные моды"))
	}
	if err := a.bisecting(); err != nil {
		return SetResult{}, err
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return SetResult{}, err
	}
	moving := map[string]bool{}
	for _, id := range ids {
		if p.Index(id) < 0 {
			return SetResult{}, i18n.Errorf("мод %q не найден", id)
		}
		moving[id] = true
	}
	if len(moving) == 0 || moving[target] || p.Index(target) < 0 {
		return SetResult{}, i18n.NewError("мод нужно бросить выше или ниже другого мода")
	}
	var moved, rest []profile.Entry
	for _, e := range p.Entries {
		if moving[e.ModID] {
			moved = append(moved, e)
		} else {
			rest = append(rest, e)
		}
	}
	at := 0
	for i, e := range rest {
		if e.ModID == target {
			at = i
		}
	}
	if after {
		at++
	}
	p.Entries = append(append(append([]profile.Entry(nil), rest[:at]...), moved...), rest[at:]...)
	if err := a.profiles.Save(p); err != nil {
		return SetResult{}, err
	}

	names := a.modNames()
	where := i18n.T("перед")
	if after {
		where = i18n.T("после")
	}
	msg := i18n.Sprintf("«%s» теперь стоит %s «%s»", names(moved[0].ModID), where, names(target))
	if len(moved) > 1 {
		msg = i18n.Sprintf("%d %s теперь стоят %s «%s»", len(moved), plural(len(moved), "мод", "мода", "модов"), where, names(target))
	}
	// Что это меняет, зависит от загрузчика модов.
	if _, ord, err := a.orderInfo(p); err == nil && ord.Auto {
		msg += i18n.T(". Порядок загрузки в игре задаёт загрузчик модов; порядок в списке решает, чей файл побеждает при конфликте")
	} else if a.deployer != nil {
		msg += i18n.T(". В игру новый порядок попадёт по кнопке «Развернуть»")
	}
	st, err := a.state()
	return SetResult{State: st, Message: msg}, err
}

// Профиль на момент двух последних развёртываний: по ним откат возвращает
// игру и профиль к предпоследнему развёртыванию.
const (
	lastDeployed = "deployed-last.json"
	prevDeployed = "deployed-prev.json"
)

func (a *Manager) rememberDeployed() {
	p, _, err := a.loadProfile()
	if err != nil {
		return
	}
	dir := a.gameState(a.settings.GameDir)
	last := filepath.Join(dir, lastDeployed)
	if data, err := os.ReadFile(last); err == nil {
		fsx.WriteFile(filepath.Join(dir, prevDeployed), data)
	}
	if data, err := json.MarshalIndent(p, "", "  "); err == nil {
		fsx.WriteFile(last, data)
	}
}

// Rollback возвращает профиль к тому, каким он был при предпоследнем
// развёртывании, и разворачивает его. Повторный откат возвращает обратно.
func (a *Manager) Rollback(dryRun bool) (DeployResult, error) {
	a.mu.Lock()
	data, err := os.ReadFile(filepath.Join(a.gameState(a.settings.GameDir), prevDeployed))
	if err != nil {
		a.mu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			return DeployResult{}, i18n.NewError("откатывать некуда: развёртываний было меньше двух")
		}
		return DeployResult{}, err
	}
	var prev profile.Profile
	if err := json.Unmarshal(data, &prev); err != nil {
		a.mu.Unlock()
		return DeployResult{}, err
	}
	if dryRun {
		a.mu.Unlock()
		return DeployResult{Message: i18n.Sprintf("Профиль «%s» вернётся к прошлому развёртыванию: %d модов", prev.Name, len(prev.Entries))}, nil
	}
	prev.Name = a.profileName()
	err = a.profiles.Save(prev)
	a.mu.Unlock()
	if err != nil {
		return DeployResult{}, err
	}
	return a.Deploy()
}
