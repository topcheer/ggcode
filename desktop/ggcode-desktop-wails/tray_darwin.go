//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Foundation

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// Dock icon click -> show window. Wails v2.13 does not implement
// applicationShouldHandleReopen, so after WindowHide the Dock icon stops
// working. We add the method to the delegate's real class at runtime; it
// sets the same action bit the tray poller drains (bit 1 = show).
static volatile int gcDockAction = 0; // bit flags: 1=show

static BOOL gcReopenIMP(id self, SEL _cmd, NSApplication *sender, BOOL flag) {
    (void)self; (void)_cmd; (void)sender; (void)flag;
    gcDockAction = 1;
    return YES;
}
static void gcInstallDockReopenHandler() {
    id dlg = [NSApp delegate];
    if (dlg == nil) { return; }
    SEL sel = @selector(applicationShouldHandleReopen:hasVisibleWindows:);
    if ([dlg respondsToSelector:sel]) { return; } // wails handles it (or a future upgrade)
    Class c = object_getClass(dlg);
    class_addMethod(c, sel, (IMP)gcReopenIMP, "B@:@B");
}
static int gcPollDockAction() {
    return __sync_lock_test_and_set(&gcDockAction, 0);
}
*/
import "C"

import (
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// initSystemTray installs the Dock-reopen handler and its poller. The tray
// itself is cross-platform now (tray.go, energye/systray).
func (a *App) initSystemTray() {
	C.gcInstallDockReopenHandler()
	safego.Go("dock-reopen-poller", func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		if a.ctx == nil {
			return
		}
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				if C.gcPollDockAction() != 0 {
					a.handleTrayShow()
				}
			}
		}
	})
}

func (a *App) handleTrayShow() {
	debug.Log("desktop", "tray: show window")
	a.lastCloseAttempt.Store(nil) // #700: atomic (4 goroutines touch this)
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	a.enqueueUIEvent("tray:show", nil)
}

func (a *App) handleTrayNewSession() {
	debug.Log("desktop", "tray: new session")
	a.lastCloseAttempt.Store(nil) // #700: atomic (4 goroutines touch this)
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
