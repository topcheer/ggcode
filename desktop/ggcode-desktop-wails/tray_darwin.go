//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Foundation

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#import <dispatch/dispatch.h>

// We use a global bitmask that Obj-C sets (OR) and Go polls via a timer.
// This avoids the complexity of //export with Obj-C classes. The old plain
// store overwrote a pending action when two clicks landed inside one poll
// window — a lost Quit left the process running (#400). Bitmask OR means
// every click is retained until the poller drains it.
static volatile int gcTrayAction = 0; // bit flags: 1=show 2=newsession 4=quit

@interface GCTrayDelegate : NSObject
@end

@implementation GCTrayDelegate
- (void)onShowClick    { __sync_or_and_fetch(&gcTrayAction, 1); }
- (void)onNewSessionClick { __sync_or_and_fetch(&gcTrayAction, 2); }
- (void)onQuitClick    { __sync_or_and_fetch(&gcTrayAction, 4); }
@end

static NSStatusItem *gcStatusItem = nil;

// gcCreateTrayBody does the actual tray setup; must run on the main thread.
static void gcCreateTrayBody() {
    @autoreleasepool {
        GCTrayDelegate *delegate = [[GCTrayDelegate alloc] init];
        gcStatusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
        gcStatusItem.behavior = NSStatusItemBehaviorRemovalAllowed;

        NSImage *icon = [NSImage imageWithSystemSymbolName:@"terminal.fill" accessibilityDescription:@"GGCode"];
        if (icon != nil) {
            icon.template = YES;
            [icon setSize:NSMakeSize(18, 18)];
            gcStatusItem.button.image = icon;
        } else {
            NSImage *fb = [[NSImage alloc] initWithSize:NSMakeSize(18, 18)];
            [fb lockFocus];
            [[NSColor blackColor] setFill];
            NSBezierPath *p = [NSBezierPath bezierPathWithRoundedRect:NSMakeRect(2, 2, 14, 14) xRadius:3 yRadius:3];
            [p fill];
            [fb unlockFocus];
            fb.template = YES;
            gcStatusItem.button.image = fb;
        }

        NSMenu *menu = [[NSMenu alloc] init];
        menu.autoenablesItems = NO;

        NSMenuItem *showItem = [[NSMenuItem alloc] initWithTitle:@"Show GGCode" action:@selector(onShowClick) keyEquivalent:@""];
        showItem.target = delegate;
        [menu addItem:showItem];

        NSMenuItem *newItem = [[NSMenuItem alloc] initWithTitle:@"New Session" action:@selector(onNewSessionClick) keyEquivalent:@""];
        newItem.target = delegate;
        [menu addItem:newItem];

        [menu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:@"Quit GGCode" action:@selector(onQuitClick) keyEquivalent:@"q"];
        quitItem.target = delegate;
        [menu addItem:quitItem];

        objc_setAssociatedObject(gcStatusItem, @selector(menu), delegate, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
        gcStatusItem.menu = menu;
    }
}

// gcCreateTray dispatches tray creation to the main thread.
// NSStatusItem must be created on the main thread, but this function
// may be called from a Go goroutine.
static void gcCreateTray() {
    if ([NSThread isMainThread]) {
        gcCreateTrayBody();
    } else {
        dispatch_async(dispatch_get_main_queue(), ^{
            gcCreateTrayBody();
        });
    }
}

// gcRemoveTrayBody does the actual tray cleanup; must run on the main thread.
static void gcRemoveTrayBody() {
    if (gcStatusItem) {
        [[NSStatusBar systemStatusBar] removeStatusItem:gcStatusItem];
        gcStatusItem = nil;
    }
    gcTrayAction = 0;
}

// gcRemoveTray dispatches tray removal to the main thread.
static void gcRemoveTray() {
    if ([NSThread isMainThread]) {
        gcRemoveTrayBody();
    } else {
        dispatch_async(dispatch_get_main_queue(), ^{
            gcRemoveTrayBody();
        });
    }
}

static int gcPollTrayAction() {
    // Atomically drain the whole bitmask; the OR-setters never lose a click.
    return __sync_fetch_and_or(&gcTrayAction, 0) == 0 ? 0 : __sync_lock_test_and_set(&gcTrayAction, 0);
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

func (a *App) initSystemTray() {
	C.gcCreateTray()
	// Start a lightweight poller goroutine that checks for tray menu clicks.
	// We use a 200ms interval which is responsive enough for menu actions
	// without any measurable CPU impact.
	safego.Go("tray-poller", func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				// Bitmask drain (#400): multiple clicks may have accumulated
				// within one poll window; handle EVERY set bit so a Quit is
				// never dropped because a Show arrived first.
				action := int(C.gcPollTrayAction())
				if action == 0 {
					continue
				}
				if action&1 != 0 {
					a.handleTrayShow()
				}
				if action&2 != 0 {
					a.handleTrayNewSession()
				}
				if action&4 != 0 {
					a.handleTrayQuit()
				}
			}
		}
	})
}

func (a *App) removeSystemTray() {
	C.gcRemoveTray()
}

func (a *App) handleTrayShow() {
	debug.Log("desktop", "tray: show window")
	a.lastCloseAttempt = nil
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	a.enqueueUIEvent("tray:show", nil)
}

func (a *App) handleTrayNewSession() {
	debug.Log("desktop", "tray: new session")
	a.lastCloseAttempt = nil
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
