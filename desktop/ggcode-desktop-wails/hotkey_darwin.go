//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Carbon -framework Foundation

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>

// Volatile flag written by the Carbon hotkey callback and polled by Go.
static volatile int gcHotkeyFired = 0; // 0=none, 1=toggle

static EventHotKeyRef gcHotkeyRef = NULL;
static EventHandlerRef gcHotkeyHandler = NULL;

static OSStatus gcHotkeyCallback(EventHandlerCallRef next, EventRef evt, void *data) {
    gcHotkeyFired = 1;
    return noErr;
}

// Register Option+Command+G as a system-wide hotkey.
// Returns the first non-noErr OSStatus so callers can surface registration
// failure (#615) — e.g. another app exclusively owning the combo.
static OSStatus gcRegisterGlobalHotkey() {
    @autoreleasepool {
        EventTypeSpec spec;
        spec.eventClass = kEventClassKeyboard;
        spec.eventKind  = kEventHotKeyPressed;

        OSStatus st = InstallApplicationEventHandler(&gcHotkeyCallback, 1, &spec, NULL, &gcHotkeyHandler);
        if (st != noErr) return st;

        EventHotKeyID keyID;
        keyID.signature = 'GGCO';
        keyID.id = 1;

        // kVK_ANSI_G = 0x05, modifiers: cmdKey | optionKey
        st = RegisterEventHotKey(0x05, cmdKey | optionKey, keyID,
                                 GetApplicationEventTarget(), 0, &gcHotkeyRef);
        // #3008: if hotkey registration fails (e.g. -9866 key combo owned by
        // another app), roll back the handler we just installed - otherwise
        // the static gcHotkeyHandler ref gets overwritten on the next retry
        // and the leaked handler can never be removed.
        if (st != noErr) {
            if (gcHotkeyHandler) {
                RemoveEventHandler(gcHotkeyHandler);
                gcHotkeyHandler = NULL;
            }
        }
        return st;
    }
}

static void gcUnregisterGlobalHotkey() {
    if (gcHotkeyRef) {
        UnregisterEventHotKey(gcHotkeyRef);
        gcHotkeyRef = NULL;
    }
    if (gcHotkeyHandler) {
        RemoveEventHandler(gcHotkeyHandler);
        gcHotkeyHandler = NULL;
    }
    gcHotkeyFired = 0;
}

static int gcPollHotkey() {
    int v = gcHotkeyFired;
    gcHotkeyFired = 0;
    return v;
}
*/
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
