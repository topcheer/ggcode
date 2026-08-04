//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation

#import <Foundation/Foundation.h>

// Returns 1 if macOS Dark Mode is enabled, 0 otherwise.
// Falls back to 0 (light) if the key is not set (older macOS versions).
static int macDarkMode() {
    @autoreleasepool {
        NSString *style = [[NSUserDefaults standardUserDefaults] stringForKey:@"AppleInterfaceStyle"];
        if (style != nil && [style.lowercaseString containsString:@"dark"]) {
            return 1;
        }
        return 0;
    }
}
*/
import "C"

// detectMacDarkMode returns true if macOS is in Dark Mode.
func detectMacDarkMode() bool {
	return C.macDarkMode() == 1
}
