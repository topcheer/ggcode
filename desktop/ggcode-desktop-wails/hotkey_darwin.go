//go:build darwin

package main

import "C"

import (
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	hotkeyPollerMu sync.Mutex
	hotkeyStop     chan struct{}
)

func hotkeyPollerRunning() bool {
	hotkeyPollerMu.Lock()
	defer hotkeyPollerMu.Unlock()
	return hotkeyStop != nil
}

func (a *App) startHotkeyPoller() {
	hotkeyPollerMu.Lock()
	defer hotkeyPollerMu.Unlock()
	if hotkeyStop != nil {
		return
	}
	stop := make(chan struct{})
	hotkeyStop = stop
	var done <-chan struct{}
	if a.ctx != nil {
		done = a.ctx.Done()
	}
	safego.Go("hotkey-poller", func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-done:

				hotkeyPollerMu.Lock()
				if hotkeyStop == stop {
					hotkeyStop = nil
				}
				hotkeyPollerMu.Unlock()
				return
			case <-ticker.C:
				if C.gcPollHotkey() != 0 {
					a.toggleWindowViaHotkey()
				}
			}
		}
	})
}

func (a *App) stopHotkeyPoller() {
	hotkeyPollerMu.Lock()
	defer hotkeyPollerMu.Unlock()
	if hotkeyStop != nil {
		close(hotkeyStop)
		hotkeyStop = nil
	}
}

func (a *App) initGlobalHotkey() error {
	if a.dc == nil || !a.dc.IsGlobalHotkeyEnabled() {
		return nil
	}

	if a.hotkeyRegisterHook != nil {
		if err := a.hotkeyRegisterHook(); err != nil {
			return err
		}
	} else if st := C.gcRegisterGlobalHotkey(); st != C.noErr {
		return fmt.Errorf("RegisterEventHotKey failed (Option+Cmd+G may be in use by another app): OSStatus %d", int(st))
	}
	debug.Log("desktop", "global hotkey registered: Option+Command+G")
	a.startHotkeyPoller()
	return nil
}

func (a *App) removeGlobalHotkey() {
	a.stopHotkeyPoller()
	C.gcUnregisterGlobalHotkey()
	debug.Log("desktop", "global hotkey unregistered")
}

func (a *App) toggleWindowViaHotkey() {
	if a.ctx == nil {
		return
	}
	debug.Log("desktop", "hotkey: toggling window")

	if a.lastCloseAttempt.Load() != nil {

		a.lastCloseAttempt.Store(nil)
		runtime.WindowShow(a.ctx)
		a.enqueueUIEvent("tray:show", nil)
	} else {

		now := time.Now()
		a.lastCloseAttempt.Store(&now)
		runtime.WindowHide(a.ctx)
		a.enqueueUIEvent("tray:hidden", nil)
	}
}
