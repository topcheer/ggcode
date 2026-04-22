package safego

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunRecoversPanic(t *testing.T) {
	// Should not propagate the panic to the test goroutine.
	Run("test-panic", func() {
		panic("boom")
	})
}

func TestGoRecoversPanic(t *testing.T) {
	done := make(chan struct{})
	Go("test-go-panic", func() {
		defer close(done)
		panic(errors.New("boom"))
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goroutine did not finish")
	}
}

func TestRecoverDefer(t *testing.T) {
	func() {
		defer Recover("inline")
		panic("inline boom")
	}()
}

func TestPanicHookFires(t *testing.T) {
	var calls atomic.Int32
	var (
		mu       sync.Mutex
		gotName  string
		gotValue any
	)
	old := PanicHook
	PanicHook = func(name string, recovered any, stack []byte) {
		calls.Add(1)
		mu.Lock()
		gotName = name
		gotValue = recovered
		mu.Unlock()
	}
	defer func() { PanicHook = old }()
	Run("hook-test", func() { panic("hooked") })
	if calls.Load() != 1 {
		t.Fatalf("hook calls = %d, want 1", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if gotName != "hook-test" {
		t.Fatalf("hook name = %q, want %q", gotName, "hook-test")
	}
	if gotValue != "hooked" {
		t.Fatalf("hook recovered = %v, want %q", gotValue, "hooked")
	}
}

func TestPanicHookPanicSwallowed(t *testing.T) {
	old := PanicHook
	PanicHook = func(name string, recovered any, stack []byte) { panic("hook boom") }
	defer func() { PanicHook = old }()
	Run("hook-panic-test", func() { panic("inner") })
}

func TestRunNoPanic(t *testing.T) {
	called := false
	Run("ok", func() { called = true })
	if !called {
		t.Fatal("fn not called")
	}
}
