// Package ui — окно программы: страница и то, что она может запросить.
// Вся работа идёт в пакете manager; здесь только диалоги и передача вызовов.
package ui

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/i18n"
	"github.com/zemidala/modvault/manager"
	"github.com/zemidala/modvault/nexus"
)

//go:embed all:frontend
var frontend embed.FS

// Assets возвращает файлы страницы, вшитые в программу.
func Assets() fs.FS {
	sub, err := fs.Sub(frontend, "frontend")
	if err != nil {
		panic(err) // папка вшита при сборке, ошибка невозможна
	}
	return langFS{sub}
}

// langFile — скрипт, которым страница узнаёт язык окна до своей загрузки.
const langFile = "js/lang.js"

// langFS отдаёт файлы страницы, подставляя в langFile язык этого запуска.
type langFS struct{ fs.FS }

func langScript() []byte {
	return []byte("window.MODVAULT_LANG = " + strconv.Quote(i18n.Language()) + ";\n")
}

func (l langFS) Open(name string) (fs.File, error) {
	if name == langFile {
		return &memFile{name: name, Reader: bytes.NewReader(langScript())}, nil
	}
	return l.FS.Open(name)
}

func (l langFS) ReadFile(name string) ([]byte, error) {
	if name == langFile {
		return langScript(), nil
	}
	return fs.ReadFile(l.FS, name)
}

// memFile — файл из памяти.
type memFile struct {
	name string
	*bytes.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f.name, f.Size()}, nil }
func (f *memFile) Close() error               { return nil }

type memInfo struct {
	name string
	size int64
}

func (i memInfo) Name() string       { return path.Base(i.name) }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

// App — объект, методы которого доступны странице.
type App struct {
	ctx context.Context
	m   *manager.Manager

	mu      sync.Mutex
	ready   bool     // страница готова принимать события
	pending []string // аргументы запуска, которые ждут готовности страницы

	window WindowState // размер и место окна с прошлого запуска
}

func NewApp(m *manager.Manager) *App {
	return &App{m: m, window: LoadWindow()}
}

// Startup вызывается при открытии окна.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.placeWindow()
}

func (a *App) State() (manager.State, error) { return a.m.State() }

func (a *App) SetEnabled(id string, enabled bool) (manager.State, error) {
	return a.m.SetEnabled(id, enabled)
}

func (a *App) ModFiles(id string) ([]string, error)  { return a.m.ModFiles(id) }
func (a *App) Deploy() (manager.DeployResult, error) { return a.m.Deploy() }
func (a *App) PlanFiles() ([]string, error)          { return a.m.PlanFiles() }
func (a *App) Play() error                           { return a.m.Play() }

// AddMod спрашивает у пользователя архив и добавляет мод из него. Если
// пользователь отказался, состояние возвращается прежним.
func (a *App) AddMod() (manager.State, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   i18n.T("Добавить мод из архива"),
		Filters: []runtime.FileFilter{{DisplayName: i18n.T("Архивы модов (*.zip, *.7z, *.rar)"), Pattern: "*.zip;*.7z;*.rar"}},
	})
	if err != nil {
		return manager.State{}, err
	}
	if path == "" {
		return a.m.State()
	}
	return a.m.AddArchive(path)
}

// ChooseGame спрашивает у пользователя папку игры.
func (a *App) ChooseGame() (manager.State, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            i18n.T("Папка игры"),
		DefaultDirectory: a.m.GameDir(),
	})
	if err != nil {
		return manager.State{}, err
	}
	if dir == "" {
		return a.m.State()
	}
	return a.m.SetGame(dir)
}

// Ask — вопрос пользователю перед действием. Окно рисует его само, в стиле
// программы: системные окна Windows сюда не подходят.
type Ask struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	OK      string `json:"ok"`     // подпись кнопки действия
	Danger  bool   `json:"danger"` // действие необратимо
	// Input — у вопроса есть поле ввода; Placeholder — подсказка в нём.
	// Введённое на экране не показывается: это поле для ключей.
	Input       bool   `json:"input"`
	Placeholder string `json:"placeholder"`
	// Secret — введённое не показывается на экране (ключи); Value — то,
	// что стоит в поле сначала.
	Secret bool   `json:"secret"`
	Value  string `json:"value"`
	// Choices — варианты ответа: вопрос решается выбором одного из них.
	Choices []Choice `json:"choices"`
}

// Choice — вариант ответа на вопрос.
type Choice struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Current bool   `json:"current"` // выбран сейчас
}

// RemoveAsk — вопрос перед удалением мода.
func (a *App) RemoveAsk(id string) Ask {
	return Ask{
		Title: i18n.T("Удалить мод"),
		Message: i18n.Sprintf("Удалить «%s» из хранилища?\n\nВсе его версии уйдут в Корзину. Если мод развёрнут, его файлы уберутся из игры при следующем развёртывании.",
			a.m.ModName(id)),
		OK: i18n.T("Удалить"),
	}
}

// RemoveResult — итог удаления. TrashUnavailable — Корзина не приняла мод,
// он остался на месте, и можно спросить, удалять ли насовсем.
type RemoveResult struct {
	State            manager.State `json:"state"`
	TrashUnavailable bool          `json:"trashUnavailable"`
	Ask              *Ask          `json:"ask"`
}

// RemoveMod удаляет мод со всеми версиями: в Корзину или, с permanent, насовсем.
func (a *App) RemoveMod(id string, permanent bool) (RemoveResult, error) {
	s, err := a.m.RemoveMod(id, permanent)
	if errors.Is(err, fsx.ErrTrashUnavailable) {
		s, err = a.m.State()
		return RemoveResult{State: s, TrashUnavailable: true, Ask: &Ask{
			Title:   i18n.T("Корзина недоступна"),
			Message: i18n.T("Положить мод в Корзину не удалось.\n\nУдалить его насовсем? Вернуть его будет нельзя."),
			OK:      i18n.T("Удалить насовсем"),
			Danger:  true,
		}}, err
	}
	return RemoveResult{State: s}, err
}

// AdoptAsk показывает, что будет принято у Vortex.
func (a *App) AdoptAsk() (Ask, error) {
	rep, err := a.m.Adopt(true)
	if err != nil {
		return Ask{}, err
	}
	msg := i18n.Sprintf("Modvault примет у Vortex %d %s (развёрнуто %d) и %d %s в игре.\n\n"+
		"Файлы игры не изменятся. Vortex будет отсоединён: его учёт развёртывания перейдёт к Modvault, "+
		"а хранилище %s останется нетронутым.\n\n"+
		"Вернуть всё как было можно в любой момент — щелчком по «Vortex: отсоединён · вернуть» в строке состояния.",
		rep.Mods, plural(rep.Mods, "мод", "мода", "модов"), rep.Enabled,
		rep.Files, plural(rep.Files, "файл", "файла", "файлов"), rep.Staging)
	if len(rep.Others) > 0 {
		msg += i18n.T("\n\nТакже перестанут учитываться: ") + strings.Join(rep.Others, ", ") + i18n.T(". Не пользуйтесь ими для этой игры.")
	}
	if len(rep.Problems) > 0 {
		msg += i18n.Sprintf("\n\nВнимание: %d %s в игре отличаются от хранилища Vortex — после усыновления они будут показаны как изменённые вне программы.",
			len(rep.Problems), plural(len(rep.Problems), "файл", "файла", "файлов"))
	}
	return Ask{Title: i18n.T("Перенять управление у Vortex"), Message: msg, OK: i18n.T("Перенять")}, nil
}

// Adopt перенимает управление у Vortex.
func (a *App) Adopt() (manager.State, error) {
	if _, err := a.m.Adopt(false); err != nil {
		return manager.State{}, err
	}
	return a.m.State()
}

// ReleaseAsk показывает, что изменится при возврате Vortex.
func (a *App) ReleaseAsk() (Ask, error) {
	rep, err := a.m.Release(true)
	if err != nil {
		return Ask{}, err
	}
	msg := i18n.T("Игра станет такой, какой её оставил Vortex: файлы модов снова будут ссылками на ") + rep.Staging +
		i18n.T(", порядок загрузки и база бандлов — как были.\n\n")
	if rep.Changes > 0 {
		msg += i18n.Sprintf("Изменится %d %s в игре. ", rep.Changes, plural(rep.Changes, "файл", "файла", "файлов"))
	}
	msg += i18n.T("Моды останутся в хранилище Modvault, и перенять управление можно будет снова.")
	return Ask{Title: i18n.T("Вернуть управление Vortex"), Message: msg, OK: i18n.T("Вернуть Vortex")}, nil
}

// Release возвращает управление Vortex.
func (a *App) Release() (manager.State, error) {
	if _, err := a.m.Release(false); err != nil {
		return manager.State{}, err
	}
	return a.m.State()
}

// IgnoreManagersAsk — вопрос перед тем, как перестать учитывать другие менеджеры.
func (a *App) IgnoreManagersAsk() Ask {
	return Ask{
		Title:   i18n.T("Не учитывать другие программы"),
		Message: i18n.T("Modvault перестанет обращать внимание на другие менеджеры модов в папке игры (кроме Vortex).\n\nПодтвердите, что не пользуетесь ими для этой игры: две программы испортят друг другу учёт."),
		OK:      i18n.T("Не учитывать"),
	}
}

// IgnoreManagers перестаёт учитывать другие менеджеры, кроме Vortex.
func (a *App) IgnoreManagers() (manager.State, error) { return a.m.IgnoreManagers() }

// plural выбирает форму слова: 1 мод, 2 мода, 5 модов.
func plural(n int, one, few, many string) string { return manager.Plural(n, one, few, many) }

// SortAsk показывает, что передвинет сортировка по правилам.
func (a *App) SortAsk() (Ask, error) {
	plan, err := a.m.SortPreview()
	if err != nil {
		return Ask{}, err
	}
	ask := Ask{Title: i18n.T("Отсортировать по правилам")}
	intro := i18n.T("Моды встанут так, как требуют правила их авторов.")
	if plan.Auto {
		intro = i18n.T("Ваш загрузчик модов сам расставляет их при запуске игры. Список встанет в том порядке, в каком игра загрузила моды в последний раз; на саму игру это не повлияет.")
	}
	if len(plan.Moves) == 0 {
		ask.Message = intro + i18n.T("\n\nПередвигать нечего: порядок уже такой.")
		return ask, nil // без кнопки действия: только сообщить
	}
	const show = 14
	lines := plan.Moves
	if len(lines) > show {
		lines = append(append([]string(nil), lines[:show]...), i18n.Sprintf("…и ещё %d", len(plan.Moves)-show))
	}
	ask.Message = i18n.Sprintf("%s\n\nПередвинется %d %s:\n%s", intro, len(plan.Moves),
		plural(len(plan.Moves), "мод", "мода", "модов"), strings.Join(lines, "\n"))
	ask.OK = i18n.T("Отсортировать")
	return ask, nil
}

// Sort расставляет моды по правилам.
func (a *App) Sort() (manager.State, error) { return a.m.Sort() }

// MoveMods переставляет моды в порядке загрузки: перед модом target или за ним.
func (a *App) MoveMods(ids []string, target string, after bool) (manager.SetResult, error) {
	return a.m.MoveMods(ids, target, after)
}

// Sets возвращает наборы модов для меню «Набор».
func (a *App) Sets() ([]manager.SetInfo, error) { return a.m.Sets() }

// NewSetAsk — вопрос о названии нового набора; count — сколько модов
// выделено (0 — набор повторит текущий).
func (a *App) NewSetAsk(count int) Ask {
	ask := Ask{Title: i18n.T("Новый набор"), OK: i18n.T("Создать"), Input: true, Placeholder: i18n.T("Название набора")}
	if count == 0 {
		ask.Message = i18n.T("Новый набор повторит текущий: те же моды включены, тот же порядок. Дальше его можно менять отдельно.\n\nТекущий набор останется выбранным.")
		return ask
	}
	ask.Message = i18n.Sprintf("В новом наборе будут включены %d %s и то, без чего они не заработают: загрузчик модов, Darktide Mod Framework и моды, которых они требуют. Остальные моды в нём выключены.\n\nТекущий набор не изменится и останется выбранным.",
		count, plural(count, "выделенный мод", "выделенных мода", "выделенных модов"))
	return ask
}

// CreateSet создаёт набор из модов ids; без них — копию текущего.
func (a *App) CreateSet(name string, ids []string) (manager.SetResult, error) {
	if strings.TrimSpace(name) == "" {
		return manager.SetResult{}, i18n.NewError("введите название набора")
	}
	return a.m.CreateSet(name, ids)
}

// AddToSet включает моды ids в наборе name.
func (a *App) AddToSet(name string, ids []string) (manager.SetResult, error) {
	return a.m.AddToSet(name, ids)
}

// RemoveFromSet выключает моды ids в наборе name.
func (a *App) RemoveFromSet(name string, ids []string) (manager.SetResult, error) {
	return a.m.RemoveFromSet(name, ids)
}

// MoveToSet переносит моды ids из текущего набора в набор name.
func (a *App) MoveToSet(name string, ids []string) (manager.SetResult, error) {
	return a.m.MoveToSet(name, ids)
}

// SetEnabledMany включает или выключает несколько модов текущего набора.
func (a *App) SetEnabledMany(ids []string, enabled bool) (manager.State, error) {
	return a.m.SetEnabledMany(ids, enabled)
}

// SwitchSet выбирает набор и сразу приводит к нему игру.
func (a *App) SwitchSet(name string) (manager.SetResult, error) { return a.m.SwitchSet(name) }

// RenameSetAsk — вопрос о новом названии набора.
func (a *App) RenameSetAsk(name string) Ask {
	return Ask{
		Title: i18n.T("Переименовать набор"), OK: i18n.T("Переименовать"), Input: true,
		Message: i18n.Sprintf("Новое название для набора «%s».", name), Placeholder: i18n.T("Название набора"), Value: name,
	}
}

// RenameSet переименовывает набор.
func (a *App) RenameSet(from, to string) (manager.State, error) {
	if strings.TrimSpace(to) == "" {
		return manager.State{}, i18n.NewError("введите название набора")
	}
	return a.m.RenameSet(from, to)
}

// DeleteSetAsk — вопрос перед удалением набора.
func (a *App) DeleteSetAsk(name string) Ask {
	return Ask{
		Title:   i18n.T("Удалить набор"),
		Message: i18n.Sprintf("Удалить набор «%s»?\n\nМоды останутся в хранилище и в других наборах; пропадёт только этот список включённых модов.", name),
		OK:      i18n.T("Удалить"),
	}
}

// DeleteSet удаляет набор.
func (a *App) DeleteSet(name string) (manager.State, error) { return a.m.DeleteSet(name) }

// NexusKeyAsk — вопрос с полем для ключа Nexus.
func (a *App) NexusKeyAsk() Ask {
	ask := Ask{Title: i18n.T("Ключ Nexus Mods"), OK: i18n.T("Сохранить"), Input: true, Secret: true, Placeholder: "Personal API Key"}
	ask.Message = i18n.T("Ключ нужен, чтобы ставить моды кнопкой «Mod Manager Download» на сайте и проверять обновления.\n\n" +
		"Где взять: nexusmods.com → настройки сайта (Site preferences) → страница API Access → Personal API Key.\n\n" +
		"Ключ хранится в учётных данных Windows; в файлы программы он не попадает.")
	if user := a.m.NexusUser(); user != "" {
		ask.Message = i18n.Sprintf("Сейчас сохранён ключ пользователя %s.\n\nВведите другой ключ, чтобы заменить его, или оставьте поле пустым, чтобы программа забыла ключ.", user)
	}
	return ask
}

// NexusLogin проверяет и запоминает ключ; пустой ключ — забыть сохранённый.
func (a *App) NexusLogin(key string) (manager.State, error) {
	if strings.TrimSpace(key) == "" {
		if a.m.NexusUser() == "" {
			return a.m.State()
		}
		return a.m.NexusLogout()
	}
	return a.m.NexusLogin(a.ctx, key)
}

// CheckUpdates проверяет на Nexus, вышли ли новые версии модов.
// О ходе проверки страница узнаёт из событий «checking».
func (a *App) CheckUpdates() (manager.UpdateReport, error) {
	return a.m.CheckUpdates(a.ctx, func(p manager.CheckProgress) {
		runtime.EventsEmit(a.ctx, "checking", p)
	})
}

// UpdateMod обновляет мод; без Premium открывает страницу файлов мода в браузере.
func (a *App) UpdateMod(id string) (manager.UpdateResult, error) {
	res, err := a.m.UpdateMod(a.ctx, id, a.progress())
	if err == nil && res.URL != "" {
		runtime.BrowserOpenURL(a.ctx, res.URL)
	}
	return res, err
}

// Endorse одобряет мод на Nexus или снимает одобрение.
func (a *App) Endorse(id string, endorse bool) (manager.EndorseResult, error) {
	return a.m.Endorse(a.ctx, id, endorse)
}

// MessageAuthor отправляет личное сообщение автору мода на Nexus.
func (a *App) MessageAuthor(id, title, body string) (string, error) {
	return a.m.MessageAuthor(a.ctx, id, title, body)
}

// OpenNexus открывает страницу мода в браузере.
func (a *App) OpenNexus(id string) error {
	page, err := a.m.ModPage(id)
	if err != nil {
		return err
	}
	runtime.BrowserOpenURL(a.ctx, page)
	return nil
}

// BisectAsk объясняет, как идёт поиск сбойного мода, и спрашивает согласия.
func (a *App) BisectAsk() Ask {
	return Ask{
		Title: i18n.T("Найти сбойный мод"),
		Message: i18n.Sprintf("Если игра падает или ведёт себя странно, а в «Требуют внимания» виновника нет, программа найдёт его делением пополам.\n\n"+
			"Первый запуск — без модов набора: если проблема осталась, дело не в них. Дальше программа включает часть модов и просит запустить игру. "+
			"Вы отвечаете, осталась ли проблема, — и круг сужается вдвое. Для 140 модов это около 10 запусков игры вместо 140.\n\n"+
			"Виновник называется, только когда проблема повторилась с ним одним. Если её вызывают несколько модов вместе, программа найдёт всё сочетание — это дольше.\n\n"+
			"Поиск идёт во временном наборе «%s»: ваш набор не меняется, и в конце программа вернёт игру к нему. Прервать поиск можно в любой момент.", manager.BisectSet),
		OK: i18n.T("Начать поиск"),
	}
}

// StartBisect начинает поиск сбойного мода.
func (a *App) StartBisect() (manager.BisectResult, error) { return a.m.StartBisect() }

// BisectAnswer принимает ответ, осталась ли проблема в этом шаге поиска.
func (a *App) BisectAnswer(problem bool) (manager.BisectResult, error) {
	return a.m.BisectAnswer(problem)
}

// BisectStatus возвращает ход поиска с подсказкой по журналу игры.
func (a *App) BisectStatus() *manager.BisectStatus { return a.m.BisectStatus() }

// CancelBisect прерывает поиск сбойного мода.
func (a *App) CancelBisect() (manager.BisectResult, error) { return a.m.CancelBisect() }

// Journal возвращает последние записи журнала действий.
func (a *App) Journal() ([]manager.Event, error) {
	events, err := a.m.Journal(500)
	if events == nil {
		events = []manager.Event{}
	}
	return events, err
}

// Downloads возвращает загрузки этого запуска программы.
func (a *App) Downloads() []manager.Download { return a.m.Downloads() }

// SetSetting включает или выключает настройку.
func (a *App) SetSetting(key string, on bool) (manager.State, error) {
	if key == manager.SettingNxm {
		// Кто открывает ссылки с сайта, знает только система; окну известен
		// путь к программе, которую нужно назначить.
		if _, ours := a.m.NxmOwner(); ours == on {
			return a.m.State()
		}
		return a.ToggleNxm()
	}
	return a.m.SetSetting(key, on)
}

// HideSetup убирает памятку «Начало работы».
func (a *App) HideSetup() (manager.State, error) { return a.m.HideSetup() }

// Conflicts возвращает разбор конфликтов файлов текущего набора.
func (a *App) Conflicts() ([]manager.ConflictInfo, error) { return a.m.Conflicts() }

// Ответы на вопрос о конфликте, которые не выбирают победителя.
const (
	choiceUnpin   = "unpin"    // снять выбор победителя
	choiceDisable = "disable:" // выключить мод; дальше — его идентификатор
)

// WinnerAsk разбирает конфликт файлов: что он значит, что советует
// программа — и спрашивает, как поступить.
func (a *App) WinnerAsk(key string) (Ask, error) {
	c, err := a.m.Conflict(key)
	if err != nil {
		return Ask{}, err
	}
	var b strings.Builder
	b.WriteString(c.Advice)
	b.WriteString(i18n.T("\n\nКто что кладёт в игру:\n"))
	for _, m := range c.Mods {
		line := i18n.Sprintf("• %s — файлов %d, спорных %d", m.Name, m.Files, m.Shared)
		switch {
		case m.Covered:
			line += i18n.T(", все достаются другому")
		case m.Lost == 0:
			line += i18n.T(", все остаются за ним")
		default:
			line += i18n.Sprintf(", из них теряет %d", m.Lost)
		}
		b.WriteString(line + "\n")
	}
	i18n.Fprintf(&b, "\nСпорные файлы (%d):\n", c.Total)
	const show = 8
	for i, f := range c.Files {
		if i == show {
			i18n.Fprintf(&b, "…и ещё %d\n", c.Total-show)
			break
		}
		b.WriteString(f + "\n")
	}
	if c.Pinned {
		b.WriteString(i18n.T("\nПобедитель выбран вами: конфликт считается решённым."))
	}

	ask := Ask{Title: i18n.T("Конфликт файлов"), Message: strings.TrimSpace(b.String())}
	for _, m := range c.Mods {
		label := i18n.Sprintf("Побеждает «%s»", m.Name)
		var notes []string
		if m.ID == c.Suggested {
			notes = append(notes, i18n.T("рекомендуется"))
		}
		if m.Winner {
			notes = append(notes, i18n.T("сейчас"))
		}
		if m.Default {
			notes = append(notes, i18n.T("по порядку загрузки"))
		}
		if len(notes) > 0 {
			label += " — " + strings.Join(notes, ", ")
		}
		ask.Choices = append(ask.Choices, Choice{Label: label, Value: m.ID, Current: m.Winner && c.Pinned})
	}
	for _, m := range c.Mods {
		if m.ID == c.Disable {
			ask.Choices = append(ask.Choices, Choice{Label: i18n.Sprintf("Выключить «%s» — конфликт исчезнет", m.Name), Value: choiceDisable + m.ID})
		}
	}
	if c.Pinned {
		ask.Choices = append(ask.Choices, Choice{Label: i18n.T("Снять выбор: пусть решает порядок загрузки"), Value: choiceUnpin})
	}
	return ask, nil
}

// SetWinner выполняет решение по конфликту: закрепляет победителя,
// выключает мод или снимает прежний выбор.
func (a *App) SetWinner(key, choice string) (manager.State, error) {
	if choice == choiceUnpin {
		return a.m.UnpinWinner(key)
	}
	if id, ok := strings.CutPrefix(choice, choiceDisable); ok {
		return a.m.SetEnabled(id, false)
	}
	return a.m.SetWinner(key, choice)
}

// DropResult — итог добавления архивов, брошенных в окно.
type DropResult struct {
	State   manager.State `json:"state"`
	Message string        `json:"message"`
	Failed  bool          `json:"failed"` // хотя бы один архив не добавлен
}

// AddDropped добавляет моды из архивов, брошенных мышью в окно.
func (a *App) AddDropped(paths []string) (DropResult, error) {
	var added int
	var problems []string
	for _, path := range paths {
		if _, err := a.m.AddArchive(path); err != nil {
			problems = append(problems, filepath.Base(path)+": "+err.Error())
			continue
		}
		added++
	}
	st, err := a.m.State()
	res := DropResult{State: st, Failed: len(problems) > 0}
	if added > 0 {
		res.Message = i18n.Sprintf("Добавлено модов: %d", added)
	}
	if len(problems) > 0 {
		if res.Message != "" {
			res.Message += ". "
		}
		res.Message += i18n.T("Не добавлено: ") + strings.Join(problems, "; ")
	}
	return res, err
}

// FilesReport объясняет состояние файлов модов в игре.
func (a *App) FilesReport() (manager.FilesReport, error) { return a.m.FilesReport() }

// HideIssue убирает замечание из «Требуют внимания».
func (a *App) HideIssue(key string) (manager.State, error) { return a.m.HideIssue(key) }

// ShowHiddenIssues возвращает скрытые замечания.
func (a *App) ShowHiddenIssues() (manager.State, error) { return a.m.ShowHiddenIssues() }

// ModVersions возвращает версии мода в хранилище.
func (a *App) ModVersions(id string) ([]manager.VersionInfo, error) { return a.m.ModVersions(id) }

// UseVersion выбирает версию мода.
func (a *App) UseVersion(id, versionID string) (manager.SetResult, error) {
	return a.m.UseVersion(id, versionID)
}

// setFilter — файлы наборов в окнах выбора файла.
func setFilter() []runtime.FileFilter {
	return []runtime.FileFilter{{DisplayName: i18n.T("Набор Modvault (*.modvault-set.json)"), Pattern: "*.modvault-set.json;*.json"}}
}

// ExportSet спрашивает, куда сохранить текущий набор, и записывает его в файл.
func (a *App) ExportSet() (manager.SetResult, error) {
	st, err := a.m.State()
	if err != nil {
		return manager.SetResult{}, err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           i18n.T("Сохранить набор в файл"),
		DefaultFilename: st.Profile + ".modvault-set.json",
		Filters:         setFilter(),
	})
	if err != nil || path == "" {
		return manager.SetResult{State: st}, err
	}
	msg, err := a.m.ExportSet(path)
	return manager.SetResult{State: st, Message: msg}, err
}

// ImportSet спрашивает файл набора и создаёт набор по нему.
func (a *App) ImportSet() (manager.ImportResult, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: i18n.T("Загрузить набор из файла"), Filters: setFilter()})
	if err != nil || path == "" {
		return manager.ImportResult{Missing: []manager.MissingMod{}}, err
	}
	return a.m.ImportSet(path)
}

// CollectionAsk — вопрос со ссылкой на коллекцию Nexus.
func (a *App) CollectionAsk() Ask {
	return Ask{
		Title: i18n.T("Загрузить коллекцию Nexus"), OK: i18n.T("Создать набор"), Input: true, Placeholder: i18n.T("Адрес страницы коллекции или её код"),
		Message: i18n.T("Программа создаст набор по коллекции: включит в нём её моды, которые у вас уже есть, и покажет, каких не хватает, со ссылками на загрузку.\n\n" +
			"Мод узнаётся по номеру на Nexus. Если у вас версия новее, чем в коллекции, она и останется. Текущий набор не изменится."),
	}
}

// ImportCollection создаёт набор по коллекции Nexus.
func (a *App) ImportCollection(link string) (manager.ImportResult, error) {
	if strings.TrimSpace(link) == "" {
		return manager.ImportResult{Missing: []manager.MissingMod{}}, i18n.NewError("вставьте адрес страницы коллекции")
	}
	return a.m.ImportCollection(a.ctx, link)
}

// maxPages — сколько страниц открывать в браузере за раз.
const maxPages = 15

// OpenPages открывает в браузере страницы модов на Nexus; посторонние
// адреса пропускает.
func (a *App) OpenPages(urls []string) error {
	opened := 0
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || !(u.Host == "nexusmods.com" || strings.HasSuffix(u.Host, ".nexusmods.com")) {
			continue
		}
		if opened == maxPages {
			break
		}
		runtime.BrowserOpenURL(a.ctx, u.String())
		opened++
	}
	if opened == 0 {
		return i18n.NewError("открывать нечего: у этих модов нет страниц на Nexus")
	}
	return nil
}

// Folders возвращает папки, которые можно открыть в Проводнике.
func (a *App) Folders() map[string]string { return a.m.Folders() }

// OpenFolder открывает в Проводнике папку игры, хранилища или журналов игры.
func (a *App) OpenFolder(kind string) error {
	path, ok := a.m.Folders()[kind]
	if !ok {
		return i18n.NewError("такой папки нет")
	}
	runtime.BrowserOpenURL(a.ctx, path)
	return nil
}

// SetFavorite добавляет моды в избранное или убирает из него.
func (a *App) SetFavorite(ids []string, favorite bool) (manager.State, error) {
	return a.m.SetFavorite(ids, favorite)
}

// OpenAuthor открывает профиль автора мода на Nexus в браузере.
func (a *App) OpenAuthor(id string) error {
	page, err := a.m.AuthorPage(id)
	if err != nil {
		return err
	}
	runtime.BrowserOpenURL(a.ctx, page)
	return nil
}

// NxmAsk — вопрос перед тем, как сменить программу, открывающую ссылки nxm://.
func (a *App) NxmAsk() Ask {
	owner, ours := a.m.NxmOwner()
	if ours {
		return Ask{
			Title:   i18n.T("Ссылки с сайта Nexus"),
			Message: i18n.T("Сейчас кнопку «Mod Manager Download» на сайте обслуживает Modvault.\n\nВернуть её программе, которая обслуживала раньше? Если такой не было, кнопка перестанет работать."),
			OK:      i18n.T("Вернуть"),
		}
	}
	who := i18n.T("Сейчас её обслуживает другая программа; вернуть ей кнопку можно тем же щелчком.")
	switch owner {
	case i18n.T("никто"):
		who = i18n.T("Сейчас её никто не обслуживает.")
	case "Vortex":
		who = i18n.T("Сейчас её обслуживает Vortex; вернуть ему кнопку можно тем же щелчком.")
	}
	return Ask{
		Title:   i18n.T("Ссылки с сайта Nexus"),
		Message: i18n.T("Modvault будет скачивать и ставить моды по кнопке «Mod Manager Download» на сайте Nexus.\n\n") + who + i18n.T("\n\n«Вернуть Vortex» возвращает и кнопку."),
		OK:      i18n.T("Открывать в Modvault"),
	}
}

// ToggleNxm переключает, кто открывает ссылки nxm://: это окно или прежняя программа.
func (a *App) ToggleNxm() (manager.State, error) {
	exe, err := os.Executable()
	if err != nil {
		return manager.State{}, err
	}
	return a.m.ToggleNxm(exe)
}

// Queue запоминает аргументы запуска: ссылку с сайта окно обработает, когда
// страница будет готова показать ход загрузки.
func (a *App) Queue(args []string) {
	a.mu.Lock()
	a.pending = append(a.pending, args...)
	a.mu.Unlock()
}

// Ready вызывает страница, когда готова принимать события.
func (a *App) Ready() {
	a.mu.Lock()
	args := a.pending
	a.pending, a.ready = nil, true
	a.mu.Unlock()
	a.open(args)
}

// Open принимает аргументы второй копии программы: Windows запускает её,
// когда на сайте нажата кнопка загрузки, а работать должна уже открытая.
func (a *App) Open(args []string) {
	a.mu.Lock()
	ready := a.ready
	if !ready {
		a.pending = append(a.pending, args...)
	}
	a.mu.Unlock()
	if ready {
		// Восстанавливать только свёрнутое окно: развёрнутое на весь экран
		// та же команда вернула бы к обычному размеру.
		if runtime.WindowIsMinimised(a.ctx) {
			runtime.WindowUnminimise(a.ctx)
		}
		runtime.WindowShow(a.ctx) // поверх других окон, размер не меняется
		a.open(args)
	}
}

func (a *App) open(args []string) {
	for _, arg := range args {
		if nexus.IsLink(arg) {
			go a.install(arg)
		}
	}
}

// install ставит мод по ссылке и сообщает странице, чем кончилось.
func (a *App) install(link string) {
	res, err := a.m.InstallLink(a.ctx, link, a.progress())
	if err != nil {
		runtime.EventsEmit(a.ctx, "install-failed", err.Error())
		return
	}
	runtime.EventsEmit(a.ctx, "installed", res)
}

// progress возвращает приёмник хода загрузки, который сообщает о нём
// странице не чаще нескольких раз в секунду.
func (a *App) progress() func(manager.Progress) {
	var last time.Time
	return func(p manager.Progress) {
		if now := time.Now(); p.Done == p.Total || now.Sub(last) > 150*time.Millisecond {
			last = now
			runtime.EventsEmit(a.ctx, "download", p)
		}
	}
}
