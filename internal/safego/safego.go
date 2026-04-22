// Package safego provides goroutine launchers with built-in panic recovery.
//
// A panic in any goroutine that is not handled within that goroutine will
// crash the entire process, which for a long-running interactive TUI is the
// worst possible failure mode (the user loses unsaved input, in-flight LLM
// streams, IM connections, etc.). Wrapping each goroutine in safego.Go
// ensures that bugs in one subsystem don't take the whole agent down.
//
// The recovered panic is logged via internal/debug, including a full stack
// trace, so the bug remains diagnosable.
package safego

import (
	"runtime/debug"

	internaldebug "github.com/topcheer/ggcode/internal/debug"
)

// PanicHook, when non-nil, is invoked once with the recovered value and the
// full stack trace each time Go() catches a panic. It runs synchronously in
// the recovering goroutine. The TUI can install a hook to surface a
// non-fatal error message to the user; servers can install a hook to ship
// the stack to a sink. It must not panic.
var PanicHook func(name string, recovered any, stack []byte)

// Go launches fn in a new goroutine with panic recovery. The name is used
// only in log output to identify the goroutine in case of a panic.
//
// Use this for any goroutine whose failure should not be allowed to crash
// the process: stream readers, batch flushers, IM listeners, background
// fetchers, watchdogs, etc.
func Go(name string, fn func()) {
	go Run(name, fn)
}

// Run invokes fn synchronously in the current goroutine with panic
// recovery. Useful when wrapping the body of a goroutine you've already
// launched yourself, or when running code in main that must not crash.
func Run(name string, fn func()) {
	defer Recover(name)
	fn()
}

// Recover is a deferred-friendly helper. Call as `defer safego.Recover("name")`
// at the top of any goroutine body you want to protect. It logs the panic
// via internal/debug (with full stack) and invokes PanicHook if set, then
// swallows the panic so the goroutine exits cleanly.
func Recover(name string) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	internaldebug.Log("safego", "PANIC in goroutine %q: %v\n%s", name, r, stack)
	if hook := PanicHook; hook != nil {
		// Guard the hook itself against panics so a buggy hook can't
		// re-trigger the very crash safego is meant to prevent.
		func() {
			defer func() {
				if r2 := recover(); r2 != nil {
					internaldebug.Log("safego", "PANIC in PanicHook for %q: %v", name, r2)
				}
			}()
			hook(name, r, stack)
		}()
	}
}
