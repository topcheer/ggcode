package main

// Out-of-process system tray host: the app spawns the trayhelper binary
// (see trayhelper/) and serves a unix socket it reports tray actions on.
// All in-process tray approaches failed on this stack - see trayhelper's
// package comment for the full post-mortem.

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

var (
	traySockOnce  sync.Once
	traySockPath  string
	trayHelperCmd *exec.Cmd
)

func (a *App) runSystemTray() {
	traySockOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ggcode-tray-*")
		if err != nil {
			debug.Log("desktop", "tray: tmpdir: %v", err)
			return
		}
		traySockPath = filepath.Join(dir, "tray.sock")

		ln, err := net.Listen("unix", traySockPath)
		if err != nil {
			debug.Log("desktop", "tray: listen: %v", err)
			return
		}

		// Serve helper connections for the lifetime of the app.
		safego.Go("tray-sock-server", func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return // listener closed during shutdown
				}
				safego.Go("tray-conn", func() { a.serveTrayConn(conn) })
			}
		})

		a.spawnTrayHelper()
	})
}

// trayHelperCandidates returns where the helper binary may live: next to
// the current executable (bundled), or in the module build dir (dev).
func trayHelperCandidates() []string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		yield := []string{
			filepath.Join(dir, "trayhelper"),
			filepath.Join(dir, "..", "trayhelper"),
			filepath.Join(dir, "Contents", "MacOS", "trayhelper"),
		}
		found := make([]string, 0, len(yield))
		for _, p := range yield {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				found = append(found, p)
			}
		}
		if len(found) > 0 {
			return found
		}
	}
	return []string{"trayhelper"} // PATH fallback
}

func (a *App) spawnTrayHelper() {
	for _, cand := range trayHelperCandidates() {
		cmd := exec.Command(cand, traySockPath)
		if err := cmd.Start(); err != nil {
			continue
		}
		trayHelperCmd = cmd
		safego.Go("tray-helper-reaper", func() {
			// Restart the helper if it ever dies unexpectedly (not when the
			// app is shutting down).
			if err := cmd.Wait(); err != nil && traySockPath != "" && a.ctx != nil && a.ctx.Err() == nil {
				debug.Log("desktop", "tray: helper exited (%v), restarting", err)
				a.spawnTrayHelper()
			}
		})
		return
	}
	debug.Log("desktop", "tray: helper binary not found - tray disabled")
}

func (a *App) serveTrayConn(conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		switch sc.Text() {
		case "show":
			a.handleTrayShow()
		case "new-session":
			a.handleTrayNewSession()
		case "quit":
			a.handleTrayQuit()
		}
	}
}

// removeSystemTray tears the tray down during shutdown (both platforms).
func (a *App) removeSystemTray() {
	// Closing the socket makes the helper exit (and remove its status
	// item); also kill it outright in case it is mid-retry-dial.
	if trayHelperCmd != nil && trayHelperCmd.Process != nil {
		trayHelperCmd.Process.Kill()
	}
	if traySockPath != "" {
		os.RemoveAll(filepath.Dir(traySockPath))
		traySockPath = ""
	}
}
