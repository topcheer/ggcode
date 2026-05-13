package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

func logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[desktop] "+format+"\n", args...)
}

func logPanic(context string) {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "[desktop] PANIC in %s: %v\n%s", context, r, debug.Stack())
	}
}
