//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

static const NSInteger GGTitlebarLabelTag = 0x67674364;
static NSString* GGTitlebarBackgroundIdentifier = @"ggcode-titlebar-background";

static void configureUnifiedTitlebar(unsigned long long nsWindowPtr) {
	NSWindow* win = (NSWindow*)nsWindowPtr;
	[win setAppearance:[NSAppearance appearanceNamed:NSAppearanceNameDarkAqua]];
	[win setTitleVisibility:NSWindowTitleHidden];
	[win setTitlebarAppearsTransparent:NO];
	if ([win respondsToSelector:@selector(setToolbarStyle:)]) {
		[win setToolbarStyle:NSWindowToolbarStyleUnifiedCompact];
	}
	if ([win respondsToSelector:@selector(setTitlebarSeparatorStyle:)]) {
		[win setTitlebarSeparatorStyle:NSTitlebarSeparatorStyleNone];
	}
}

static NSView* ensureTitlebarBackground(NSWindow* win) {
	NSButton* close = [win standardWindowButton:NSWindowCloseButton];
	if (close == nil) {
		return nil;
	}
	NSView* titlebarView = [close superview];
	if (titlebarView == nil) {
		return nil;
	}
	NSView* background = nil;
	for (NSView* child in [titlebarView subviews]) {
		if ([[child identifier] isEqualToString:GGTitlebarBackgroundIdentifier]) {
			background = child;
			break;
		}
	}
	if (background == nil) {
		background = [[NSView alloc] initWithFrame:[titlebarView bounds]];
		[background setIdentifier:GGTitlebarBackgroundIdentifier];
		[background setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
		[background setWantsLayer:YES];
		[titlebarView addSubview:background positioned:NSWindowBelow relativeTo:nil];
		[background release];
	}
	[background setFrame:[titlebarView bounds]];
	return background;
}

static NSTextField* ensureTitlebarLabel(NSWindow* win) {
	NSButton* close = [win standardWindowButton:NSWindowCloseButton];
	if (close == nil) {
		return nil;
	}
	NSView* titlebarView = [close superview];
	if (titlebarView == nil) {
		return nil;
	}
	NSTextField* label = (NSTextField*)[titlebarView viewWithTag:GGTitlebarLabelTag];
	if (label == nil) {
		label = [NSTextField labelWithString:@""];
		[label setTag:GGTitlebarLabelTag];
		[label setAlignment:NSTextAlignmentLeft];
		[label setFont:[NSFont systemFontOfSize:14 weight:NSFontWeightSemibold]];
		[label setLineBreakMode:NSLineBreakByTruncatingTail];
		[label setAutoresizingMask:NSViewWidthSizable | NSViewMinYMargin | NSViewMaxYMargin];
		[titlebarView addSubview:label];
	}
	NSRect bounds = [titlebarView bounds];
	CGFloat leading = 78.0;
	CGFloat trailing = 110.0;
	CGFloat width = bounds.size.width - leading - trailing;
	if (width < 140.0) {
		width = 140.0;
	}
	CGFloat height = 22.0;
	CGFloat y = floor((bounds.size.height - height) / 2.0);
	if (y < 0) {
		y = 0;
	}
	[label setFrame:NSMakeRect(leading, y, width, height)];
	return label;
}

static void setUnifiedTitlebarAppearance(unsigned long long nsWindowPtr,
		double bgR, double bgG, double bgB, double bgA,
		double fgR, double fgG, double fgB, double fgA) {
	NSWindow* win = (NSWindow*)nsWindowPtr;
	NSView* background = ensureTitlebarBackground(win);
	NSTextField* label = ensureTitlebarLabel(win);
	NSColor* bgColor = [NSColor colorWithSRGBRed:bgR green:bgG blue:bgB alpha:bgA];
	NSColor* fgColor = [NSColor colorWithSRGBRed:fgR green:fgG blue:fgB alpha:fgA];
	if (background != nil && [background layer] != nil) {
		[[background layer] setBackgroundColor:[bgColor CGColor]];
	}
	if (label != nil) {
		[label setTextColor:fgColor];
	}
	[win setBackgroundColor:bgColor];
}

static void setUnifiedTitlebarText(unsigned long long nsWindowPtr, const char* title) {
	NSWindow* win = (NSWindow*)nsWindowPtr;
	NSTextField* label = ensureTitlebarLabel(win);
	if (label == nil) {
		return;
	}
	NSString* text = @"";
	if (title != NULL) {
		text = [NSString stringWithUTF8String:title];
		if (text == nil) {
			text = @"";
		}
	}
	[label setStringValue:text];
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
	"image/color"
	"unsafe"
)

func setDockIconMac(path string) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	C.setAppDockIcon(cpath)
}

func setupNativeTitlebar(w fyne.Window) nativeTitlebarConfig {
	nw, ok := w.(driver.NativeWindow)
	if !ok {
		return nativeTitlebarConfig{}
	}
	nw.RunNative(func(ctx any) {
		mac, ok := ctx.(driver.MacWindowContext)
		if !ok {
			return
		}
		C.configureUnifiedTitlebar(C.ulonglong(mac.NSWindow))
	})
	return nativeTitlebarConfig{}
}

func updateNativeTitlebarAppearance(w fyne.Window, bg, fg color.NRGBA) {
	nw, ok := w.(driver.NativeWindow)
	if !ok {
		return
	}
	nw.RunNative(func(ctx any) {
		mac, ok := ctx.(driver.MacWindowContext)
		if !ok {
			return
		}
		C.setUnifiedTitlebarAppearance(
			C.ulonglong(mac.NSWindow),
			C.double(float64(bg.R)/255.0), C.double(float64(bg.G)/255.0), C.double(float64(bg.B)/255.0), C.double(float64(bg.A)/255.0),
			C.double(float64(fg.R)/255.0), C.double(float64(fg.G)/255.0), C.double(float64(fg.B)/255.0), C.double(float64(fg.A)/255.0),
		)
	})
}

func updateNativeWindowTitle(w fyne.Window, title string) {
	nw, ok := w.(driver.NativeWindow)
	if !ok {
		return
	}
	nw.RunNative(func(ctx any) {
		mac, ok := ctx.(driver.MacWindowContext)
		if !ok {
			return
		}
		ctitle := C.CString(title)
		defer C.free(unsafe.Pointer(ctitle))
		C.setUnifiedTitlebarText(C.ulonglong(mac.NSWindow), ctitle)
	})
}
