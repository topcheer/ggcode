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
	"time"

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
	// #1403-B: consecutive short-lived helper runs - drives the respawn
	// backoff ladder (reset after a healthy run).
	trayRespawnFailures int
	// #1403-B2: retained so removeSystemTray can close the listener and
	// unblock the accept loop instead of leaking it to process exit.
	trayListener net.Listener
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
			// #1403-B4: the MkdirTemp dir above leaks on listen failure -
			// nothing references it anymore.
			os.RemoveAll(dir)
			traySockPath = ""
			return
		}
		trayListener = ln

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

// trayRespawnBackoff sequences the wait before each respawn attempt
// (#1403-B): a crash-looping helper (corrupted binary, Gatekeeper denial,
// missing dylib) respawned every few milliseconds - fork/exec storm for
// the rest of the app's life. Steps up to a 30s ceiling; resets on any
// run that lived long enough to be healthy.
var trayRespawnBackoff = []time.Duration{
	0, time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second,
}

// minHealthyHelperUptime marks a helper run that lasted long enough to
// reset the backoff ladder (a helper that ran a minute did not crash-loop).
const minHealthyHelperUptime = 30 * time.Second

func (a *App) spawnTrayHelper() {
	// #1403-A: the spawn path (sockPath read, Start, cmd assignment) used
	// to run fully OUTSIDE trayMu while removeSystemTray reads/writes the
	// same fields under it - a deterministic data race, and a teardown that
	// swept between the reaper's unlocked check and our assignment killed
	// the OLD (already dead) cmd while the NEW helper ran on unsupervised
	// (bounded to ~5s by the helper's dial-exit, but incorrect).
	trayMu.Lock()
	path := traySockPath
	if path == "" || trayShuttingDown {
		// Socket torn down (or teardown in flight) since the reaper's check:
		// spawning now would create exactly the orphan #1351 fixed.
		trayMu.Unlock()
		return
	}
	for _, cand := range trayHelperCandidates() {
		cmd := exec.Command(cand, path)
		if err := cmd.Start(); err != nil {
			continue
		}
		trayHelperCmd = cmd
		trayMu.Unlock()
		started := time.Now()
		safego.Go("tray-helper-reaper", func() {
			// Restart the helper if it dies unexpectedly - never during or
			// after shutdown. #1351: guards read state under trayMu.
			// #1403-B: with backoff - see trayRespawnBackoff.
			err := cmd.Wait()
			trayMu.Lock()
			shutdown := trayShuttingDown
			curPath := traySockPath
			trayMu.Unlock()
			if shutdown || curPath == "" || a.ctx == nil || a.ctx.Err() != nil {
				return
			}
			if err == nil {
				return
			}
			// Backoff ladder: advance while runs stay short, reset after a
			// healthy run. Runs beyond minHealthyHelperUptime are normal
			// restarts, not crash loops.
			if time.Since(started) >= minHealthyHelperUptime {
				trayRespawnFailures = 0
			} else {
				trayRespawnFailures++
			}
			idx := trayRespawnFailures
			if idx >= len(trayRespawnBackoff) {
				idx = len(trayRespawnBackoff) - 1
			}
			wait := trayRespawnBackoff[idx]
			debug.Log("desktop", "tray: helper exited (%v), restart in %v (failure #%d)", err, wait, trayRespawnFailures)
			if wait > 0 {
				select {
				case <-time.After(wait):
				case <-a.ctx.Done():
					return
				}
			}
			a.spawnTrayHelper()
		})
		return
	}
	trayMu.Unlock()
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
	ln := trayListener
	dir := ""
	if traySockPath != "" {
		dir = filepath.Dir(traySockPath)
		traySockPath = ""
	}
	trayMu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
	}
	// #1403-B2: closing the listener unblocks the accept goroutine (the
	// old comment claimed "listener closed during shutdown" but nothing
	// ever closed it - the loop leaked until process exit).
	if ln != nil {
		_ = ln.Close()
	}
	if dir != "" {
		os.RemoveAll(dir)
	}
}
