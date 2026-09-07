//go:build linux && arm64

package main

import "syscall"

// dupTo duplicates fd onto targetFD. linux/arm64 has no dup2 syscall (the
// kernel only exposes dup3), and Go's syscall package mirrors that ABI, so
// arm64 builds must route through Dup3 with empty flags - semantically
// identical to Dup2. Without this split, cross-compiling linux/arm64 fails
// with "undefined: syscall.Dup2" (caught by the v1.3.233 release build).
func dupTo(fd, targetFD int) error {
	return syscall.Dup3(fd, targetFD, 0)
}
