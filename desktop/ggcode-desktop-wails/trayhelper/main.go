// Command trayhelper is the out-of-process system tray for GGCode Desktop.
//
// WHY A SEPARATE PROCESS: every in-process approach failed - hand-rolled
// CGO NSStatusItem never rendered on macOS 26 (five variations), and
// energye/systray in both Run() and RunWithExternalLoop() modes races
// Wails' ownership of the NSApplication main loop and the app dies within
// seconds. Running the tray in its own tiny process sidesteps the runtime
// entirely: this binary owns its main thread, so the library runs in its
// native mode (this is the pattern real Wails v2 tray apps converge on).
//
// Protocol: helper connects to the socket path in argv[1] and writes one
// line per tray action: "show", "new-session", "quit". It exits when the
// main app closes the socket (process teardown), which also removes the
// status item.
package main

import (
	"net"
	"os"
	"time"

	systray "github.com/energye/systray"
)

func main() {
	sockPath := ""
	if len(os.Args) > 1 {
		sockPath = os.Args[1]
	}
	var conn net.Conn
	if sockPath != "" {
		// Main app may not have bound the socket yet - retry briefly.
		for i := 0; i < 50; i++ {
			c, err := net.Dial("unix", sockPath)
			if err == nil {
				conn = c
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// #1351: all retries exhausted - the main app (or its socket dir) is
	// gone. Exit instead of running a ghost tray: the socket-death watcher
	// below only starts when conn != nil, so without this early exit a
	// racing respawn leaves an orphan icon that responds to nothing.
	if conn == nil {
		os.Exit(1)
	}
	send := func(line string) {
		if conn == nil {
			return
		}
		conn.Write([]byte(line + "\n"))
	}

	onReady := func() {
		systray.SetTitle(">_")
		systray.SetTooltip("GGCode")
		systray.SetOnClick(func(menu systray.IMenu) {
			send("show")
		})
		systray.AddMenuItem("Show GGCode", "Open the main window").Click(func() { send("show") })
		systray.AddMenuItem("New Session", "Open and start a new session").Click(func() { send("new-session") })
		systray.AddSeparator()
		systray.AddMenuItem("Quit GGCode", "").Click(func() { send("quit") })
	}
	onExit := func() {
		if conn != nil {
			conn.Close()
		}
		os.Exit(0)
	}

	// Watch for main-app death: when the socket closes, tear the tray down
	// so the icon never outlives the app.
	if conn != nil {
		go func() {
			buf := make([]byte, 16)
			for {
				if _, err := conn.Read(buf); err != nil {
					systray.Quit()
					return
				}
			}
		}()
	}

	systray.Run(onReady, onExit)
}
