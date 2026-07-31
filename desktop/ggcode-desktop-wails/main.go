package main

import (
	"context"
	"embed"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// logWriter redirects standard library log output to debug.Log so that
// third-party libraries (pion/turn, pion/webrtc) writing via the standard
// log package don't corrupt the terminal output by writing to stderr.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	debug.Log("stderr", "%s", string(p))
	return len(p), nil
}

// redirectStderr replaces os.Stderr with a pipe that captures all writes
// and routes them to debug.Log. This catches ALL writes to os.Stderr
// regardless of how the library obtained the reference.
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
	// Redirect os.Stderr at the file descriptor level to prevent
	// third-party libraries from corrupting terminal output.
	redirectStderr()

	// Also redirect the standard log package's default output.
	log.SetOutput(logWriter{})
	log.SetFlags(0)

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
