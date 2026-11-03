package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestRunStartSetsElapsedOrigin(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge unavailable in test env: %v", err)
	}

	probe := func(name string, fn func()) {

		idle := time.Now().Add(10 * time.Second)
		for time.Now().Before(idle) && b.IsWorking() {
			time.Sleep(10 * time.Millisecond)
		}
		b.mu.Lock()
		b.startTime = time.Time{}
		b.mu.Unlock()
		before := time.Now()

		done := make(chan struct{})
		go func() {
			defer close(done)
			fn()
		}()

		deadline := time.Now().Add(5 * time.Second)
		observed := false
		for time.Now().Before(deadline) {
			b.mu.Lock()
			st := b.startTime
			b.mu.Unlock()
			if st.After(before) {
				if b.IsWorking() {
					if el := b.Elapsed(); el < 0 || el > 10*time.Second {
						t.Fatalf("%s: Elapsed() = %v, want a small sane duration from this run's origin", name, el)
					}
				}
				observed = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !observed {
			t.Fatalf("%s: run-start never set startTime (stale zero value or old origin) within deadline", name)
		}

		b.Cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Logf("%s goroutine still draining after Cancel; not blocking the suite", name)
		}
	}

	probe("hidden-run", func() { _ = b.SendHiddenText("probe") })
	probe("text-run", func() { _ = b.sendMessageData(tunnel.MessageData{Text: "probe"}, "desktop", "") })
}
