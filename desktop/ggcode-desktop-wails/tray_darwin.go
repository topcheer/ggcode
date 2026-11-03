//go:build darwin

package main

import "C"

import (
	"time"

	"github.com/topcheer/ggcode/internal/safego"
)

func (a *App) initSystemTray() {
	C.gcInstallDockReopenHandler()
	safego.Go("dock-reopen-poller", func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		if a.ctx == nil {
			return
		}
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				if C.gcPollDockAction() != 0 {
					a.handleTrayShow()
				}
			}
		}
	})
}
