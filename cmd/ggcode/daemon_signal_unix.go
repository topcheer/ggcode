//go:build unix

package main

import (
	"syscall"
)

func selfSignal(sig syscall.Signal) {
	syscall.Kill(syscall.Getpid(), sig)
}
