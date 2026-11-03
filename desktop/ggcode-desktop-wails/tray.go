package main

import (
	"bufio"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	traySockOnce sync.Once

	trayMu           sync.Mutex
	traySockPath     string
	trayHelperCmd    *exec.Cmd
	trayShuttingDown bool

	trayRespawnFailures int

	trayListener net.Listener
)

func (a *App) runSystemTray() {
	traySockOnce.Do(func() {

		trayMu.Lock()
		if trayShuttingDown {
			trayMu.Unlock()
			debug.Log("desktop", "tray: shutdown raced setup - skipping tray init")
			return
		}

		spawn := a.setupTrayListenerLocked()
		trayMu.Unlock()
		if spawn {
			a.spawnTrayHelper()
		}
	})
}

func (a *App) setupTrayListenerLocked() bool {
	dir, err := os.MkdirTemp("", "ggcode-tray-*")
	if err != nil {
		debug.Log("desktop", "tray: tmpdir: %v", err)
		return false
	}
	traySockPath = filepath.Join(dir, "tray.sock")

	ln, err := net.Listen("unix", traySockPath)
	if err != nil {
		debug.Log("desktop", "tray: listen: %v", err)

		os.RemoveAll(dir)
		traySockPath = ""
		return false
	}
	trayListener = ln

	safego.Go("tray-sock-server", func() {
		for {
			conn, err := ln.Accept()
			if err != nil {

				if errors.Is(err, net.ErrClosed) {
					return
				}
				debug.Log("tray", "accept error: %v (retrying)", err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			safego.Go("tray-conn", func() { a.serveTrayConn(conn) })
		}
	})
	return true
}

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

var trayRespawnBackoff = []time.Duration{
	0, time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second,
}

const minHealthyHelperUptime = 30 * time.Second

func (a *App) spawnTrayHelper() {

	trayMu.Lock()
	path := traySockPath
	if path == "" || trayShuttingDown {

		trayMu.Unlock()
		return
	}
	for _, cand := range trayHelperCandidates() {
		cmd := exec.Command(cand, path)
		hideConsoleWindow(cmd)
		if err := cmd.Start(); err != nil {
			continue
		}
		trayHelperCmd = cmd
		trayMu.Unlock()
		started := time.Now()
		safego.Go("tray-helper-reaper", func() {

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

			if time.Since(started) >= minHealthyHelperUptime {
				trayRespawnFailures = 0
			} else {
				trayRespawnFailures++
			}

			_ = trayRespawnFailures
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

func (a *App) handleTrayShow() {
	debug.Log("desktop", "tray: show window")
	a.lastCloseAttempt.Store(nil)
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	a.enqueueUIEvent("tray:show", nil)
}

func (a *App) handleTrayNewSession() {
	debug.Log("desktop", "tray: new session")
	a.lastCloseAttempt.Store(nil)
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	a.enqueueUIEvent("tray:new-session", nil)
}

func (a *App) handleTrayQuit() {
	debug.Log("desktop", "tray: quit")
	if a.ctx != nil {
		a.shutdown(a.ctx)
	}
	os.Exit(0)
}

func (a *App) removeSystemTray() {

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

	if ln != nil {
		_ = ln.Close()
	}
	if dir != "" {
		os.RemoveAll(dir)
	}
}
