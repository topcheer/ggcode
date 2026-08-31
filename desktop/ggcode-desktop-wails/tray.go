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
	traySockOnce sync.Once
	// trayMu guards traySockPath / trayHelperCmd / trayShuttingDown.
	// The reaper goroutine reads them while removeSystemTray (called from
	// the shutdown path) writes them - unlocked access was a data race that
	// let the reaper respawn a helper mid-shutdown (#1351).
	trayMu           sync.Mutex
	traySockPath     string
	trayHelperCmd    *exec.Cmd
	trayShuttingDown bool
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
//
// #1350: no PATH fallback by design - exec'ing whatever "trayhelper"
// happens to be on PATH would run an unknown binary with the app's socket
// path as argv[1]. Bundled-next-to-exe is the only supported production
// layout (the release script builds the helper into Contents/MacOS/);
// when it is absent the tray stays disabled with a debug log rather than
// guessing. The third candidate previously joined to
// <exe-dir>/Contents/MacOS/trayhelper - inside a bundle the exe already
// lives in Contents/MacOS, so that resolved to a dead nested path.
func trayHelperCandidates() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	dir := filepath.Dir(exe)
	yield := []string{
		filepath.Join(dir, "trayhelper"),
		filepath.Join(dir, "..", "trayhelper"),
	}
	found := make([]string, 0, len(yield))
	for _, p := range yield {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			found = append(found, p)
		}
	}
	return found
}

func (a *App) spawnTrayHelper() {
	for _, cand := range trayHelperCandidates() {
		cmd := exec.Command(cand, traySockPath)
		if err := cmd.Start(); err != nil {
			continue
		}
		trayHelperCmd = cmd
		safego.Go("tray-helper-reaper", func() {
			// Restart the helper if it dies unexpectedly - never during or
			// after shutdown. #1351: the old guard read traySockPath unlocked
			// (data race with removeSystemTray) and relied on a.ctx cancel
			// that the shutdown path never performs, so a Kill-during-teardown
			// could slip past all guards and respawn an orphan helper.
			// cmd.Wait also reaps the killed process (no zombie).
			if err := cmd.Wait(); err != nil {
				trayMu.Lock()
				shutdown := trayShuttingDown
				path := traySockPath
				trayMu.Unlock()
				if !shutdown && path != "" && a.ctx != nil && a.ctx.Err() == nil {
					debug.Log("desktop", "tray: helper exited (%v), restarting", err)
					a.spawnTrayHelper()
				}
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
	//
	// #1351: set the shutdown flag and clear the sock path BEFORE Kill so
	// the reaper woken by the kill observes the torn-down state under the
	// same mutex and does not respawn mid-teardown. The reaper's cmd.Wait
	// reaps the killed process.
	trayMu.Lock()
	trayShuttingDown = true
	cmd := trayHelperCmd
	dir := ""
	if traySockPath != "" {
		dir = filepath.Dir(traySockPath)
		traySockPath = ""
	}
	trayMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
	if dir != "" {
		os.RemoveAll(dir)
	}
}
