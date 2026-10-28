package agent

import (
	"testing"
)

// TestSetSessionIDNilRegistry pins #4513: NewAgent documents support for a nil
// tool registry, so SetSessionID must not panic via Registry.Get on a nil
// receiver. Before the fix this panicked on a.tools.Get("todo_write").
func TestSetSessionIDNilRegistry(t *testing.T) {
	a := NewAgent(&mockProvider{}, nil, "sys", 1)
	if a.tools != nil {
		t.Fatalf("precondition: NewAgent must keep nil registry nil, got %T", a.tools)
	}
	a.SetSessionID("session-4513")
	if got := a.sessionID; got != "session-4513" {
		t.Fatalf("sessionID = %q, want %q", got, "session-4513")
	}
}
