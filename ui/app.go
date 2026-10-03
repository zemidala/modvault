// Package ui — окно программы: страница и то, что она может запросить.
// Вся работа идёт в пакете manager; здесь только диалоги и передача вызовов.
package ui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zemidala/modvault/core/fsx"
	"github.com/zemidala/modvault/manager"
)

//go:embed all:frontend
var frontend embed.FS

// Assets возвращает файлы страницы, вшитые в программу.
func Assets() fs.FS {
	sub, err := fs.Sub(frontend, "frontend")
	if err != nil {
		panic(err) // папка вшита при сборке, ошибка невозможна
	}
	return sub
}

// App — объект, методы которого доступны странице.
type App struct {
	ctx context.Context
	m   *manager.Manager
}

func NewApp(m *manager.Manager) *App {
	return &App{m: m}
}

// Startup вызывается при открытии окна.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
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
		Title:   "Добавить мод из архива",
		Filters: []runtime.FileFilter{{DisplayName: "Архивы модов (*.zip, *.7z, *.rar)", Pattern: "*.zip;*.7z;*.rar"}},
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
		Title:            "Папка игры",
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
}

// RemoveAsk — вопрос перед удалением мода.
func (a *App) RemoveAsk(id string) Ask {
	return Ask{
		Title: "Удалить мод",
		Message: fmt.Sprintf("Удалить «%s» из хранилища?\n\nВсе его версии уйдут в Корзину. Если мод развёрнут, его файлы уберутся из игры при следующем развёртывании.",
			a.m.ModName(id)),
		OK: "Удалить",
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
			Title:   "Корзина недоступна",
			Message: "Положить мод в Корзину не удалось.\n\nУдалить его насовсем? Вернуть его будет нельзя.",
			OK:      "Удалить насовсем",
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
	msg := fmt.Sprintf("Modvault примет у Vortex %d %s (развёрнуто %d) и %d %s в игре.\n\n"+
		"Файлы игры не изменятся. Vortex будет отсоединён: его учёт развёртывания перейдёт к Modvault, "+
		"а хранилище %s останется нетронутым.\n\n"+
		"Вернуть всё как было можно в любой момент — щелчком по «Vortex: отсоединён · вернуть» в строке состояния.",
		rep.Mods, plural(rep.Mods, "мод", "мода", "модов"), rep.Enabled,
		rep.Files, plural(rep.Files, "файл", "файла", "файлов"), rep.Staging)
	if len(rep.Others) > 0 {
		msg += "\n\nТакже перестанут учитываться: " + strings.Join(rep.Others, ", ") + ". Не пользуйтесь ими для этой игры."
	}
	if len(rep.Problems) > 0 {
		msg += fmt.Sprintf("\n\nВнимание: %d %s в игре отличаются от хранилища Vortex — после усыновления они будут показаны как изменённые вне программы.",
			len(rep.Problems), plural(len(rep.Problems), "файл", "файла", "файлов"))
	}
	return Ask{Title: "Перенять управление у Vortex", Message: msg, OK: "Перенять"}, nil
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
	msg := "Игра станет такой, какой её оставил Vortex: файлы модов снова будут ссылками на " + rep.Staging +
		", порядок загрузки и база бандлов — как были.\n\n"
	if rep.Changes > 0 {
		msg += fmt.Sprintf("Изменится %d %s в игре. ", rep.Changes, plural(rep.Changes, "файл", "файла", "файлов"))
	}
	msg += "Моды останутся в хранилище Modvault, и перенять управление можно будет снова."
	return Ask{Title: "Вернуть управление Vortex", Message: msg, OK: "Вернуть Vortex"}, nil
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
		Title:   "Не учитывать другие программы",
		Message: "Modvault перестанет обращать внимание на другие менеджеры модов в папке игры (кроме Vortex).\n\nПодтвердите, что не пользуетесь ими для этой игры: две программы испортят друг другу учёт.",
		OK:      "Не учитывать",
	}
}

// IgnoreManagers перестаёт учитывать другие менеджеры, кроме Vortex.
func (a *App) IgnoreManagers() (manager.State, error) { return a.m.IgnoreManagers() }

func plural(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}
