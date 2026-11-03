//go:build darwin

package main

import "C"

func detectMacDarkMode() bool {
	return C.macDarkMode() == 1
}
