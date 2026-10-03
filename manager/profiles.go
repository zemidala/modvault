package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
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
		return fmt.Errorf("«%s» — текущий профиль: сначала переключитесь на другой", name)
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
			return DeployResult{}, errors.New("откатывать некуда: развёртываний было меньше двух")
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
		return DeployResult{Message: fmt.Sprintf("Профиль «%s» вернётся к прошлому развёртыванию: %d модов", prev.Name, len(prev.Entries))}, nil
	}
	prev.Name = a.profileName()
	err = a.profiles.Save(prev)
	a.mu.Unlock()
	if err != nil {
		return DeployResult{}, err
	}
	return a.Deploy()
}
