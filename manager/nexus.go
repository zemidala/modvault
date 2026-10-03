package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zemidala/modvault/core/deploy"
	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/core/store"
	"github.com/zemidala/modvault/internal/version"
	"github.com/zemidala/modvault/nexus"
)

// Nexus: ключ, загрузка по ссылке с сайта, проверка обновлений. В сеть
// программа ходит только по просьбе пользователя и никогда — под замком
// Manager: загрузка идёт долго, а окно в это время должно отвечать.

const (
	downloadsDir = "downloads"
	updatesFile  = "updates.json"
	nexusSource  = "Nexus Mods"
)

// errNoNexusKey — ключ ещё не введён.
var errNoNexusKey = errors.New("ключ Nexus не задан: щёлкните «Nexus» в строке состояния или выполните modvault nexus login")

// client возвращает клиента Nexus с сохранённым ключом.
func (a *Manager) client() (*nexus.Client, error) {
	if a.nexus != nil {
		return a.nexus, nil
	}
	key, err := a.keys.Load()
	if err != nil {
		return nil, fmt.Errorf("ключ Nexus не прочитан: %w", err)
	}
	if key == "" {
		return nil, errNoNexusKey
	}
	a.nexus = &nexus.Client{Base: a.nexusBase, Key: key, Version: version.Version}
	return a.nexus, nil
}

// domain — имя игры в адресах Nexus.
func (a *Manager) domain() (string, error) {
	d := a.game.NexusDomain()
	if d == "" {
		return "", fmt.Errorf("модов %s на Nexus нет", a.game.Name())
	}
	return d, nil
}

// NexusUser возвращает имя владельца сохранённого ключа; пусто — ключа нет.
func (a *Manager) NexusUser() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.NexusUser
}

// NexusLogin проверяет ключ на Nexus и запоминает его.
func (a *Manager) NexusLogin(ctx context.Context, key string) (State, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return State{}, errors.New("ключ пуст")
	}
	a.mu.Lock()
	base := a.nexusBase
	a.mu.Unlock()

	c := &nexus.Client{Base: base, Key: key, Version: version.Version}
	user, err := c.Validate(ctx)
	if err != nil {
		return State{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return State{}, a.openErr
	}
	if err := a.keys.Save(key); err != nil {
		return State{}, fmt.Errorf("ключ не сохранён в учётных данных Windows: %w", err)
	}
	a.nexus = c
	a.settings.NexusUser, a.settings.NexusPremium = user.Name, user.Premium
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}

// NexusLogout забывает ключ.
func (a *Manager) NexusLogout() (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return State{}, a.openErr
	}
	if err := a.keys.Delete(); err != nil {
		return State{}, err
	}
	a.nexus = nil
	a.settings.NexusUser, a.settings.NexusPremium = "", false
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}

// Progress — ход загрузки файла.
type Progress struct {
	Name  string `json:"name"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"` // 0 — размер неизвестен
}

// InstallResult — состояние окна после установки и сообщение для пользователя.
type InstallResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
}

// InstallLink скачивает и ставит мод по ссылке nxm:// с сайта.
func (a *Manager) InstallLink(ctx context.Context, raw string, progress func(Progress)) (InstallResult, error) {
	link, err := nexus.ParseLink(raw)
	if err != nil {
		return InstallResult{}, err
	}
	a.mu.Lock()
	c, err := a.client()
	domain, derr := a.domain()
	a.mu.Unlock()
	if err := errors.Join(err, derr); err != nil {
		return InstallResult{}, err
	}
	if link.Game != domain {
		return InstallResult{}, fmt.Errorf("ссылка для другой игры (%s), а Modvault ведёт %s", link.Game, a.game.Name())
	}
	return a.installFile(ctx, c, domain, link.ModID, link.FileID, link.Key, link.Expires, progress)
}

// installFile скачивает файл мода во временный файл, сверяет его и только
// потом кладёт в хранилище. Обрыв на любом шаге хранилище не трогает.
func (a *Manager) installFile(ctx context.Context, c *nexus.Client, domain string, modID, fileID int, key, expires string, progress func(Progress)) (result InstallResult, err error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	// Один файл — одна загрузка: вторая писала бы в тот же временный файл.
	a.mu.Lock()
	busy := a.downloading[fileID]
	if !busy {
		if a.downloading == nil {
			a.downloading = map[int]bool{}
		}
		a.downloading[fileID] = true
	}
	a.mu.Unlock()
	if busy {
		return InstallResult{}, errors.New("этот файл уже скачивается")
	}
	defer func() {
		a.mu.Lock()
		delete(a.downloading, fileID)
		a.mu.Unlock()
	}()

	// Загрузка видна в разделе «Загрузки», её итог попадает в журнал.
	track := a.startDownload(modID, fileID)
	defer func() { a.finishDownload(track, result.Message, err) }()

	files, err := c.Files(ctx, domain, modID)
	if err != nil {
		return InstallResult{}, fmt.Errorf("мод %d: %w", modID, err)
	}
	file, ok := files.Find(fileID)
	if !ok {
		// Файл скрыт со страницы, но по прямой ссылке ещё отдаётся.
		if file, err = c.File(ctx, domain, modID, fileID); err != nil {
			return InstallResult{}, fmt.Errorf("файл %d мода %d: %w", fileID, modID, err)
		}
	}
	mod, err := c.Mod(ctx, domain, modID)
	if err != nil {
		return InstallResult{}, fmt.Errorf("мод %d: %w", modID, err)
	}
	urls, err := c.DownloadLinks(ctx, domain, modID, fileID, key, expires)
	if errors.Is(err, nexus.ErrForbidden) {
		if key == "" {
			return InstallResult{}, fmt.Errorf("без Premium файл скачивается только кнопкой «Mod Manager Download» на сайте: %w", err)
		}
		return InstallResult{}, fmt.Errorf("ссылка с сайта устарела — нажмите кнопку загрузки ещё раз: %w", err)
	}
	if err != nil {
		return InstallResult{}, err
	}

	name := safeFileName(file.FileName)
	if name == "" {
		name = fmt.Sprintf("nexus-%d-%d", modID, fileID)
	}
	title := mod.Name
	if title == "" {
		title = name
	}
	a.describeDownload(track, title, firstNonEmpty(file.Version, file.ModVersion, mod.Version), file.Size)
	dir := filepath.Join(a.home, downloadsDir)
	// Номер файла в имени: начатую загрузку другого файла не примут за эту.
	part := filepath.Join(dir, fmt.Sprintf("%d-%s.part", fileID, name))
	err = nexus.Download(ctx, urls, part, file.Size, func(done, total int64) {
		a.progressDownload(track, done, total)
		progress(Progress{Name: title, Done: done, Total: total})
	})
	if err != nil {
		return InstallResult{}, fmt.Errorf("«%s»: %w", title, err)
	}

	// Сверка: Nexus узнаёт свои архивы по MD5. Свежий файл он может ещё не
	// знать — тогда остаётся сверка размера, которую сделала загрузка.
	if sum, err := nexus.MD5File(part); err == nil {
		if match, found, err := c.KnownMD5(ctx, domain, sum, modID, fileID); err == nil && found && !match {
			os.Remove(part)
			return InstallResult{}, fmt.Errorf("«%s»: скачанный файл не совпал с тем, что лежит на Nexus; он удалён, попробуйте ещё раз", title)
		}
	}
	// Номер файла и в имени архива: два файла с одним именем, скачанные
	// разом, не затрут друг друга.
	archive := filepath.Join(dir, fmt.Sprintf("%d-%s", fileID, name))
	os.Remove(archive)
	if err := os.Rename(part, archive); err != nil {
		return InstallResult{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return InstallResult{}, a.openErr
	}
	info := store.Info{
		Source: nexusSource, NexusID: modID, FileID: fileID,
		Version: firstNonEmpty(file.Version, file.ModVersion, mod.Version),
	}
	info.Name = mod.Name
	if file.Optional() && file.Name != "" {
		info.Name = file.Name
	}
	if info.Name == "" {
		info.Name = fmt.Sprintf("Nexus %d", modID)
	}
	info.Name = a.nexusName(info.Name, modID, file, files)
	// Автор известен из того же ответа Nexus: запоминаем, чтобы не спрашивать снова.
	known := a.loadUpdates()
	known.Authors[modID], known.Profiles[modID] = firstNonEmpty(mod.Author, mod.UploadedBy), mod.AuthorPage(domain)
	known.Stats[modID] = statsOf(mod, time.Now().UTC())
	known.ModCategory[modID] = mod.CategoryID
	// Поставленный файл — последнее, что известно о моде: он актуален, пока
	// проверка не найдёт новее.
	delete(known.Gone, modID)
	a.saveUpdates(known)

	synced := a.inSync() // до обновления: потом профиль уже отличается от игры
	v, prev, err := a.addVersion(archive, info)
	if err != nil {
		if errors.Is(err, errAlreadyStored) {
			os.Remove(archive)
			return InstallResult{}, err
		}
		return InstallResult{}, fmt.Errorf("%w (скачанный архив остался: %s)", err, archive)
	}
	os.Remove(archive) // копия архива теперь лежит в хранилище

	msg := fmt.Sprintf("Установлен: %s %s", v.Name, v.Version)
	if prev != "" {
		msg = a.deployUpdate(v, synced)
	} else if a.deployer != nil {
		msg += ". Чтобы он попал в игру — «Развернуть»"
	}
	st, err := a.state()
	return InstallResult{State: st, Message: msg}, err
}

// partAge — сколько ждёт продолжения недокачанный файл. Ссылка на файл
// живёт недолго, и к загрузке недельной давности уже не вернутся.
const partAge = 7 * 24 * time.Hour

// cleanDownloads убирает из папки загрузок давно брошенные недокачанные
// файлы. Скачанные архивы не трогает: они остаются, только если мод не
// удалось добавить, и могут понадобиться.
func (a *Manager) cleanDownloads() {
	dir := filepath.Join(a.home, downloadsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > partAge {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// inSync сообщает, что игра сейчас совпадает с профилем и развёртывать
// в неё можно: папка выбрана и ею не управляет другой менеджер модов.
func (a *Manager) inSync() bool {
	if a.deployer == nil || a.deployErr != nil || len(a.managers()) > 0 {
		return false
	}
	plan, err := a.plan()
	return err == nil && plan.Empty()
}

// deployUpdate доводит обновление мода до игры, не спрашивая: включённый
// мод остаётся включённым, значит, новая версия сразу ложится на место
// прежней; выключенный остаётся выключенным. synced — совпадала ли игра
// с профилем до обновления: если нет, в профиле есть и другие
// неразвёрнутые изменения, и развёртывать их заодно без спроса нельзя.
// Возвращает сообщение для пользователя.
func (a *Manager) deployUpdate(v store.Version, synced bool) string {
	msg := fmt.Sprintf("Обновлён: %s до %s", v.Name, v.Version)
	p, _, err := a.loadProfile()
	if err != nil {
		return msg
	}
	if i := p.Index(v.ModID); i < 0 || !p.Entries[i].Enabled {
		return msg + ". Мод выключен и остаётся выключенным"
	}
	if a.deployer == nil {
		return msg
	}
	if a.settings.ManualUpdates {
		return msg + ". В игру он попадёт по кнопке «Развернуть»"
	}
	if !synced {
		return msg + ". В игру он попадёт по кнопке «Развернуть»: там ждут и другие изменения"
	}
	plan, err := a.plan()
	if err == nil && !plan.Empty() {
		if _, err = a.deployer.Apply(plan); err == nil {
			a.recovery = deploy.NothingToRecover
			a.rememberDeployed()
		}
	}
	if err != nil {
		return msg + ". Развернуть новую версию не удалось: " + err.Error() + ". Игра осталась на прежней; нажмите «Развернуть», когда причина устранена"
	}
	return msg + " — новая версия уже в игре"
}

// nexusName решает, каким модом хранилища станет файл с Nexus: версией уже
// установленного мода или новым модом. Возвращает название, под которым
// его добавлять. file и files могут быть пустыми, если о файле ничего не
// известно, кроме номера мода.
func (a *Manager) nexusName(name string, nexusID int, file nexus.File, files nexus.Files) string {
	mods, _, err := a.store.List()
	if err != nil {
		return name
	}
	var same []store.Version
	for _, m := range mods {
		if v := m.Latest(); v.NexusID == nexusID {
			same = append(same, v)
		}
	}
	// Тот же файл или файл, пришедший ему на смену.
	for _, v := range same {
		if file.ID != 0 && v.NexusFileID != 0 && (v.NexusFileID == file.ID || files.Replaces(v.NexusFileID, file.ID)) {
			return v.Name
		}
	}
	for _, v := range same {
		if v.ModID == store.Slug(name) {
			return v.Name
		}
	}
	if file.Optional() {
		return name // дополнение к моду — отдельный мод
	}
	// Основной файл обновляет мод с этим номером, если тот сам не дополнение.
	for _, v := range same {
		if stored, ok := files.Find(v.NexusFileID); !ok || !stored.Optional() {
			return v.Name
		}
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// safeFileName оставляет от имени, которое прислал сервер, только имя файла
// без знаков, недопустимых в Windows.
func safeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	return strings.Trim(name, " .")
}

// update — последний файл мода на Nexus, каким его застала проверка.
type update struct {
	NexusID int    `json:"nexusId"`
	FileID  int    `json:"fileId"`
	Version string `json:"version"`
}

// newerThan сообщает, что файл новее установленной версии мода.
func (u update) newerThan(v store.Version) bool {
	if u.NexusID != v.NexusID || (v.NexusFileID != 0 && v.NexusFileID == u.FileID) {
		return false
	}
	return nexus.Newer(v.Version, u.Version)
}

// updates — итог последней проверки обновлений.
type updates struct {
	// Checked — когда проверка в последний раз прошла целиком.
	Checked time.Time `json:"checked"`
	// Mods — последний файл на Nexus для мода хранилища.
	Mods map[string]update `json:"mods"`
	// Authors — автор мода по его номеру на Nexus; пустая строка — Nexus
	// автора не назвал или мода там больше нет.
	Authors map[int]string `json:"authors"`
	// Profiles — адрес профиля автора на Nexus; запись есть у каждого мода,
	// о котором уже спрашивали, даже если адрес пуст.
	Profiles map[int]string `json:"profiles"`
	// Categories — названия категорий модов игры по номеру; ModCategory —
	// номер категории мода по его номеру на Nexus.
	Categories  map[int]string `json:"categories,omitempty"`
	ModCategory map[int]int    `json:"modCategory,omitempty"`
	// Gone — моды, которых на Nexus больше нет: страница убрана или скрыта.
	Gone map[int]bool `json:"gone,omitempty"`
	// Stats — одобрения и скачивания мода по его номеру на Nexus.
	Stats map[int]modStats `json:"stats"`
}

// modStats — статистика мода на Nexus, какой её застал запрос.
type modStats struct {
	Endorsements    int       `json:"endorsements"`
	Downloads       int       `json:"downloads"`
	UniqueDownloads int       `json:"uniqueDownloads"`
	At              time.Time `json:"at"`
}

// statsAge — как долго статистика считается свежей: чаще её не запрашиваем,
// чтобы проверка обновлений оставалась быстрой.
const statsAge = 7 * 24 * time.Hour

func statsOf(m nexus.ModInfo, at time.Time) modStats {
	return modStats{Endorsements: m.Endorsements, Downloads: m.Downloads, UniqueDownloads: m.UniqueDownloads, At: at}
}

func (a *Manager) updatesPath() string {
	return filepath.Join(a.home, "nexus", updatesFile)
}

// loadUpdates читает итог последней проверки; нет файла или он испорчен —
// считается, что проверки не было.
func (a *Manager) loadUpdates() updates {
	var u updates
	if data, err := os.ReadFile(a.updatesPath()); err == nil {
		json.Unmarshal(data, &u)
	}
	if u.Mods == nil {
		u.Mods = map[string]update{}
	}
	if u.Authors == nil {
		u.Authors = map[int]string{}
	}
	if u.Profiles == nil {
		u.Profiles = map[int]string{}
	}
	if u.Stats == nil {
		u.Stats = map[int]modStats{}
	}
	if u.Gone == nil {
		u.Gone = map[int]bool{}
	}
	if u.Categories == nil {
		u.Categories = map[int]string{}
	}
	if u.ModCategory == nil {
		u.ModCategory = map[int]int{}
	}
	return u
}

func (a *Manager) saveUpdates(u updates) error {
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.updatesPath()), 0o755); err != nil {
		return err
	}
	return fsx.WriteFile(a.updatesPath(), data)
}

// CheckProgress — шаг проверки обновлений: до запроса о моде и после ответа.
type CheckProgress struct {
	Done  int `json:"done"`  // о скольких модах уже есть ответ
	Total int `json:"total"` // о скольких спрашиваем
	// Name и Mods — название и идентификаторы модов, о которых этот шаг.
	Name string   `json:"name"`
	Mods []string `json:"mods"`
	// Finished — ответ получен; Available — найденная версия новее
	// установленной; Missing — мода на Nexus больше нет.
	Finished  bool   `json:"finished"`
	Available string `json:"available"`
	Missing   bool   `json:"missing"`
	// Первый шаг проверки вместо мода несёт расклад: Queued — о ком будут
	// спрашивать, Unchanged — кто с прошлой проверки не менялся.
	Queued    []string `json:"queued"`
	Unchanged []string `json:"unchanged"`
}

// UpdateReport — итог проверки обновлений.
type UpdateReport struct {
	State    State  `json:"state"`
	Message  string `json:"message"`
	Mods     int    `json:"mods"`     // модов с номером на Nexus
	Unknown  int    `json:"unknown"`  // модов без номера: их проверить нельзя
	Updates  int    `json:"updates"`  // модов, у которых есть версия новее
	Requests int    `json:"requests"` // запросов к Nexus
}

// updateCheckManual — значение настройки «проверять обновления только по кнопке».
const updateCheckManual = "manual"

// SetUpdateCheck задаёт, когда проверять обновления: при запуске окна
// (onStart) или только по кнопке.
func (a *Manager) SetUpdateCheck(onStart bool) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openErr != nil {
		return State{}, a.openErr
	}
	a.settings.UpdateCheck = updateCheckManual
	if onStart {
		a.settings.UpdateCheck = ""
	}
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	return a.state()
}

// Сколько живёт итог проверки: у Nexus список «что менялось» есть только
// за последний месяц, после него проверяются все моды заново.
const updatesHorizon = 27 * 24 * time.Hour

// CheckUpdates узнаёт на Nexus, вышли ли новые версии модов профиля.
// Первый раз спрашивает о каждом моде, потом — только о менявшихся.
// progress получает ход проверки: о каком моде спрашивают и что узнали.
func (a *Manager) CheckUpdates(ctx context.Context, progress func(CheckProgress)) (UpdateReport, error) {
	if progress == nil {
		progress = func(CheckProgress) {}
	}
	type target struct {
		modID   string
		nexusID int
		fileID  int
		v       store.Version
	}
	var rep UpdateReport

	a.mu.Lock()
	c, err := a.client()
	domain, derr := a.domain()
	var targets []target
	var cache updates
	if err = errors.Join(err, derr, a.openErr); err == nil {
		var real bool
		if real, err = a.hasMods(); err == nil && real {
			p, _, perr := a.loadProfile()
			err = perr
			for _, e := range p.Entries {
				v, gerr := a.store.Get(e.ModID, e.VersionID)
				if gerr != nil {
					err = gerr
					break
				}
				if v.NexusID == 0 {
					rep.Unknown++
					continue
				}
				targets = append(targets, target{e.ModID, v.NexusID, v.NexusFileID, v})
			}
		}
		cache = a.loadUpdates()
	}
	a.mu.Unlock()
	if err != nil {
		return rep, err
	}
	rep.Mods = len(targets)

	started := time.Now().UTC()
	// О каких модах спрашивать: обо всех или только о менявшихся с прошлой проверки.
	ask := map[int]bool{}
	age := started.Sub(cache.Checked)
	if cache.Checked.IsZero() || age > updatesHorizon || age < 0 {
		for _, t := range targets {
			ask[t.nexusID] = true
		}
	} else if len(targets) > 0 {
		period := nexus.PeriodMonth
		switch {
		case age < 23*time.Hour:
			period = nexus.PeriodDay
		case age < 6*24*time.Hour:
			period = nexus.PeriodWeek
		}
		changed, err := c.Updated(ctx, domain, period)
		rep.Requests++
		if err != nil {
			return rep, err
		}
		since := cache.Checked.Add(-time.Hour).Unix() // запас на расхождение часов
		recent := map[int]bool{}
		for _, u := range changed {
			if u.LatestFileUpdate >= since {
				recent[u.ModID] = true
			}
		}
		for _, t := range targets {
			if known, ok := cache.Mods[t.modID]; recent[t.nexusID] || !ok || known.NexusID != t.nexusID {
				ask[t.nexusID] = true
			}
		}
	}

	// У кого автор ещё неизвестен или статистика устарела: о таких модах
	// спрашиваем страницу мода. Автор запоминается насовсем, статистика
	// обновляется не чаще раза в неделю.
	wantAuthor := map[int]bool{}
	for _, t := range targets {
		_, known := cache.Profiles[t.nexusID]
		_, sorted := cache.ModCategory[t.nexusID]
		stats, counted := cache.Stats[t.nexusID]
		if !known || !sorted || !counted || started.Sub(stats.At) > statsAge {
			wantAuthor[t.nexusID] = true
		}
	}

	ids := make([]int, 0, len(ask)+len(wantAuthor))
	for id := range ask {
		ids = append(ids, id)
	}
	for id := range wantAuthor {
		if !ask[id] {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	// Расклад до первого запроса о моде: кто в очереди, а о ком уже известно,
	// что с прошлой проверки он не менялся.
	plan := CheckProgress{Total: len(ids)}
	for _, t := range targets {
		if ask[t.nexusID] || wantAuthor[t.nexusID] {
			plan.Queued = append(plan.Queued, t.modID)
		} else {
			plan.Unchanged = append(plan.Unchanged, t.modID)
		}
	}
	progress(plan)
	var failure error
	// Названия категорий спрашиваем один раз: они общие для всей игры.
	if len(cache.Categories) == 0 && len(targets) > 0 {
		names, err := c.Categories(ctx, domain)
		rep.Requests++
		if err == nil {
			cache.Categories = names
		}
	}
	for i, id := range ids {
		// Моды хранилища с этим номером на Nexus: обычно один.
		step := CheckProgress{Done: i, Total: len(ids)}
		for _, t := range targets {
			if t.nexusID == id {
				step.Mods = append(step.Mods, t.modID)
				if step.Name == "" {
					step.Name = t.v.Name
				}
			}
		}
		progress(step)
		step.Done, step.Finished = i+1, true

		gone := func(err error) bool {
			return errors.Is(err, nexus.ErrNotFound) || errors.Is(err, nexus.ErrForbidden)
		}
		if ask[id] {
			files, err := c.Files(ctx, domain, id)
			rep.Requests++
			if gone(err) {
				step.Missing = true // мод убран с Nexus или скрыт автором
				cache.Gone[id] = true
				cache.Authors[id], cache.Profiles[id] = "", ""
				cache.Stats[id] = modStats{At: started}
				cache.ModCategory[id] = 0
				progress(step)
				continue
			}
			if err != nil {
				failure = err
				break
			}
			delete(cache.Gone, id)
			for _, t := range targets {
				if t.nexusID != id {
					continue
				}
				if latest, ok := files.Latest(t.fileID); ok {
					cache.Mods[t.modID] = update{NexusID: id, FileID: latest.ID, Version: firstNonEmpty(latest.Version, latest.ModVersion)}
				}
			}
		}
		if wantAuthor[id] {
			info, err := c.Mod(ctx, domain, id)
			rep.Requests++
			switch {
			case gone(err):
				cache.Authors[id], cache.Profiles[id] = "", ""
				cache.Stats[id] = modStats{At: started}
				cache.ModCategory[id] = 0
			case err != nil:
				failure = err
			default:
				cache.Authors[id] = firstNonEmpty(info.Author, info.UploadedBy)
				cache.Profiles[id] = info.AuthorPage(domain)
				cache.Stats[id] = statsOf(info, started)
				cache.ModCategory[id] = info.CategoryID
			}
			if failure != nil {
				break
			}
		}
		for _, t := range targets {
			if found, ok := cache.Mods[t.modID]; ok && t.nexusID == id && found.newerThan(t.v) {
				step.Available = found.Version
			}
		}
		progress(step)
	}
	if failure == nil {
		cache.Checked = started
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// Что успели узнать, сохраняется и при сбое: повтор начнёт не с нуля.
	if err := a.saveUpdates(cache); err != nil {
		return rep, errors.Join(failure, err)
	}
	if failure != nil {
		a.noteError("Проверка обновлений прервана", failure)
		return rep, fmt.Errorf("проверка обновлений прервана: %w", failure)
	}
	if rep.State, err = a.state(); err != nil {
		return rep, err
	}
	for _, m := range rep.State.Mods {
		if m.Available != "" {
			rep.Updates++
		}
	}
	rep.Message = "Обновлений нет"
	if rep.Updates > 0 {
		rep.Message = fmt.Sprintf("%s: %d", plural(rep.Updates, "Есть обновление", "Есть обновления", "Есть обновления"), rep.Updates)
	}
	rep.Message += fmt.Sprintf(" · проверено модов: %d", rep.Mods)
	if rep.Unknown > 0 {
		rep.Message += fmt.Sprintf(", без номера на Nexus: %d", rep.Unknown)
	}
	if l := c.Limits(); l.Known {
		rep.Message += fmt.Sprintf(" · запросов сегодня осталось: %d", l.Daily)
	}
	a.note(EventNexus, "Проверка обновлений: "+rep.Message)
	return rep, nil
}

// UpdateResult — итог просьбы обновить мод. Без Premium Nexus отдаёт файл
// только по кнопке на сайте: тогда URL — страница, которую нужно открыть.
type UpdateResult struct {
	State   State  `json:"state"`
	Message string `json:"message"`
	URL     string `json:"url"`
}

// UpdateMod обновляет мод до версии, найденной проверкой обновлений.
func (a *Manager) UpdateMod(ctx context.Context, id string, progress func(Progress)) (UpdateResult, error) {
	a.mu.Lock()
	v, err := a.profileVersion(id)
	c, cerr := a.client()
	domain, derr := a.domain()
	found, known := a.loadUpdates().Mods[id]
	premium := a.settings.NexusPremium
	_, ours := a.nxmOwner()
	a.mu.Unlock()
	if err := errors.Join(err, cerr, derr); err != nil {
		return UpdateResult{}, err
	}
	if !known || !found.newerThan(v) {
		return UpdateResult{}, fmt.Errorf("для «%s» обновление не найдено: проверьте обновления ещё раз", v.Name)
	}
	if premium {
		res, err := a.installFile(ctx, c, domain, v.NexusID, found.FileID, "", "", progress)
		return UpdateResult{State: res.State, Message: res.Message}, err
	}
	res := UpdateResult{URL: nexus.DownloadPage(domain, v.NexusID, found.FileID)}
	res.Message = fmt.Sprintf("Открыта страница загрузки «%s» %s: нажмите на ней кнопку загрузки", v.Name, found.Version)
	if !ours {
		res.Message = fmt.Sprintf("Открыта страница загрузки «%s». Ссылки с сайта сейчас открывает не Modvault: включите их щелчком по «Ссылки nxm» в строке состояния или скачайте архив вручную и добавьте его кнопкой «Добавить мод»", v.Name)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	res.State, err = a.state()
	return res, err
}

// ModPage возвращает адрес страницы мода на Nexus.
func (a *Manager) ModPage(id string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, err := a.profileVersion(id)
	if err != nil {
		return "", err
	}
	domain, err := a.domain()
	if err != nil {
		return "", err
	}
	return nexus.ModPage(domain, v.NexusID), nil
}

// AuthorPage возвращает адрес профиля автора мода на Nexus.
func (a *Manager) AuthorPage(id string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, err := a.profileVersion(id)
	if err != nil {
		return "", err
	}
	page := a.loadUpdates().Profiles[v.NexusID]
	if page == "" {
		return "", fmt.Errorf("профиль автора «%s» неизвестен: проверьте обновления", v.Name)
	}
	return page, nil
}

// profileVersion возвращает версию мода, выбранную в профиле, если у мода
// известен номер на Nexus.
func (a *Manager) profileVersion(id string) (store.Version, error) {
	if real, err := a.hasMods(); err != nil || !real {
		return store.Version{}, errors.Join(err, errors.New("это демонстрационный мод: на Nexus его нет"))
	}
	p, _, err := a.loadProfile()
	if err != nil {
		return store.Version{}, err
	}
	i := p.Index(id)
	if i < 0 {
		return store.Version{}, fmt.Errorf("мод %q не найден", id)
	}
	v, err := a.store.Get(id, p.Entries[i].VersionID)
	if err != nil {
		return store.Version{}, err
	}
	if v.NexusID == 0 {
		return store.Version{}, fmt.Errorf("у «%s» неизвестен номер на Nexus", v.Name)
	}
	return v, nil
}

// nxmOwner сообщает, кто открывает ссылки nxm://, и не Modvault ли это.
func (a *Manager) nxmOwner() (owner string, ours bool) {
	if a.protocol == nil {
		return "", false
	}
	cmd, err := a.protocol.Command()
	switch {
	case err != nil:
		return "не удалось узнать", false
	case cmd == "":
		return "никто", false
	case cmd == a.settings.NxmCommand:
		return "Modvault", true
	case strings.Contains(strings.ToLower(cmd), "vortex"):
		return "Vortex", false
	}
	return "другая программа", false
}

// NxmOwner сообщает, кто открывает ссылки nxm:// с сайта Nexus.
func (a *Manager) NxmOwner() (owner string, ours bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nxmOwner()
}

// ToggleNxm переключает, кто открывает ссылки nxm://: Modvault (программой
// exe) или тот, кто открывал их до него.
func (a *Manager) ToggleNxm(exe string) (State, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.protocol == nil || a.openErr != nil {
		return State{}, errors.Join(errors.New("ссылками nxm:// управляет только окно программы"), a.openErr)
	}
	if _, ours := a.nxmOwner(); ours {
		if err := a.protocol.SetCommand(a.settings.NxmPrevious); err != nil {
			return State{}, err
		}
		a.settings.NxmCommand, a.settings.NxmPrevious = "", ""
		if err := a.saveSettings(); err != nil {
			return State{}, err
		}
		return a.state()
	}
	current, err := a.protocol.Command()
	if err != nil {
		return State{}, err
	}
	// Кого возвращать, записываем до того, как занять его место.
	a.settings.NxmCommand, a.settings.NxmPrevious = `"`+exe+`" "%1"`, current
	if err := a.saveSettings(); err != nil {
		return State{}, err
	}
	if err := a.protocol.SetCommand(a.settings.NxmCommand); err != nil {
		a.settings.NxmCommand, a.settings.NxmPrevious = "", ""
		return State{}, errors.Join(err, a.saveSettings())
	}
	return a.state()
}

// returnNxm отдаёт ссылки nxm:// прежнему обработчику, если их держит Modvault.
func (a *Manager) returnNxm() {
	if _, ours := a.nxmOwner(); !ours {
		return
	}
	if a.protocol.SetCommand(a.settings.NxmPrevious) == nil {
		a.settings.NxmCommand, a.settings.NxmPrevious = "", ""
	}
}

// nxmLabel — подпись пункта «Ссылки nxm» для владельца из nxmOwner.
func nxmLabel(owner string) string {
	switch owner {
	case "никто":
		return "никто не открывает"
	case "не удалось узнать":
		return owner
	}
	return "открывает " + owner
}

// nexusStatus — пункты строки состояния о Nexus.
func (a *Manager) nexusStatus() []StatusItem {
	if a.game.NexusDomain() == "" {
		return nil
	}
	item := StatusItem{Label: "Nexus", Value: "ключ не задан", Level: LevelOff, Command: "NexusKey"}
	if a.settings.NexusUser != "" {
		item.Value, item.Level = a.settings.NexusUser, LevelOK
		if a.settings.NexusPremium {
			item.Value += " · Premium"
		}
	}
	items := []StatusItem{item}
	if owner, ours := a.nxmOwner(); owner != "" {
		level := LevelOff
		if ours {
			level = LevelOK
		}
		items = append(items, StatusItem{Label: "Ссылки nxm", Value: nxmLabel(owner), Level: level, Command: "ToggleNxm"})
	}
	return items
}
