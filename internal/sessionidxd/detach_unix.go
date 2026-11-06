//go:build !windows

package sessionidxd

import "syscall"

// detachAttr makes a self-launched daemon independent of the parent
// terminal/session (survives the client ggcode exiting).
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
