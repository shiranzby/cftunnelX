package main

import (
	"context"
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:  "cftunnelX",
		Width:  1280,
		Height: 820,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		Bind: []interface{}{app},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
