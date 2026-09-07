//go:build !windows && !(linux && arm64)

package main

import "syscall"

// dupTo duplicates fd onto targetFD on platforms where the dup2 syscall
// exists (darwin, linux/amd64, ...). See stderr_tee_dup3.go for the
// linux/arm64 counterpart.
func dupTo(fd, targetFD int) error {
	return syscall.Dup2(fd, targetFD)
}
