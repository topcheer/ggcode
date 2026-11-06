//go:build windows

package sessionidxd

import "syscall"

// detachAttr: on Windows the daemon v1 is not wired (client falls back to
// local Manager); keep a compilable stub.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
