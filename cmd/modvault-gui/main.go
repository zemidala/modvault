// Команда modvault-gui — окно менеджера модов.
//
// Собирается с тегами: go build -tags desktop,production -ldflags "-H windowsgui".
package main

import (
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/zemidala/modvault/manager"
	"github.com/zemidala/modvault/ui"
)

func main() {
	app := ui.NewApp(manager.New())
	// Windows запускает программу со ссылкой nxm:// в аргументах, когда на
	// сайте Nexus нажата кнопка загрузки.
	app.Queue(os.Args[1:])

	window := app.Window()
	startState := options.Normal
	if window.Maximised {
		startState = options.Maximised
	}

	err := wails.Run(&options.App{
		// Окно одно: вторая копия отдаёт свои аргументы первой и закрывается.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "modvault-7c1f4a52-9d0e-4b7b-a6c3-2f5e8d1b0a94",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) { app.Open(data.Args) },
		},
		Title: "Modvault",
		// Окно открывается таким, каким его закрыли.
		Width:            window.Width,
		Height:           window.Height,
		MinWidth:         ui.MinWidth,
		MinHeight:        ui.MinHeight,
		WindowStartState: startState,
		OnBeforeClose:    app.BeforeClose,
		// Цвет окна до загрузки страницы — тот же, что у её фона.
		BackgroundColour: &options.RGBA{R: 0x1b, G: 0x17, B: 0x14, A: 0xff},
		AssetServer:      &assetserver.Options{Assets: ui.Assets()},
		OnStartup:        app.Startup,
		Bind:             []interface{}{app},
	})
	if err != nil {
		fatal(err)
	}
}
