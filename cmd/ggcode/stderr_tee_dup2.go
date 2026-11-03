//go:build !windows && !(linux && (arm64 || riscv64 || loong64))

package main

import "syscall"

func dupTo(fd, targetFD int) error {
	return syscall.Dup2(fd, targetFD)
}
