package permission

import (
	"encoding/json"
	"testing"
	"time"
)

func denyTwice(t *testing.T, th *AskThrottle, name, args string) {
	t.Helper()
	for i := 0; i < 2; i++ {
		th.RecordDenial(name, json.RawMessage(args))
	}
}

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

func TestAskThrottleKeyIsolation(t *testing.T) {
	th := NewAskThrottle()
	denyTwice(t, th, "edit_file", `{"file_path":"/a/x.go"}`)
	if th.ShouldSuppress("edit_file", json.RawMessage(`{"file_path":"/b/x.go"}`)) {
		t.Fatal("different directory suppressed by another key's denials")
	}
}

func TestAskThrottleWindowExpiry(t *testing.T) {
	th := NewAskThrottle()
	th.window = 10 * time.Millisecond
	denyTwice(t, th, "im", `{"message":"x"}`)
	time.Sleep(15 * time.Millisecond)
	if th.ShouldSuppress("im", json.RawMessage(`{"message":"x"}`)) {
		t.Fatal("expired denials still suppress")
	}
}

func TestAskThrottleSingleDenialNoSuppress(t *testing.T) {
	th := NewAskThrottle()
	th.RecordDenial("git_push", json.RawMessage(`{}`))
	if th.ShouldSuppress("git_push", json.RawMessage(`{}`)) {
		t.Fatal("single denial suppressed")
	}
}

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
