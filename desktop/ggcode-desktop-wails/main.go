package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	goruntime "runtime"
)

var appInstance *App

type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	debug.Log("stderr", "%s", string(p))
	return len(p), nil
}

func redirectStderr() {
	r, w, err := os.Pipe()
	if err != nil {
		if devNull, err2 := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err2 == nil {
			os.Stderr = devNull
		}
		return
	}
	os.Stderr = w

	var mu sync.Mutex
	buf := make([]byte, 0, 4096)
	go func() {
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf = append(buf, tmp[:n]...)
				for {
					idx := -1
					for i, b := range buf {
						if b == '\n' {
							idx = i
							break
						}
					}
					if idx < 0 {

						if len(buf) > 64*1024 {
							buf = buf[len(buf)-64*1024:]
						}
						break
					}
					line := string(buf[:idx])
					buf = buf[idx+1:]
					if line != "" {
						debug.Log("stderr", "%s", line)
					}
				}
				mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()
}

//go:embed all:frontend/dist
var assets embed.FS

func main() {

	defer func() {
		if r := recover(); r != nil {
			path := agent.WriteCrashLog("desktop", r)

			if f := os.NewFile(2, "/dev/stderr"); f != nil {
				fmt.Fprintf(f, "ggcode desktop crashed: %v (panic log: %s)\n", r, path)
			}
			os.Exit(1)
		}
	}()

	redirectStderr()

	log.SetOutput(logWriter{})
	log.SetFlags(0)

	app := NewApp()

	go app.runSystemTray()
	appInstance = app
	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	safego.Go("desktop.shutdown-signal", func() {
		<-shutdownSignals
		app.shutdown(context.Background())
		os.Exit(0)
	})

	bgColor := &options.RGBA{R: 13, G: 17, B: 23, A: 255}
	if !isSystemDark() {
		bgColor = &options.RGBA{R: 255, G: 255, B: 255, A: 255}
	}

	err := wails.Run(&options.App{
		Title:  "GGCode Desktop",
		Width:  1280,
		Height: 860,

		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "ggcode-desktop-singleton",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				debug.Log("desktop", "second instance attempted launch (args=%d); focusing existing", len(data.Args))

				runtime.WindowShow(app.ctx)
				runtime.WindowUnminimise(app.ctx)
			},
		},
		MinWidth:  900,
		MinHeight: 600,

		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: bgColor,
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {

			if goruntime.GOOS != "darwin" {
				return false
			}

			now := time.Now()
			if app.lastCloseAttempt.Load() != nil {

				return false
			}
			app.lastCloseAttempt.Store(&now)
			runtime.WindowHide(ctx)

			app.enqueueUIEvent("tray:hidden", nil)
			return true
		},
		EnableDefaultContextMenu: true,
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
			CSSDropProperty:    "--wails-drop-target",
			CSSDropValue:       "drop",
		},
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
