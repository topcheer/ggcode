package safego

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// stderrSink is atomically swappable for tests; production always writes
// to os.Stderr. atomic access keeps panic-path writes race-free even when
// a test swaps the sink concurrently.
var stderrSink atomic.Pointer[io.Writer]

func init() {
	w := io.Writer(os.Stderr)
	stderrSink.Store(&w)
}

func stderrWrite(format string, args ...any) {
	if w := stderrSink.Load(); w != nil {
		fmt.Fprintf(*w, format, args...)
	}
}

var logSink atomic.Pointer[func(category, format string, args ...any)]

func SetLogger(fn func(category, format string, args ...any)) {
	logSink.Store(&fn)
}

func logger() func(category, format string, args ...any) {
	if p := logSink.Load(); p != nil {
		return *p
	}
	return nil
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
	stderrWrite("[%s] [safego] PANIC in goroutine %q: %v\n%s\n",
		time.Now().UTC().Format(time.RFC3339), name, r, stack)
	if l := logger(); l != nil {
		l("safego", "PANIC in goroutine %q: %v\n%s", name, r, stack)
	}
	if hook := GetPanicHook(); hook != nil {

		func() {
			defer func() {
				if r2 := recover(); r2 != nil {
					stderrWrite("[safego] PANIC in PanicHook for %q: %v\n", name, r2)
					if l := logger(); l != nil {
						l("safego", "PANIC in PanicHook for %q: %v", name, r2)
					}
				}
			}()
			hook(name, r, stack)
		}()
	}
}
