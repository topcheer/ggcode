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

// appInstance holds a reference to the running App for tray callbacks.
// Set in main(), used by exported CGO functions in tray_export_darwin.go.
var appInstance *App

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
						// #1431-C: data without a newline (\r progress
						// bars, cgo diagnostic blobs on fd 2) used to stay
						// in buf forever - unbounded growth (~17MB/day at
						// 200B/s). Cap the retained tail; overflow drops
						// the OLDEST bytes.
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
	// Top-level panic containment (1a of the v1.3.224 crash follow-up):
	// the Wails run loop, tray callbacks and any non-safego path panic here
	// previously took the whole desktop app down with nothing on disk.
	// Crash log goes to ~/.ggcode/crash/; stderr is a pipe to debug.Log at
	// this point, so the crash note also goes to the debug ring for
	// post-mortem (desktop has no terminal to print to).
	defer func() {
		if r := recover(); r != nil {
			path := agent.WriteCrashLog("desktop", r)
			// Write the crash note straight to fd 2 — the REAL stderr.
			// os.Stderr was replaced with the pipe write end by
			// redirectStderr (which never saves the original), so a
			// log.Printf / os.Stderr write lands in the pipe whose reader
			// goroutine only feeds the in-memory debug ring — and the
			// os.Exit below kills the process before anything drains it,
			// so the note vanished (#1314; the "restore" a previous fix
			// claimed never existed). fd 2 itself is untouched by the
			// redirect and remains visible to the parent/launch context,
			// next to the durable ~/.ggcode/crash/ log written above.
			if f := os.NewFile(2, "/dev/stderr"); f != nil {
				fmt.Fprintf(f, "ggcode desktop crashed: %v (panic log: %s)\n", r, path)
			}
			os.Exit(1)
		}
	}()

	// Redirect os.Stderr at the file descriptor level to prevent
	// third-party libraries from corrupting terminal output.
	redirectStderr()

	// Also redirect the standard log package's default output.
	log.SetOutput(logWriter{})
	log.SetFlags(0)

	app := NewApp()
	// System tray (energye/systray) must start before wails.Run; it parks
	// in its own goroutine and never touches the main thread.
	go app.runSystemTray()
	appInstance = app
	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	safego.Go("desktop.shutdown-signal", func() {
		<-shutdownSignals
		app.shutdown(context.Background())
		os.Exit(0)
	})

	// Determine system appearance for initial window background.
	// This prevents a flash of wrong-colored background before React mounts.
	bgColor := &options.RGBA{R: 13, G: 17, B: 23, A: 255} // dark default (#0D1117)
	if !isSystemDark() {
		bgColor = &options.RGBA{R: 255, G: 255, B: 255, A: 255} // light (#FFFFFF)
	}

	err := wails.Run(&options.App{
		Title:  "GGCode Desktop",
		Width:  1280,
		Height: 860,
		// #1431-B: no single-instance guard - a second launch (Dock
		// double-open / updater race) left TWO trays, fought over the
		// session JSONL lock, A2A port, tunnel share and IM adapters
		// (QQ multi-device kick), with the second instance half-initialized.
		// The second launch just focuses the first and exits.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "ggcode-desktop-singleton",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				debug.Log("desktop", "second instance attempted launch (args=%d); focusing existing", len(data.Args))
			},
		},
		MinWidth:  900,
		MinHeight: 600,
		// Frameless: fully custom-drawn title bar on ALL platforms.
		// TopDragBar.tsx draws traffic lights (macOS) or flat buttons
		// (Windows/Linux) entirely in the webview — no native chrome.
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: bgColor,
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			// Close-to-tray: hide the window instead of quitting.
			// The user can re-open via the tray icon or fully quit via tray menu.
			//
			// Non-macOS: initSystemTray is a no-op (tray_other.go) - there is
			// NO tray icon to re-open or quit from, so intercepting here would
			// strand the window with no exit path. Quit for real instead.
			if goruntime.GOOS != "darwin" {
				return false
			}
			// X always hides to tray: every re-show path (tray Show /
			// New Session, hotkey, Dock reopen, SetWindowFocused) re-arms
			// the flag, so the next X hides again by design. Quitting is via
			// the tray right-click menu (Quit GGCode) or Cmd+Q - the tray is
			// always visible, so there is always an exit path. The
			// second-attempt early-return below only fires when the window
			// was re-shown WITHOUT going through a flag-clearing path (e.g.
			// a native unhide racing the frontend focus event), where
			// quitting on X is the safe fallback rather than trapping.
			now := time.Now()
			if app.lastCloseAttempt.Load() != nil {
				// Second close attempt -> actually quit
				return false
			}
			app.lastCloseAttempt.Store(&now) // #700: atomic (4 goroutines touch this)
			runtime.WindowHide(ctx)
			// Notify frontend that window was hidden to tray
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
