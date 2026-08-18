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
        return RegisterEventHotKey(0x05, cmdKey | optionKey, keyID,
                                   GetApplicationEventTarget(), 0, &gcHotkeyRef);
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

// hotkeyPollerMu guards the single hotkey poller's stop channel (#615:
// every enable used to spawn one more 200ms poller goroutine that only
// exited on app shutdown; repeated toggles leaked goroutines and made
// several pollers race on the atomic gcHotkeyFired flag, so a hotkey press
// was occasionally swallowed).
var (
	hotkeyPollerMu sync.Mutex
	hotkeyStop     chan struct{}
)

// hotkeyPollerRunning reports whether the single poller goroutine is
// registered (test hook for #615; synchronous under hotkeyPollerMu).
func hotkeyPollerRunning() bool {
	hotkeyPollerMu.Lock()
	defer hotkeyPollerMu.Unlock()
	return hotkeyStop != nil
}

// startHotkeyPoller ensures exactly one poller goroutine runs; a second
// call while it is alive is a no-op.
func (a *App) startHotkeyPoller() {
	hotkeyPollerMu.Lock()
	defer hotkeyPollerMu.Unlock()
	if hotkeyStop != nil {
		return // already running — reuse instead of stacking another poller
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
				// App shutting down: clear the registration so a later
				// re-enable (new app context) can start a fresh poller.
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

// stopHotkeyPoller stops the poller goroutine if one is running.
func (a *App) stopHotkeyPoller() {
	hotkeyPollerMu.Lock()
	defer hotkeyPollerMu.Unlock()
	if hotkeyStop != nil {
		close(hotkeyStop)
		hotkeyStop = nil
	}
}

// initGlobalHotkey registers a system-wide hotkey (Option+Command+G)
// to toggle the window visibility from anywhere. Returns an error when the
// OS rejects the registration (e.g. the combo is exclusively owned by
// another application) instead of silently pretending success (#615).
func (a *App) initGlobalHotkey() error {
	if a.dc == nil || !a.dc.IsGlobalHotkeyEnabled() {
		return nil
	}
	// #615 test hook: replaces only the OS registration call, so the poller
	// lifecycle below stays exercised. The real RegisterEventHotKey can fail
	// on machines where another app owns the combo (OSStatus -9866).
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

// removeGlobalHotkey unregisters the system-wide hotkey and stops its
// poller goroutine so repeated toggles do not leak one poller per enable
// (#615).
func (a *App) removeGlobalHotkey() {
	a.stopHotkeyPoller()
	C.gcUnregisterGlobalHotkey()
	debug.Log("desktop", "global hotkey unregistered")
}

// toggleWindowViaHotkey shows or hides the window depending on current state.
func (a *App) toggleWindowViaHotkey() {
	if a.ctx == nil {
		return
	}
	debug.Log("desktop", "hotkey: toggling window")
	// Wails v2 does not expose window visibility query, so we track state
	// ourselves. If the window was hidden by close-to-tray or hotkey, show it.
	// Otherwise hide it (user is actively working in the app).
	if a.lastCloseAttempt.Load() != nil {
		// Window was hidden to tray — show it
		a.lastCloseAttempt.Store(nil)
		runtime.WindowShow(a.ctx)
		a.enqueueUIEvent("tray:show", nil)
	} else {
		// Window is visible — hide it
		now := time.Now()
		a.lastCloseAttempt.Store(&now) // #700: atomic (4 goroutines touch this)
		runtime.WindowHide(a.ctx)
		a.enqueueUIEvent("tray:hidden", nil)
	}
}
