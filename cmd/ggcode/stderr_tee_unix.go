//go:build !windows

package main

import (
	"os"
	"sync"
	"syscall"

	"github.com/topcheer/ggcode/internal/debug"
)

func teeStderrFD(w *os.File) (restore func(), ok bool) {
	origFD, err := syscall.Dup(2)
	if err != nil {
		return nil, false
	}
	if err := dupTo(int(w.Fd()), 2); err != nil {
		_ = syscall.Close(origFD)
		return nil, false
	}
	var once sync.Once
	return func() {
		once.Do(func() {

			if err := dupTo(origFD, 2); err != nil {
				debug.Log("stderr-tee", "restore dupTo failed (%v) - keeping spare fd %d open; stderr still teed", err, origFD)
				return
			}
			_ = syscall.Close(origFD)
		})
	}, true
}
