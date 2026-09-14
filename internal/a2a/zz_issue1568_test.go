package a2a

// #1568 case B: stop() cleared m.browser unlocked while the refresh
// ticker's lookup() read it in a check-then-use pattern - the lock now
// pairs both sides (source pin; the race itself is a timing window).

import (
	"os"
	"strings"
	"testing"
)

func TestIssue1568LookupStopLockWired(t *testing.T) {
	b, err := os.ReadFile("mdns.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	lookup := src[strings.Index(src, "func (m *mdnsService) lookup()"):][:600]
	stop := src[strings.Index(src, "func (m *mdnsService) stop()"):][:500]
	if !strings.Contains(lookup, "m.mu.Lock()") || !strings.Contains(lookup, "m.mu.Unlock()") {
		t.Fatal("lookup must snapshot browser under the lock")
	}
	if !strings.Contains(stop, "m.mu.Lock()") {
		t.Fatal("stop must clear browser/server under the same lock")
	}
	if !strings.Contains(src, "sync.Mutex") && strings.Contains(src, "#1568-B") {
		t.Fatal("the race-pairing mutex must carry its issue annotation")
	}
}
