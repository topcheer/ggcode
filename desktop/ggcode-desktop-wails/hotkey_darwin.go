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
static void gcRegisterGlobalHotkey() {
    @autoreleasepool {
        EventTypeSpec spec;
        spec.eventClass = kEventClassKeyboard;
        spec.eventKind  = kEventHotKeyPressed;

        InstallApplicationEventHandler(&gcHotkeyCallback, 1, &spec, NULL, &gcHotkeyHandler);

        EventHotKeyID keyID;
        keyID.signature = 'GGCO';
        keyID.id = 1;

        // kVK_ANSI_G = 0x05, modifiers: cmdKey | optionKey
        RegisterEventHotKey(0x05, cmdKey | optionKey, keyID,
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
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// initGlobalHotkey registers a system-wide hotkey (Option+Command+G)
// to toggle the window visibility from anywhere.
func (a *App) initGlobalHotkey() {
	if a.dc == nil || !a.dc.IsGlobalHotkeyEnabled() {
		return
	}
	C.gcRegisterGlobalHotkey()
	debug.Log("desktop", "global hotkey registered: Option+Command+G")

	safego.Go("hotkey-poller", func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				if C.gcPollHotkey() != 0 {
					a.toggleWindowViaHotkey()
				}
			}
		}
	})
}

// removeGlobalHotkey unregisters the system-wide hotkey.
func (a *App) removeGlobalHotkey() {
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
	if a.lastCloseAttempt != nil {
		// Window was hidden to tray — show it
		a.lastCloseAttempt = nil
		runtime.WindowShow(a.ctx)
		a.enqueueUIEvent("tray:show", nil)
	} else {
		// Window is visible — hide it
		now := time.Now()
		a.lastCloseAttempt = &now
		runtime.WindowHide(a.ctx)
		a.enqueueUIEvent("tray:hidden", nil)
	}
}
