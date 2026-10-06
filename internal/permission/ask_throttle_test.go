package permission

import (
	"encoding/json"
	"testing"
	"time"
)

// ATR-2026-00118 pattern 1: rapid repeated permission requests must trip the
// ask throttle instead of re-prompting the user.

func denyTwice(t *testing.T, th *AskThrottle, name, args string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		th.RecordDenial(name, json.RawMessage(args))
	}
}

// Two denials of the same key within the window trip the breaker.
func TestAskThrottleSuppressesAfterTwoDenials(t *testing.T) {
	th := NewAskThrottle()
	if th.ShouldSuppress("run_command", json.RawMessage(`{"command":"rm -rf /"}`)) {
		t.Fatal("suppressed before any denial")
	}
	denyTwice(t, th, "run_command", `{"command":"rm -rf /"}`)
	if !th.ShouldSuppress("run_command", json.RawMessage(`{"command":"rm -rf /"}`)) {
		t.Fatal("not suppressed after two denials")
	}
}

// A different key is unaffected (a different directory is a different
// signature - pathSignature is dir+ext granularity by design, so same-dir
// files intentionally share one key).
func TestAskThrottleKeyIsolation(t *testing.T) {
	th := NewAskThrottle()
	denyTwice(t, th, "edit_file", `{"file_path":"/a/x.go"}`)
	if th.ShouldSuppress("edit_file", json.RawMessage(`{"file_path":"/b/x.go"}`)) {
		t.Fatal("different directory suppressed by another key's denials")
	}
}

// Denials older than the window expire.
func TestAskThrottleWindowExpiry(t *testing.T) {
	th := NewAskThrottle()
	th.window = 10 * time.Millisecond
	denyTwice(t, th, "im", `{"message":"x"}`)
	time.Sleep(15 * time.Millisecond)
	if th.ShouldSuppress("im", json.RawMessage(`{"message":"x"}`)) {
		t.Fatal("expired denials still suppress")
	}
}

// One denial alone never suppresses (a single deny may be a mis-tap retry).
func TestAskThrottleSingleDenialNoSuppress(t *testing.T) {
	th := NewAskThrottle()
	th.RecordDenial("git_push", json.RawMessage(`{}`))
	if th.ShouldSuppress("git_push", json.RawMessage(`{}`)) {
		t.Fatal("single denial suppressed")
	}
}

// Concurrent RecordDenial/ShouldSuppress must be race-free.
func TestAskThrottleConcurrent(t *testing.T) {
	th := NewAskThrottle()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			th.RecordDenial("run_command", json.RawMessage(`{"command":"x"}`))
		}
	}()
	for i := 0; i < 50; i++ {
		th.ShouldSuppress("run_command", json.RawMessage(`{"command":"x"}`))
	}
	<-done
}
