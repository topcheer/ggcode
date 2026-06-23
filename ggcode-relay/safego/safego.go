// Package safego provides goroutine launchers with built-in panic recovery.
//
// A panic in any goroutine that is not handled within that goroutine will
// crash the entire process. Wrapping each goroutine in safego.Go ensures
// that bugs in one subsystem don't take the whole relay server down.
package safego

import (
	"log"
	"runtime/debug"
)

// Go launches fn in a new goroutine with panic recovery. The name is used
// only in log output to identify the goroutine in case of a panic.
func Go(name string, fn func()) {
	go func() {
		defer recoverPanic(name)
		fn()
	}()
}

func recoverPanic(name string) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	log.Printf("[relay-safego] PANIC in goroutine %q: %v\n%s", name, r, stack)
}
