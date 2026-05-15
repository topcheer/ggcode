//go:build windows

package main

/*
#cgo LDFLAGS: -lgdi32 -luser32 -ldwmapi

#include <windows.h>
#include <dwmapi.h>

static void setDarkTitlebar(unsigned long long hwnd) {
	HWND win = (HWND)hwnd;
	// Windows 10 1809+ / Windows 11: DWMWA_USE_IMMERSIVE_DARK_MODE
	BOOL value = TRUE;
	// Attribute 20 (DWMWA_USE_IMMERSIVE_DARK_MODE before Win11 22H2)
	DwmSetWindowAttribute(win, 20, &value, sizeof(value));
	// Attribute 38 (DWMWA_SYSTEMBACKDROP_TYPE for Win11 22H2+)
	// value = 1; // Mica
	// DwmSetWindowAttribute(win, 38, &value, sizeof(value));
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
		win, ok := ctx.(driver.WindowsWindowContext)
		if !ok {
			return
		}
		C.setDarkTitlebar(C.ulonglong(win.HWND))
	})
}

func setDockIconMac(path string) {}
