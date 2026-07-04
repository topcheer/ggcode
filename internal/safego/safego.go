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
	"sync"
)

// logFn is set by the debug package at init time to avoid a circular import.
// Before it is set, panic recovery still works but nothing is logged.
var logFn func(category, format string, args ...any)

// SetLogger installs the debug log function. Called once at startup by
// internal/debug to wire up the dependency without creating an import cycle.
func SetLogger(fn func(category, format string, args ...any)) {
	logFn = fn
}

// PanicHook, when non-nil, is invoked once with the recovered value and the
// full stack trace each time Go() catches a panic. It runs synchronously in
// the recovering goroutine. The TUI can install a hook to surface a
// non-fatal error message to the user; servers can install a hook to ship
// the stack to a sink. It must not panic.
//
// Access is protected by panicHookMu to prevent data races between goroutines
// calling Recover() and callers mutating the hook (e.g. tests).
var (
	panicHookMu sync.RWMutex
	panicHook   func(name string, recovered any, stack []byte)
)

// SetPanicHook sets the global panic hook. Pass nil to clear it.
func SetPanicHook(fn func(name string, recovered any, stack []byte)) {
	panicHookMu.Lock()
	panicHook = fn
	panicHookMu.Unlock()
}

// GetPanicHook returns the current panic hook (may be nil).
func GetPanicHook() func(name string, recovered any, stack []byte) {
	panicHookMu.RLock()
	defer panicHookMu.RUnlock()
	return panicHook
}

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
	if logFn != nil {
		logFn("safego", "PANIC in goroutine %q: %v\n%s", name, r, stack)
	}
	if hook := GetPanicHook(); hook != nil {
		// Guard the hook itself against panics so a buggy hook can't
		// re-trigger the very crash safego is meant to prevent.
		func() {
			defer func() {
				if r2 := recover(); r2 != nil {
					if logFn != nil {
						logFn("safego", "PANIC in PanicHook for %q: %v", name, r2)
					}
				}
			}()
			hook(name, r, stack)
		}()
	}
}
