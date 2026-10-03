// Команда modvault-gui — окно менеджера модов.
//
// Собирается с тегами: go build -tags desktop,production -ldflags "-H windowsgui".
package main

import (
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/zemidala/modvault/ui"
)

func main() {
	app := ui.NewApp()

	err := wails.Run(&options.App{
		Title:     "Modvault",
		Width:     1280,
		Height:    900,
		MinWidth:  960,
		MinHeight: 600,
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
