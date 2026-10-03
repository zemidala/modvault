// Package ui — окно программы: страница и то, что она может запросить.
// Вся работа идёт в пакете manager; здесь только диалоги и передача вызовов.
package ui

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

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

// RemoveMod после подтверждения удаляет мод со всеми версиями в Корзину.
// Если Корзина недоступна, спрашивает, удалять ли насовсем.
func (a *App) RemoveMod(id string) (manager.State, error) {
	if !a.confirm("Удалить мод", fmt.Sprintf("Удалить «%s» из хранилища?\n\nВсе его версии уйдут в Корзину. Если мод развёрнут, его файлы уберутся из игры при следующем развёртывании.", a.m.ModName(id))) {
		return a.m.State()
	}
	s, err := a.m.RemoveMod(id, false)
	if errors.Is(err, fsx.ErrTrashUnavailable) {
		if !a.confirm("Корзина недоступна", "Положить мод в Корзину не удалось.\n\nУдалить его насовсем? Вернуть его будет нельзя.") {
			return a.m.State()
		}
		s, err = a.m.RemoveMod(id, true)
	}
	return s, err
}

func (a *App) confirm(title, message string) bool {
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type: runtime.QuestionDialog, Title: title, Message: message, DefaultButton: "No",
	})
	return err == nil && answer == "Yes"
}
