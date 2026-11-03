//go:build !windows

package wailskit

import "syscall"

func noWindowSysProcAttr() *syscall.SysProcAttr {
	return nil
}
