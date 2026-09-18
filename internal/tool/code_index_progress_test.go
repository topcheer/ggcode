package tool

import (
	"sync"
	"testing"
)

// SetOnProgress registers without racing and the callback is stored.
// (The throttled firing itself is exercised via doBuild integration in
// the REPL; here we pin the registration contract.)
func TestSetOnProgressRegistration(t *testing.T) {
	m := NewCodeIndexManager(t.TempDir())
	var mu sync.Mutex
	called := 0
	m.SetOnProgress(func(done, total int) {
		mu.Lock()
		called++
		mu.Unlock()
	})
	if m.onProgress == nil {
		t.Fatalf("onProgress must be stored")
	}
	// Fire it directly (doBuild calls it on its own goroutine).
	m.onProgress(1, 2)
	mu.Lock()
	defer mu.Unlock()
	if called != 1 {
		t.Fatalf("callback not invoked, called=%d", called)
	}
}
