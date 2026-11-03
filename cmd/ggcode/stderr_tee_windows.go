//go:build windows

package main

import "os"

func teeStderrFD(w *os.File) (restore func(), ok bool) {
	return nil, false
}
