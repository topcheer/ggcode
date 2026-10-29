package wailskit

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// TestRunStartSetsElapsedOrigin (#4186): every run entry - sendMessageData
// (plain-text main path) and SendHiddenText (hidden path) - must set
// b.startTime in the run-start critical section, not just SendContent.
// Previously startTime was SendContent-only, so Elapsed() returned durations
// measured from the zero time (~63.8 billion seconds) on text/hidden runs, or
// a stale origin from an earlier image-paste run.
//
// The probe observes the run-start critical section only (same pattern as
// zz_issue522_test.go): the call runs in a goroutine, startTime freshness is
// asserted as soon as the run is observable via Elapsed()/IsRunning(), then
// Cancel() unwinds the run so the test stays bounded.
func TestRunStartSetsElapsedOrigin(t *testing.T) {
	b, err := NewChatBridge()
	if err != nil {
		t.Skipf("bridge unavailable in test env: %v", err)
	}

	probe := func(name string, fn func()) {
		// Precondition: bridge idle (no concurrent writer to startTime).
		idle := time.Now().Add(10 * time.Second)
		for time.Now().Before(idle) && b.IsWorking() {
			time.Sleep(10 * time.Millisecond)
		}
		b.mu.Lock()
		b.startTime = time.Time{} // clear so a stale zero/old value cannot satisfy the probe
		b.mu.Unlock()
		before := time.Now()

		done := make(chan struct{})
		go func() {
			defer close(done)
			fn()
		}()

		// The startTime write happens in the first critical section of the
		// run. Poll startTime itself (not IsWorking): the run may complete
		// faster than an IsWorking poll catches it in the full suite, but a
		// run-start write remains observable afterwards.
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

		// Unwind so the goroutine cannot block the suite on provider I/O,
		// then wait for idle so the next probe starts from a clean state.
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
