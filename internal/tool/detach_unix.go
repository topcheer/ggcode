//go:build !windows

package tool

import "syscall"

// detachSysProcAttr returns a SysProcAttr that starts the child in a new session
// so it survives independent of the ggcode process.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
