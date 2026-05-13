package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	chatService := NewChatService()
	dc := chatService.GetDesktopConfig()

	// Auto-initialize if we have a saved workDir.
	if dc.WorkDir != "" {
		if err := chatService.InitFromWorkDir(dc.WorkDir); err != nil {
			log.Printf("[desktop] Auto-load workdir %s failed: %v", dc.WorkDir, err)
		}
	}

	w, h := dc.WinW, dc.WinH
	if w == 0 {
		w = 1200
	}
	if h == 0 {
		h = 800
	}

	app := application.New(application.Options{
		Name:        "ggcode",
		Description: "AI-powered coding assistant",
		Services: []application.Service{
			application.NewService(chatService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	chatService.SetApp(app)

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "ggcode",
		Width:  w,
		Height: h,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(30, 30, 30),
		URL:              "/",
	})

	mainWindow.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		mainWindow.Hide()
	})

	// ─── Menu ──────────────────────────────────────────
	menu := app.NewMenu()

	fileMenu := menu.AddSubmenu("File")
	fileMenu.Add("New Chat").SetAccelerator("CmdOrCtrl+N").OnClick(func(_ *application.Context) {
		chatService.ClearMessages()
	})
	fileMenu.AddSeparator()
	fileMenu.Add("Close Window").SetAccelerator("CmdOrCtrl+W").OnClick(func(_ *application.Context) {
		mainWindow.Hide()
	})
	fileMenu.AddSeparator()
	fileMenu.Add("Quit").SetAccelerator("CmdOrCtrl+Q").OnClick(func(_ *application.Context) {
		app.Quit()
	})

	editMenu := menu.AddSubmenu("Edit")
	editMenu.Add("Undo").SetAccelerator("CmdOrCtrl+Z").OnClick(func(_ *application.Context) {})
	editMenu.Add("Redo").SetAccelerator("CmdOrCtrl+Shift+Z").OnClick(func(_ *application.Context) {})
	editMenu.AddSeparator()
	editMenu.Add("Cut").SetAccelerator("CmdOrCtrl+X").OnClick(func(_ *application.Context) {})
	editMenu.Add("Copy").SetAccelerator("CmdOrCtrl+C").OnClick(func(_ *application.Context) {})
	editMenu.Add("Paste").SetAccelerator("CmdOrCtrl+V").OnClick(func(_ *application.Context) {})
	editMenu.Add("Select All").SetAccelerator("CmdOrCtrl+A").OnClick(func(_ *application.Context) {})

	viewMenu := menu.AddSubmenu("View")
	viewMenu.Add("Toggle Full Screen").SetAccelerator("Ctrl+Cmd+F").OnClick(func(_ *application.Context) {
		mainWindow.ToggleFullscreen()
	})

	helpMenu := menu.AddSubmenu("Help")
	helpMenu.Add("About ggcode").OnClick(func(_ *application.Context) {
		app.Menu.ShowAbout()
	})

	app.Menu.SetApplicationMenu(menu)

	// ─── System Tray ────────────────────────────────
	trayMenu := app.NewMenu()
	trayMenu.Add("Show ggcode").OnClick(func(_ *application.Context) {
		mainWindow.Show().Focus()
	})
	trayMenu.AddSeparator()
	trayMenu.Add("New Chat").OnClick(func(_ *application.Context) {
		chatService.ClearMessages()
		mainWindow.Show().Focus()
	})
	trayMenu.AddSeparator()
	trayMenu.Add("Quit ggcode").OnClick(func(_ *application.Context) {
		app.Quit()
	})

	tray := app.SystemTray.New()
	tray.SetLabel("G")
	tray.SetTooltip("ggcode - AI Coding Assistant")
	tray.SetMenu(trayMenu)
	tray.OnClick(func() {
		mainWindow.Show().Focus()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
