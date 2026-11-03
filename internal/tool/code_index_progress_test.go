package tool

import (
	"sync"
	"testing"
)

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

	m.onProgress(1, 2)
	mu.Lock()
	defer mu.Unlock()
	if called != 1 {
		t.Fatalf("callback not invoked, called=%d", called)
	}
}
