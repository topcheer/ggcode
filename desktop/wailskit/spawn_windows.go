//go:build windows

package wailskit

import "syscall"

// noWindowSysProcAttr returns SysProcAttr for spawning console children from
// the GUI-subsystem desktop process without flashing a terminal window
// (CREATE_NO_WINDOW).
func noWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x08000000}
}
