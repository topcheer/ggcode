//go:build !unix

package tui

import "context"

func enableBubbleteaTrace()                              {}
func drainStdinResidual()                                {}
func startTTYWatchdog(ctx context.Context) (stop func()) { return func() {} }
