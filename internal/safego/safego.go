package safego

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"
)

var logFn func(category, format string, args ...any)

func SetLogger(fn func(category, format string, args ...any)) {
	logFn = fn
}

var (
	panicHookMu sync.RWMutex
	panicHook   func(name string, recovered any, stack []byte)
)

func SetPanicHook(fn func(name string, recovered any, stack []byte)) {
	panicHookMu.Lock()
	panicHook = fn
	panicHookMu.Unlock()
}

func GetPanicHook() func(name string, recovered any, stack []byte) {
	panicHookMu.RLock()
	defer panicHookMu.RUnlock()
	return panicHook
}

func Go(name string, fn func()) {
	go Run(name, fn)
}

func Run(name string, fn func()) {
	defer Recover(name)
	fn()
}

func Recover(name string) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	// PANIC is fatal-class: always mirror to stderr so the failure is
	// visible even when debug logging is disabled or its sinks are nil
	// (silent-swallow defect: a recovered panic that nobody can see turns
	// into a "3s timeout" mystery at the call site - #5060 family).
	fmt.Fprintf(os.Stderr, "[%s] [safego] PANIC in goroutine %q: %v\n%s\n",
		time.Now().UTC().Format(time.RFC3339), name, r, stack)
	if logFn != nil {
		logFn("safego", "PANIC in goroutine %q: %v\n%s", name, r, stack)
	}
	if hook := GetPanicHook(); hook != nil {

		func() {
			defer func() {
				if r2 := recover(); r2 != nil {
					fmt.Fprintf(os.Stderr, "[safego] PANIC in PanicHook for %q: %v\n", name, r2)
					if logFn != nil {
						logFn("safego", "PANIC in PanicHook for %q: %v", name, r2)
					}
				}
			}()
			hook(name, r, stack)
		}()
	}
}
