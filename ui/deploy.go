package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/internal/version"
)

const settingsFile = "settings.json"

// settings — настройки программы, которые переживают перезапуск.
type settings struct {
	// GameDir — папка, куда развёртываются моды. До этапа 4 её выбирают вручную.
	GameDir string `json:"gameDir"`
}

func loadSettings(home string) (settings, error) {
	var s settings
	data, err := os.ReadFile(filepath.Join(home, settingsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("%s: %w", settingsFile, err)
	}
	return s, nil
}

func (a *App) saveSettings() error {
	data, err := json.MarshalIndent(a.settings, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(filepath.Join(a.home, settingsFile), data)
}

// gameState — папка состояния развёртывания для папки игры. У каждой папки
// своя: смена папки не путает учёт, а прежняя установка остаётся как была.
func (a *App) gameState(game string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(game))))
	return filepath.Join(a.home, "deploy", hex.EncodeToString(sum[:6]))
}

// openDeployer открывает развёртывание в выбранную папку; заодно откатывает
// развёртывание, оборвавшееся в прошлый раз.
func (a *App) openDeployer() {
	a.deployer, a.recovery, a.deployErr = deploy.Open(a.settings.GameDir, a.gameState(a.settings.GameDir))
	if a.deployErr != nil && errors.Is(a.deployErr, fs.ErrNotExist) {
		a.deployErr = fmt.Errorf("папка %s не найдена", a.settings.GameDir)
	}
}

// ChooseGame спрашивает у пользователя папку игры.
func (a *App) ChooseGame() (State, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Папка игры",
		DefaultDirectory: a.settings.GameDir,
	})
	if err != nil {
		return State{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if dir == "" {
		return a.state()
	}
	return a.setGame(dir)
}

func (a *App) setGame(dir string) (State, error) {
	if a.openErr != nil {
		return State{}, a.openErr
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return State{}, err
	}
	a.settings.GameDir = abs
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	a.openDeployer()
	return a.state()
}

// DeployResult — состояние окна после развёртывания и сообщение для пользователя.
type DeployResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
}

// Deploy приводит игру в соответствие с профилем.
func (a *App) Deploy() (DeployResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	real, err := a.hasMods()
	if err != nil {
		return DeployResult{}, err
	}
	if !real {
		return DeployResult{}, errors.New("сейчас показаны демонстрационные данные: добавьте мод, чтобы было что развернуть")
	}
	plan, err := a.plan()
	if err != nil {
		return DeployResult{}, err
	}

	msg := "Игра уже совпадает с профилем"
	if !plan.Empty() {
		res, err := a.deployer.Apply(plan)
		if err != nil {
			return DeployResult{}, err
		}
		a.recovery = deploy.NothingToRecover
		msg = fmt.Sprintf("Развёрнуто: %d %s", res.Changes, plural(res.Changes, "изменение", "изменения", "изменений"))
		if res.Displaced != "" {
			msg += ". Файлы, изменённые вне программы, сохранены в " + res.Displaced
		}
	}
	st, err := a.state()
	return DeployResult{State: st, Message: msg}, err
}

// PlanFiles перечисляет, что развёртывание сделает с каждым файлом.
func (a *App) PlanFiles() ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	plan, err := a.plan()
	if err != nil {
		return nil, err
	}
	names := a.modNames()
	lines := make([]string, 0, len(plan.Changes))
	for _, c := range plan.Changes {
		switch c.Kind {
		case deploy.Add:
			lines = append(lines, fmt.Sprintf("+ %s — %s", c.Path, names(c.ModID)))
		case deploy.Replace:
			owner := names(c.ModID)
			if c.OldID != c.ModID {
				owner = names(c.OldID) + " → " + owner
			}
			lines = append(lines, fmt.Sprintf("~ %s — %s", c.Path, owner))
		case deploy.Remove:
			lines = append(lines, fmt.Sprintf("− %s — %s", c.Path, names(c.ModID)))
		}
	}
	return lines, nil
}

// plan рассчитывает развёртывание для основного профиля.
func (a *App) plan() (*deploy.Plan, error) {
	if a.deployer == nil && a.deployErr == nil {
		return nil, errors.New("сначала выберите папку игры")
	}
	if a.deployErr != nil {
		return nil, a.deployErr
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return nil, err
	}
	sources, err := a.sources(p)
	if err != nil {
		return nil, err
	}
	return a.deployer.Plan(sources, p.Winners)
}

// sources собирает включённые моды профиля в порядке загрузки. Файлы ложатся
// в игру по тем же путям, что в архиве; раскладку под Darktide даст этап 4.
func (a *App) sources(p profile.Profile) ([]deploy.Source, error) {
	var out []deploy.Source
	for _, e := range p.Entries {
		if !e.Enabled {
			continue
		}
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return nil, err
		}
		root := a.store.FilesDir(e.ModID, e.VersionID)
		s := deploy.Source{ModID: e.ModID, VersionID: e.VersionID, Files: make([]deploy.File, len(v.Files))}
		for i, f := range v.Files {
			s.Files[i] = deploy.File{Path: f.Path, Src: filepath.Join(root, filepath.FromSlash(f.Path)), Hash: f.Hash, Size: f.Size}
		}
		out = append(out, s)
	}
	return out, nil
}

// modNames возвращает функцию «идентификатор → название мода».
func (a *App) modNames() func(string) string {
	names := map[string]string{}
	if mods, _, err := a.store.List(); err == nil {
		for _, m := range mods {
			names[m.ID] = m.Latest().Name
		}
	}
	return func(id string) string {
		if name, ok := names[id]; ok {
			return name
		}
		return id
	}
}

// realState собирает состояние окна из хранилища, профиля и развёртывания.
func (a *App) realState() (State, error) {
	p, mods, err := a.loadProfile()
	if err != nil {
		return State{}, err
	}
	_, problems, err := a.store.List()
	if err != nil {
		return State{}, err
	}
	names := a.modNames()

	s := State{
		Version: version.String(),
		Home:    a.home,
		Profile: p.Name,
		Issues:  []Issue{},
		Mods:    make([]Mod, 0, len(p.Entries)),
		Plan:    []string{},
	}

	var plan *deploy.Plan
	var planErr error
	if a.deployer != nil && a.deployErr == nil {
		plan, planErr = a.plan()
	}

	pending := map[string]bool{}
	if plan != nil {
		for _, c := range plan.Changes {
			pending[c.ModID] = true
			if c.OldID != "" {
				pending[c.OldID] = true
			}
		}
	}

	byID := make(map[string]store.Mod, len(mods))
	for _, m := range mods {
		byID[m.ID] = m
	}
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return State{}, err
		}
		row := Mod{
			ID: e.ModID, Name: v.Name, Version: v.Version, Source: v.Source,
			Files: len(v.Files), Versions: len(byID[e.ModID].Versions), Enabled: e.Enabled,
		}
		if row.Version == "" {
			row.Version = "—"
		}
		switch {
		case e.Enabled && plan == nil:
			row.State, row.Level = "В хранилище", LevelOK
		case e.Enabled && pending[e.ModID]:
			row.State, row.Level = "Ждёт развёртывания", LevelWarn
		case e.Enabled:
			row.State, row.Level = "Развёрнут", LevelOK
		case pending[e.ModID]:
			row.State, row.Level = "Выключен, ждёт развёртывания", LevelOff
		default:
			row.State, row.Level = "Выключен", LevelOff
		}
		s.Mods = append(s.Mods, row)
	}

	// Замечания: сначала то, что мешает работать, потом то, что стоит знать.
	if a.recovery == deploy.RolledBack {
		s.Issues = append(s.Issues, Issue{
			Title:  "Прошлое развёртывание было прервано",
			Detail: "Игра возвращена в состояние до него; его можно повторить",
			Level:  LevelWarn,
		})
	}
	switch {
	case a.settings.GameDir == "":
		s.Issues = append(s.Issues, Issue{
			Title:  "Папка игры не выбрана",
			Detail: "Находить Darktide программа научится на этапе 4. Пока укажите папку вручную; для пробы подойдёт пустая",
			Level:  LevelWarn, Action: "Выбрать папку", Command: "ChooseGame",
		})
	case a.deployErr != nil:
		s.Issues = append(s.Issues, Issue{
			Title: "Развёртывание недоступно", Detail: a.deployErr.Error(),
			Level: LevelError, Action: "Выбрать папку", Command: "ChooseGame",
		})
	case planErr != nil:
		s.Issues = append(s.Issues, Issue{Title: "Не удалось рассчитать развёртывание", Detail: planErr.Error(), Level: LevelError})
	}
	for _, problem := range problems {
		s.Issues = append(s.Issues, Issue{Title: "Запись в хранилище повреждена", Detail: problem.Error(), Level: LevelError})
	}
	if plan != nil {
		s.Issues = append(s.Issues, driftIssues(plan)...)
		s.Issues = append(s.Issues, conflictIssues(plan, names)...)
		s.Plan, s.PlanTitle = planLines(plan, names)
	}

	s.Status = a.status(s, len(mods), plan)
	return s, nil
}

func driftIssues(plan *deploy.Plan) []Issue {
	changed, missing := 0, 0
	for _, d := range plan.Drift {
		if d.Missing {
			missing++
		} else {
			changed++
		}
	}
	var out []Issue
	if changed > 0 {
		out = append(out, Issue{
			Title:  fmt.Sprintf("%d %s вне программы", changed, plural(changed, "файл мода изменён", "файла модов изменены", "файлов модов изменены")),
			Detail: "При развёртывании они будут сохранены в отдельную папку, а на их место лягут файлы модов",
			Level:  LevelError,
		})
	}
	if missing > 0 {
		out = append(out, Issue{
			Title:  fmt.Sprintf("%d %s из игры", missing, plural(missing, "файл мода пропал", "файла модов пропали", "файлов модов пропали")),
			Detail: "Например, после проверки файлов в Steam. При развёртывании они лягут заново",
			Level:  LevelWarn,
		})
	}
	return out
}

// conflictIssues сводит конфликты по наборам модов: одна строка на набор,
// а не на каждый файл.
func conflictIssues(plan *deploy.Plan, names func(string) string) []Issue {
	type group struct {
		mods   []string
		winner string
		files  int
	}
	var order []string
	groups := map[string]*group{}
	for _, c := range plan.Conflicts {
		k := strings.Join(c.Mods, "\x00") + "\x01" + c.Winner
		g, ok := groups[k]
		if !ok {
			g = &group{mods: c.Mods, winner: c.Winner}
			groups[k] = g
			order = append(order, k)
		}
		g.files++
	}
	out := make([]Issue, 0, len(order))
	for _, k := range order {
		g := groups[k]
		modNames := make([]string, len(g.mods))
		for i, id := range g.mods {
			modNames[i] = names(id)
		}
		why := "он ниже в порядке загрузки"
		if g.winner != g.mods[len(g.mods)-1] {
			why = "он закреплён победителем"
		}
		out = append(out, Issue{
			Title:  fmt.Sprintf("%s меняют одни и те же файлы (%d)", strings.Join(modNames, " и "), g.files),
			Detail: fmt.Sprintf("Конфликт файлов · побеждает %s: %s", names(g.winner), why),
			Level:  LevelWarn, Action: "Выбрать победителя", Stage: 8,
		})
	}
	return out
}

// planLines описывает план по модам: «Scoreboard — положить 3 файла».
func planLines(plan *deploy.Plan, names func(string) string) ([]string, string) {
	if plan.Empty() {
		return []string{}, ""
	}
	type counts struct{ add, replace, remove int }
	var order []string
	byMod := map[string]*counts{}
	get := func(id string) *counts {
		c, ok := byMod[id]
		if !ok {
			c = &counts{}
			byMod[id] = c
			order = append(order, id)
		}
		return c
	}
	for _, c := range plan.Changes {
		switch c.Kind {
		case deploy.Add:
			get(c.ModID).add++
		case deploy.Replace:
			get(c.ModID).replace++
		case deploy.Remove:
			get(c.ModID).remove++
		}
	}
	files := func(n int) string {
		return fmt.Sprintf("%d %s", n, plural(n, "файл", "файла", "файлов"))
	}
	lines := make([]string, 0, len(order))
	for _, id := range order {
		c := byMod[id]
		var parts []string
		if c.add > 0 {
			parts = append(parts, "положить "+files(c.add))
		}
		if c.replace > 0 {
			parts = append(parts, "заменить "+files(c.replace))
		}
		if c.remove > 0 {
			parts = append(parts, "убрать "+files(c.remove))
		}
		lines = append(lines, names(id)+" — "+strings.Join(parts, ", "))
	}
	n := len(plan.Changes)
	return lines, fmt.Sprintf("План развёртывания: %d %s", n, plural(n, "изменение", "изменения", "изменений"))
}

func (a *App) status(s State, mods int, plan *deploy.Plan) []StatusItem {
	game := StatusItem{Label: "Игра", Value: "папка не выбрана", Level: LevelWarn, Command: "ChooseGame"}
	if a.settings.GameDir != "" {
		game.Value, game.Level = a.settings.GameDir, LevelOK
		if a.deployErr != nil {
			game.Level = LevelError
		}
	}

	files := StatusItem{Label: "Файлы в игре", Value: "—", Level: LevelOff}
	if plan != nil {
		files.Value, files.Level = "совпадают с профилем", LevelOK
		if !plan.Empty() {
			files.Value, files.Level = "ждут развёртывания", LevelWarn
		}
		for _, d := range plan.Drift {
			if !d.Missing {
				files.Value, files.Level = "изменены вне программы", LevelError
				break
			}
		}
	}

	checks := StatusItem{Label: "Проверки", Value: "замечаний нет", Level: LevelOK}
	if n := len(s.Issues); n > 0 {
		checks.Value = fmt.Sprintf("%d %s", n, plural(n, "замечание", "замечания", "замечаний"))
		checks.Level = LevelWarn
		for _, i := range s.Issues {
			if i.Level == LevelError {
				checks.Level = LevelError
			}
		}
	}
	return []StatusItem{
		game,
		{Label: "Хранилище", Value: fmt.Sprintf("%d %s", mods, plural(mods, "мод", "мода", "модов")), Level: LevelOK},
		files,
		checks,
	}
}
