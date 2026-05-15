//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

static void setDarkAppearance(unsigned long long nsWindowPtr) {
	NSWindow* win = (NSWindow*)nsWindowPtr;
	[win setAppearance:[NSAppearance appearanceNamed:NSAppearanceNameDarkAqua]];
}

static void setAppDockIcon(const char* path) {
	NSImage* img = [[NSImage alloc] initWithContentsOfFile:[NSString stringWithUTF8String:path]];
	if (img) {
		[NSApp setApplicationIconImage:img];
		[img release];
	}
}
*/
import "C"

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

func setDockIconMac(path string) {
	C.setAppDockIcon(C.CString(path))
}

func setupNativeTitlebar(w fyne.Window) {
	nw, ok := w.(driver.NativeWindow)
	if !ok {
		return
	}
	nw.RunNative(func(ctx any) {
		mac, ok := ctx.(driver.MacWindowContext)
		if !ok {
			return
		}
		C.setDarkAppearance(C.ulonglong(mac.NSWindow))
	})
}
