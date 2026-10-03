package manager

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

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/profile"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/game"
	"github.com/zemidala/modvault/internal/version"
)

const settingsFile = "settings.json"

// settings — настройки программы, которые переживают перезапуск.
type settings struct {
	// GameDir — папка игры, куда развёртываются моды.
	GameDir string `json:"gameDir"`
	// GameStore — откуда игра: «Steam», «Xbox», «вручную».
	GameStore string `json:"gameStore,omitempty"`
	// IgnoredManagers — другие менеджеры модов, которые пользователь
	// разрешил не учитывать.
	IgnoredManagers []string `json:"ignoredManagers,omitempty"`
	// Profile — текущий профиль; пусто — «Основной».
	Profile string `json:"profile,omitempty"`
	// NexusUser — чей ключ Nexus сохранён; сам ключ лежит в учётных данных Windows.
	NexusUser    string `json:"nexusUser,omitempty"`
	NexusPremium bool   `json:"nexusPremium,omitempty"`
	// NxmCommand — команда, которой Modvault назначил себя открывать ссылки
	// nxm://; NxmPrevious — кто открывал их до этого.
	NxmCommand  string `json:"nxmCommand,omitempty"`
	NxmPrevious string `json:"nxmPrevious,omitempty"`
	// UpdateCheck — когда проверять обновления модов: "start" или пусто —
	// сама при запуске окна, "manual" — только по кнопке.
	UpdateCheck string `json:"updateCheck,omitempty"`
	// Favorites — избранные моды.
	Favorites []string `json:"favorites,omitempty"`
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

func (a *Manager) saveSettings() error {
	data, err := json.MarshalIndent(a.settings, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteFile(filepath.Join(a.home, settingsFile), data)
}

// gameState — папка состояния развёртывания для папки игры. У каждой папки
// своя: смена папки не путает учёт, а прежняя установка остаётся как была.
func (a *Manager) gameState(game string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(game))))
	return filepath.Join(a.home, "deploy", hex.EncodeToString(sum[:6]))
}

// openDeployer открывает развёртывание в выбранную папку; заодно откатывает
// развёртывание, оборвавшееся в прошлый раз.
func (a *Manager) openDeployer() {
	a.deployer, a.recovery, a.deployErr = deploy.Open(a.settings.GameDir, a.gameState(a.settings.GameDir))
	if a.deployErr != nil && errors.Is(a.deployErr, fs.ErrNotExist) {
		a.deployErr = fmt.Errorf("папка %s не найдена", a.settings.GameDir)
	}
}

// SetGame выбирает папку игры.
func (a *Manager) SetGame(dir string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.setGame(dir)
}

// GameDir возвращает выбранную папку игры.
func (a *Manager) GameDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.GameDir
}

func (a *Manager) setGame(dir string) (State, error) {
	if a.openErr != nil {
		return State{}, a.openErr
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return State{}, err
	}
	where := "вручную"
	if installs, err := a.game.Detect(); err == nil {
		for _, inst := range installs {
			if strings.EqualFold(filepath.Clean(inst.Dir), abs) {
				where = inst.Store
			}
		}
	}
	if err := a.setInstall(game.Install{Dir: abs, Store: where}); err != nil {
		return State{}, err
	}
	return a.state()
}

func (a *Manager) setInstall(inst game.Install) error {
	a.settings.GameDir, a.settings.GameStore = inst.Dir, inst.Store
	if err := a.saveSettings(); err != nil {
		return err
	}
	a.openDeployer()
	return nil
}

// Play запускает игру.
func (a *Manager) Play() error {
	a.mu.Lock()
	inst := game.Install{Dir: a.settings.GameDir, Store: a.settings.GameStore}
	a.mu.Unlock()
	if inst.Dir == "" {
		return errors.New("сначала выберите папку игры")
	}
	return a.game.Launch(inst)
}

// layout раскладывает версию мода по папкам игры.
func (a *Manager) layout(v store.Version) (game.Layout, error) {
	paths := make([]string, len(v.Files))
	for i, f := range v.Files {
		paths[i] = f.Path
	}
	if v.AsIs {
		return a.game.Describe(paths), nil
	}
	return a.game.Layout(paths)
}

// DeployResult — состояние окна после развёртывания и сообщение для пользователя.
type DeployResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
}

// Deploy приводит игру в соответствие с профилем.
func (a *Manager) Deploy() (DeployResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	real, err := a.hasMods()
	if err != nil {
		return DeployResult{}, err
	}
	if !real {
		return DeployResult{}, errors.New("сейчас показаны демонстрационные данные: добавьте мод, чтобы было что развернуть")
	}
	if managers := a.managers(); len(managers) > 0 {
		return DeployResult{}, fmt.Errorf("игрой управляет %s; Modvault не развёртывает поверх другого менеджера модов", strings.Join(managers, " и "))
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
		a.rememberDeployed()
		msg = fmt.Sprintf("Развёрнуто: %d %s", res.Changes, plural(res.Changes, "изменение", "изменения", "изменений"))
		if res.Displaced != "" {
			msg += ". Файлы, изменённые вне программы, сохранены в " + res.Displaced
		}
	}
	st, err := a.state()
	return DeployResult{State: st, Message: msg}, err
}

// PlanFiles перечисляет, что развёртывание сделает с каждым файлом.
func (a *Manager) PlanFiles() ([]string, error) {
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
func (a *Manager) plan() (*deploy.Plan, error) {
	if a.deployer == nil && a.deployErr == nil {
		return nil, errors.New("сначала выберите папку игры")
	}
	if a.deployErr != nil {
		return nil, a.deployErr
	}
	plan, _, err := a.planWithNotices()
	return plan, err
}

// planWithNotices рассчитывает развёртывание и возвращает замечания игры.
func (a *Manager) planWithNotices() (*deploy.Plan, []game.Notice, error) {
	if a.deployer == nil && a.deployErr == nil {
		return nil, nil, errors.New("сначала выберите папку игры")
	}
	if a.deployErr != nil {
		return nil, nil, a.deployErr
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return nil, nil, err
	}
	sources, notices, err := a.sources(p)
	if err != nil {
		return nil, notices, err
	}
	plan, err := a.deployer.Plan(sources, p.Winners)
	return plan, notices, err
}

// generatedMod — служебные файлы игры (порядок загрузки, патч) в развёртывании
// выглядят как мод, стоящий последним: они побеждают одноимённые файлы модов.
const generatedMod = "modvault"

// sources собирает включённые моды профиля в порядке загрузки, раскладывает
// их файлы по папкам игры и добавляет служебные файлы игры.
func (a *Manager) sources(p profile.Profile) ([]deploy.Source, []game.Notice, error) {
	var out []deploy.Source
	var notices []game.Notice
	infos := make([]game.ModInfo, 0, len(p.Entries))
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return nil, nil, err
		}
		l, err := a.layout(v)
		if err != nil {
			notices = append(notices, game.Notice{Level: game.Error, Title: fmt.Sprintf("«%s» не развёртывается", v.Name), Detail: err.Error()})
			continue
		}
		infos = append(infos, game.ModInfo{ModID: e.ModID, Enabled: e.Enabled, Layout: l})
		if !e.Enabled {
			continue
		}
		root := a.store.FilesDir(e.ModID, e.VersionID)
		s := deploy.Source{ModID: e.ModID, VersionID: e.VersionID}
		for _, f := range v.Files {
			dst, ok := l.Paths[f.Path]
			if !ok {
				continue
			}
			s.Files = append(s.Files, deploy.File{Path: dst, Src: filepath.Join(root, filepath.FromSlash(f.Path)), Hash: f.Hash, Size: f.Size})
		}
		out = append(out, s)
	}

	generated, gameNotices, err := a.game.Generate(game.Context{
		Dir:      a.settings.GameDir,
		WorkDir:  filepath.Join(a.gameState(a.settings.GameDir), "generated"),
		Mods:     infos,
		Original: a.deployer.Original,
	})
	notices = append(notices, gameNotices...)
	if err != nil {
		return nil, notices, err
	}
	if len(generated) > 0 {
		s := deploy.Source{ModID: generatedMod, VersionID: "1"}
		for _, g := range generated {
			hash, size, err := fsx.HashFile(g.Src)
			if err != nil {
				return nil, notices, err
			}
			s.Files = append(s.Files, deploy.File{Path: g.Path, Src: g.Src, Hash: hash, Size: size})
		}
		out = append(out, s)
	}
	return out, notices, nil
}

// modNames возвращает функцию «идентификатор → название мода».
func (a *Manager) modNames() func(string) string {
	names := map[string]string{}
	if mods, _, err := a.store.List(); err == nil {
		for _, m := range mods {
			names[m.ID] = m.Latest().Name
		}
	}
	names[generatedMod] = "Modvault (служебные файлы)"
	return func(id string) string {
		if name, ok := names[id]; ok {
			return name
		}
		return id
	}
}

// realState собирает состояние окна из хранилища, профиля и развёртывания.
func (a *Manager) realState() (State, error) {
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
		Version:      version.String(),
		Home:         a.home,
		Profile:      p.Name,
		CheckOnStart: a.settings.NexusUser != "" && a.settings.UpdateCheck != updateCheckManual,
		Issues:       []Issue{},
		Mods:         make([]Mod, 0, len(p.Entries)),
		Plan:         []string{},
	}

	var plan *deploy.Plan
	var notices []game.Notice
	var planErr error
	if a.deployer != nil && a.deployErr == nil {
		plan, notices, planErr = a.planWithNotices()
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
	updates := a.loadUpdates()
	inSets := a.setsByMod(p)
	favorite := map[string]bool{}
	for _, id := range a.settings.Favorites {
		favorite[id] = true
	}
	for _, e := range p.Entries {
		v, err := a.store.Get(e.ModID, e.VersionID)
		if err != nil {
			return State{}, err
		}
		row := Mod{
			ID: e.ModID, Name: v.Name, Version: v.Version, Source: v.Source, NexusID: v.NexusID,
			Files: len(v.Files), Versions: len(byID[e.ModID].Versions), Enabled: e.Enabled,
			Sets: inSets[e.ModID], Author: updates.Authors[v.NexusID], AuthorURL: updates.Profiles[v.NexusID],
		}
		if row.Sets == nil {
			row.Sets = []string{}
		}
		row.Favorite = favorite[e.ModID]
		if st, ok := updates.Stats[v.NexusID]; ok && v.NexusID != 0 && updates.Profiles[v.NexusID] != "" {
			row.HasStats = true
			row.Endorsements, row.Downloads, row.UniqueDownloads = st.Endorsements, st.Downloads, st.UniqueDownloads
		}
		switch u, checked := updates.Mods[e.ModID]; {
		case v.NexusID == 0:
		case updates.Gone[v.NexusID]:
			row.UpdateStatus = "missing"
		case checked && u.newerThan(v):
			row.Available, row.UpdateStatus = u.Version, "update"
		case checked && u.NexusID == v.NexusID:
			row.UpdateStatus = "current"
		default:
			row.UpdateStatus = "unknown"
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
	if a.settings.GameDir != "" && a.deployErr == nil {
		for _, m := range a.managers() {
			if m == "Vortex" {
				s.Issues = append(s.Issues, Issue{
					Title:  "Игрой управляет Vortex",
					Detail: "Modvault может перенять его моды без переустановки: файлы игры не изменятся, а вернуть всё Vortex можно в любой момент",
					Level:  LevelWarn, Action: "Перенять у Vortex", Command: "Adopt",
				})
				continue
			}
			s.Issues = append(s.Issues, Issue{
				Title:  "В папке игры лежит " + m,
				Detail: "Пока он есть, Modvault не развёртывает: две программы испортят друг другу учёт. Если вы им для этой игры не пользуетесь, его можно не учитывать",
				Level:  LevelError, Action: "Не учитывать", Command: "IgnoreManagers",
			})
		}
		if err := a.game.Validate(a.settings.GameDir); err != nil {
			s.Issues = append(s.Issues, Issue{Title: "Похоже, это не папка " + a.game.Name(), Detail: err.Error(), Level: LevelWarn, Action: "Выбрать папку", Command: "ChooseGame"})
		}
	}
	for _, n := range notices {
		if n.Level == game.Info {
			continue
		}
		level := LevelWarn
		if n.Level == game.Error {
			level = LevelError
		}
		s.Issues = append(s.Issues, Issue{Title: n.Title, Detail: n.Detail, Level: level})
	}
	for _, problem := range problems {
		s.Issues = append(s.Issues, Issue{Title: "Запись в хранилище повреждена", Detail: problem.Error(), Level: LevelError})
	}
	orderIssues, note := a.orderIssues(p)
	s.Issues = append(s.Issues, orderIssues...)
	s.OrderNote = note
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
		if c.Winner == generatedMod {
			continue // служебный файл заменяет образец из архива — так и задумано
		}
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

func (a *Manager) status(s State, mods int, plan *deploy.Plan) []StatusItem {
	game := StatusItem{Label: "Игра", Value: "папка не выбрана", Level: LevelWarn, Command: "ChooseGame"}
	if a.settings.GameDir != "" {
		game.Value, game.Level = a.settings.GameDir, LevelOK
		if a.settings.GameStore != "" && a.settings.GameStore != "вручную" {
			game.Value = a.game.Name() + " · " + a.settings.GameStore
		}
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
	items := []StatusItem{
		game,
		{Label: "Хранилище", Value: fmt.Sprintf("%d %s", mods, plural(mods, "мод", "мода", "модов")), Level: LevelOK},
		files,
		checks,
	}
	if rec, err := a.loadRecord(); err == nil && rec != nil {
		items = append(items, StatusItem{Label: "Vortex", Value: "отсоединён · вернуть", Level: LevelOff, Command: "Release"})
	}
	return append(items, a.nexusStatus()...)
}

// IgnoreManagers перестаёт учитывать другие менеджеры модов, кроме Vortex:
// пользователь подтвердил, что не пользуется ими для этой игры.
func (a *Manager) IgnoreManagers() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range a.managers() {
		if m != "Vortex" {
			a.settings.IgnoredManagers = append(a.settings.IgnoredManagers, m)
		}
	}
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}
