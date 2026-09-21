package main

import (
	"strings"
	"testing"
)

// #2623: the auto-load failure summary must distinguish lock IO failures from
// genuine cross-instance locks. Claiming "in use by other instances" when the
// lock directory was unreadable sends the user hunting for processes that do
// not exist (the #1487-D sister-path mistake).
func TestAutoLoadLockSummaryMessage_IODistinction(t *testing.T) {
	msg := autoLoadLockSummaryMessage(3, 3)
	if strings.Contains(msg, "in use by other instances") {
		t.Fatalf("IO-failure summary must not claim other instances: %q", msg)
	}
	if !strings.Contains(msg, "3 of 3") || !strings.Contains(msg, "I/O error") {
		t.Fatalf("IO-failure summary should name the real cause: %q", msg)
	}
	if !strings.Contains(msg, "Starting a new session") {
		t.Fatalf("summary should state the fallback: %q", msg)
	}
}

func TestAutoLoadLockSummaryMessage_AllLocked(t *testing.T) {
	msg := autoLoadLockSummaryMessage(0, 2)
	if !strings.Contains(msg, "2 workspace session(s) are in use by other instances") {
		t.Fatalf("pure-conflict summary should keep the original wording: %q", msg)
	}
	if strings.Contains(msg, "I/O error") {
		t.Fatalf("pure-conflict summary must not mention IO errors: %q", msg)
	}
}
