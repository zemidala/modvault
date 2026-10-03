package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/nexus"
)

// Скрытые замечания. Замечание, с которым пользователь согласен жить
// (например, ошибки безобидного мода), можно убрать из «Требуют внимания».

// issueKey — ключ замечания, не зависящий от чисел в его тексте: команда с
// её целью, а где команды нет — заголовок.
func issueKey(i Issue) string {
	if i.Command != "" && i.Arg != "" {
		return i.Command + "|" + i.Arg
	}
	return i.Title
}

// splitHidden делит замечания на видимые и скрытые пользователем.
func (a *Manager) splitHidden(issues []Issue) (shown []Issue, hidden int) {
	skip := map[string]bool{}
	for _, k := range a.settings.HiddenIssues {
		skip[k] = true
	}
	shown = make([]Issue, 0, len(issues))
	for _, i := range issues {
		i.Key = issueKey(i)
		if skip[i.Key] {
			hidden++
			continue
		}
		shown = append(shown, i)
	}
	return shown, hidden
}

// HideIssue убирает замечание из «Требуют внимания», пока его не вернут.
func (a *Manager) HideIssue(key string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return State{}, a.openErr
	}
	for _, k := range a.settings.HiddenIssues {
		if k == key {
			return a.state()
		}
	}
	a.settings.HiddenIssues = append(a.settings.HiddenIssues, key)
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}

// ShowHiddenIssues возвращает все скрытые замечания.
func (a *Manager) ShowHiddenIssues() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return State{}, a.openErr
	}
	a.settings.HiddenIssues = nil
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}

// Версии мода в хранилище: после обновления остаётся прежняя версия, и к
// ней можно вернуться.

// VersionInfo — версия мода в хранилище.
type VersionInfo struct {
	ID      string    `json:"id"`
	Version string    `json:"version"`
	Added   time.Time `json:"added"`
	Current bool      `json:"current"` // выбрана в наборах
}

// ModVersions возвращает версии мода, от новых к старым.
func (a *Manager) ModVersions(id string) ([]VersionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, mods, err := a.loadProfile()
	if err != nil {
		return nil, err
	}
	i := p.Index(id)
	if i < 0 {
		return nil, fmt.Errorf("мод %q не найден", id)
	}
	var out []VersionInfo
	for _, m := range mods {
		if m.ID != id {
			continue
		}
		for j := len(m.Versions) - 1; j >= 0; j-- {
			v := m.Versions[j]
			label := v.Version
			if label == "" {
				label = v.ID
			}
			out = append(out, VersionInfo{ID: v.ID, Version: label, Added: v.Added, Current: v.ID == p.Entries[i].VersionID})
		}
	}
	return out, nil
}

// UseVersion выбирает версию мода — во всех наборах, потому что версия у
// мода одна. В игру она попадает так же, как обновление: сама, если мод
// включён и других неразвёрнутых изменений нет.
func (a *Manager) UseVersion(id, versionID string) (SetResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, err := a.store.Get(id, versionID)
	if err != nil {
		return SetResult{}, fmt.Errorf("версии %q у мода нет", versionID)
	}
	synced := a.inSync()
	p, _, err := a.loadProfile()
	if err != nil {
		return SetResult{}, err
	}
	if err := p.SetVersion(id, versionID); err != nil {
		return SetResult{}, err
	}
	if err := a.profiles.Save(p); err != nil {
		return SetResult{}, err
	}
	a.shareVersion(id, versionID)
	msg := strings.Replace(a.deployUpdate(v, synced), "Обновлён: ", "Выбрана версия: ", 1)
	msg = strings.Replace(msg, " до ", " ", 1)
	a.note(EventInstall, msg)
	st, err := a.state()
	return SetResult{State: st, Message: msg}, err
}

// Набор как файл: список включённых модов набора, по которому тот же набор
// можно собрать на другом компьютере.

// setFile — содержимое файла набора.
type setFile struct {
	Format int          `json:"modvault_set"` // версия формата
	Name   string       `json:"name"`
	Game   string       `json:"game"` // имя игры в адресах Nexus
	Mods   []setFileMod `json:"mods"`
}

type setFileMod struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	NexusID int    `json:"nexusId,omitempty"`
	FileID  int    `json:"fileId,omitempty"`
}

// ExportSet записывает текущий набор в файл: его включённые моды по порядку.
func (a *Manager) ExportSet(path string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if real, err := a.hasMods(); err != nil || !real {
		return "", errors.Join(err, errors.New("сначала выберите папку игры"))
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return "", err
	}
	out := setFile{Format: 1, Name: p.Name, Game: a.game.NexusDomain(), Mods: []setFileMod{}}
	local := 0
	for _, e := range p.Entries {
		if !e.Enabled {
			continue
		}
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return "", err
		}
		out.Mods = append(out.Mods, setFileMod{Name: v.Name, Version: v.Version, NexusID: v.NexusID, FileID: v.NexusFileID})
		if v.NexusID == 0 {
			local++
		}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	if err := fsx.WriteFile(path, data); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Набор «%s» сохранён в файл: %d %s", p.Name, len(out.Mods), plural(len(out.Mods), "мод", "мода", "модов"))
	if local > 0 {
		msg += fmt.Sprintf(". У %d из них нет номера на Nexus — получателю придётся искать их самому", local)
	}
	a.note(EventSet, msg)
	return msg, nil
}

// MissingMod — мод из файла набора, которого нет в хранилище.
type MissingMod struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"` // страница на Nexus; пусто — номера нет
}

// ImportResult — итог загрузки набора из файла.
type ImportResult struct {
	State   State        `json:"state"`
	Message string       `json:"message"`
	Set     string       `json:"set"` // название созданного набора
	Missing []MissingMod `json:"missing"`
}

// ImportSet создаёт набор по файлу: включает в нём моды, которые есть в
// хранилище, и перечисляет те, которых не хватает. Текущий набор не меняется.
func (a *Manager) ImportSet(path string) (ImportResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if real, err := a.hasMods(); err != nil || !real {
		return ImportResult{}, errors.Join(err, errors.New("сначала выберите папку игры"))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ImportResult{}, err
	}
	var in setFile
	if err := json.Unmarshal(data, &in); err != nil || in.Format == 0 {
		return ImportResult{}, fmt.Errorf("%s — не файл набора Modvault", filepath.Base(path))
	}
	if domain := a.game.NexusDomain(); in.Game != "" && in.Game != domain {
		return ImportResult{}, fmt.Errorf("набор собран для другой игры (%s)", in.Game)
	}
	p, mods, err := a.loadProfile()
	if err != nil {
		return ImportResult{}, err
	}
	if err := a.profiles.Save(p); err != nil {
		return ImportResult{}, err
	}

	// Мод из файла ищем в хранилище по номеру на Nexus, а без него — по названию.
	byNexus, byName := map[int]string{}, map[string]string{}
	for _, m := range mods {
		v := m.Latest()
		if v.NexusID != 0 {
			byNexus[v.NexusID] = m.ID
		}
		byName[store.Slug(v.Name)] = m.ID
	}
	var have []string
	res := ImportResult{Missing: []MissingMod{}}
	for _, m := range in.Mods {
		id, ok := byNexus[m.NexusID]
		if !ok || m.NexusID == 0 {
			id, ok = byName[store.Slug(m.Name)]
		}
		if ok {
			have = append(have, id)
			continue
		}
		miss := MissingMod{Name: m.Name, Version: m.Version}
		if m.NexusID != 0 && in.Game != "" {
			miss.URL = nexus.ModPage(in.Game, m.NexusID)
			if m.FileID != 0 {
				miss.URL = nexus.DownloadPage(in.Game, m.NexusID, m.FileID)
			}
		}
		res.Missing = append(res.Missing, miss)
	}

	// Название не должно совпасть с существующим набором.
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	taken, err := a.profiles.List()
	if err != nil {
		return ImportResult{}, err
	}
	sort.Strings(taken)
	base := name
	for n := 2; contains(taken, name) || name == p.Name; n++ {
		name = fmt.Sprintf("%s (%d)", base, n)
	}

	next := profile.Profile{Name: name, Entries: append([]profile.Entry(nil), p.Entries...), Winners: p.Winners}
	for i := range next.Entries {
		next.Entries[i].Enabled = false
	}
	if _, err := a.withRequired(&next, have); err != nil {
		return ImportResult{}, err
	}
	if err := a.profiles.Save(next); err != nil {
		return ImportResult{}, err
	}
	res.Set = name
	res.Message = fmt.Sprintf("Набор «%s» создан из файла: %d из %d %s уже есть и включены в нём", name, len(have), len(in.Mods), plural(len(in.Mods), "мода", "модов", "модов"))
	if n := len(res.Missing); n > 0 {
		res.Message += fmt.Sprintf(". Не хватает %d: после установки включите их в этом наборе", n)
	}
	a.note(EventSet, res.Message)
	res.State, err = a.state()
	return res, err
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Folders возвращает папки, которые окно умеет открыть в Проводнике:
// название → путь. Несуществующие папки не возвращаются.
func (a *Manager) Folders() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]string{}
	add := func(name, path string) {
		if path == "" {
			return
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			out[name] = path
		}
	}
	add("game", a.settings.GameDir)
	add("store", a.home)
	if rep, ok := a.game.LastRun(); ok {
		add("logs", filepath.Dir(rep.Log))
	}
	return out
}
