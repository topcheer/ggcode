package main

import (
	"context"
	"embed"
	"os"
	"os/signal"
	"syscall"

	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	safego.Go("desktop.shutdown-signal", func() {
		<-shutdownSignals
		app.shutdown(context.Background())
		os.Exit(0)
	})

	err := wails.Run(&options.App{
		Title:     "GGCode Desktop",
		Width:     1280,
		Height:    860,
		MinWidth:  900,
		MinHeight: 600,
		// Frameless: fully custom-drawn title bar on ALL platforms.
		// TopDragBar.tsx draws traffic lights (macOS) or flat buttons
		// (Windows/Linux) entirely in the webview — no native chrome.
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:         &options.RGBA{R: 13, G: 17, B: 23, A: 255},
		OnStartup:                app.startup,
		OnShutdown:               app.shutdown,
		EnableDefaultContextMenu: true,
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideTitle:                  true,
				HideTitleBar:               true,
				FullSizeContent:            true,
				UseToolbar:                 false,
			},
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
