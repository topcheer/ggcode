//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Foundation

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

// We use a global int that Obj-C writes and Go polls via a timer.
// This avoids the complexity of //export with Obj-C classes.
static volatile int gcTrayAction = 0; // 0=none, 1=show, 2=newsession, 3=quit

@interface GCTrayDelegate : NSObject
@end

@implementation GCTrayDelegate
- (void)onShowClick    { gcTrayAction = 1; }
- (void)onNewSessionClick { gcTrayAction = 2; }
- (void)onQuitClick    { gcTrayAction = 3; }
@end

static NSStatusItem *gcStatusItem = nil;

static void gcCreateTray() {
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

static void gcRemoveTray() {
    if (gcStatusItem) {
        [[NSStatusBar systemStatusBar] removeStatusItem:gcStatusItem];
        gcStatusItem = nil;
    }
    gcTrayAction = 0;
}

static int gcPollTrayAction() {
    int a = gcTrayAction;
    gcTrayAction = 0;
    return a;
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
				action := int(C.gcPollTrayAction())
				if action == 0 {
					continue
				}
				switch action {
				case 1:
					a.handleTrayShow()
				case 2:
					a.handleTrayNewSession()
				case 3:
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
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	a.enqueueUIEvent("tray:show", nil)
}

func (a *App) handleTrayNewSession() {
	debug.Log("desktop", "tray: new session")
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
