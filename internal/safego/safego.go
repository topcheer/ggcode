package safego

import (
	"runtime/debug"
	"sync"
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
	if logFn != nil {
		logFn("safego", "PANIC in goroutine %q: %v\n%s", name, r, stack)
	}
	if hook := GetPanicHook(); hook != nil {

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
