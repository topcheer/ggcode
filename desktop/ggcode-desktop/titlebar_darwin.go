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
*/
import "C"

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

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
