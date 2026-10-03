package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zemidala/modvault/core/fsx"
)

// Размеры окна: наименьшие и те, с которыми оно открывается в первый раз.
const (
	MinWidth      = 960
	MinHeight     = 600
	defaultWidth  = 1280
	defaultHeight = 900
)

// WindowState — размер и место окна, какими их оставил пользователь.
type WindowState struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	// X и Y — место окна на экране; Placed — оно известно.
	X      int  `json:"x"`
	Y      int  `json:"y"`
	Placed bool `json:"placed"`
	// Maximised — окно было развёрнуто на весь экран. Размер и место при
	// этом хранятся прежние: к ним окно вернётся, когда разворот снимут.
	Maximised bool `json:"maximised"`
}

// windowPath — файл с состоянием окна. Он лежит в профиле пользователя, а
// не в хранилище модов: окно одно, где бы ни лежали моды.
func windowPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = "."
	}
	return filepath.Join(base, "Modvault", "window.json")
}

// LoadWindow читает сохранённое состояние окна; если его нет или оно
// испорчено, возвращает размер по умолчанию.
func LoadWindow() WindowState {
	ws := WindowState{Width: defaultWidth, Height: defaultHeight}
	data, err := os.ReadFile(windowPath())
	if err != nil {
		return ws
	}
	var saved WindowState
	if json.Unmarshal(data, &saved) != nil || saved.Width < MinWidth || saved.Height < MinHeight {
		return ws
	}
	return saved
}

func saveWindow(ws WindowState) error {
	data, err := json.Marshal(ws)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(windowPath()), 0o755); err != nil {
		return err
	}
	return fsx.WriteFile(windowPath(), data)
}

// nextWindow решает, что запомнить при закрытии. У развёрнутого и у
// свёрнутого окна текущий размер — не тот, что выбрал пользователь, поэтому
// от них берётся только сам признак разворота.
func nextWindow(prev WindowState, width, height, x, y int, maximised, minimised bool) WindowState {
	if minimised {
		return prev
	}
	if maximised {
		prev.Maximised = true
		return prev
	}
	if width < MinWidth || height < MinHeight {
		prev.Maximised = false
		return prev
	}
	return WindowState{Width: width, Height: height, X: x, Y: y, Placed: true}
}

// Window возвращает состояние, с которым окно нужно открыть.
func (a *App) Window() WindowState { return a.window }

// placeWindow ставит окно туда, где оно стояло в прошлый раз. Если того
// места на экранах больше нет (монитор отключён), окно остаётся по центру.
func (a *App) placeWindow() {
	ws := a.window
	if !ws.Placed || ws.Maximised || !onScreen(ws.X+100, ws.Y+20) {
		return
	}
	// Оконная библиотека читает место окна от угла всех экранов, а ставит —
	// от угла рабочей области текущего. Поправляем на разницу, пока окно
	// не встанет точно.
	x, y := ws.X, ws.Y
	for range 3 {
		runtime.WindowSetPosition(a.ctx, x, y)
		gotX, gotY := runtime.WindowGetPosition(a.ctx)
		if gotX == ws.X && gotY == ws.Y {
			return
		}
		x, y = x-(gotX-ws.X), y-(gotY-ws.Y)
	}
}

// BeforeClose запоминает размер и место окна перед закрытием. Возвращает
// false: закрытию не мешает.
func (a *App) BeforeClose(ctx context.Context) bool {
	width, height := runtime.WindowGetSize(ctx)
	x, y := runtime.WindowGetPosition(ctx)
	ws := nextWindow(a.window, width, height, x, y, runtime.WindowIsMaximised(ctx), runtime.WindowIsMinimised(ctx))
	saveWindow(ws) // не сохранилось — окно просто откроется как в прошлый раз
	return false
}
