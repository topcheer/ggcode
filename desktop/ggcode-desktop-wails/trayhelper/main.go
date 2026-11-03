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

		for i := 0; i < 50; i++ {
			c, err := net.Dial("unix", sockPath)
			if err == nil {
				conn = c
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

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
