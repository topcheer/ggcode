package permission

import (
	"encoding/json"
	"strings"
	"testing"
)

// #3059 acceptance: a learned delegate approval must never ride a different
// prompt - MakeKey pins free-text body tools to a hash of the body text.
func TestIssue3059_MakeKeyBodiesPinnedToPromptHash(t *testing.T) {
	mk := func(prompt string) string {
		in, _ := json.Marshal(map[string]string{"agent": "claude", "prompt": prompt})
		k, ok := MakeKey("delegate", in)
		if !ok {
			t.Fatal("expected ok")
		}
		return k
	}
	a, b := mk("review the auth service"), mk("deploy production now")
	if a == b {
		t.Fatalf("different prompts must yield different keys: %q", a)
	}
	if !strings.HasPrefix(a, "delegate:") {
		t.Fatalf("key must not be bare tool name: %q", a)
	}
	if mk("review the auth service") != a {
		t.Fatal("identical prompt must yield identical key (learning still works)")
	}
	// spawn_agent task / lanchat message are the same shape.
	taskIn, _ := json.Marshal(map[string]string{"task": "do X"})
	if k, _ := MakeKey("spawn_agent", taskIn); !strings.HasPrefix(k, "spawn_agent:") {
		t.Fatalf("task field must be signed, got %q", k)
	}
	msgIn, _ := json.Marshal(map[string]string{"to": "tm-2", "message": "hello"})
	if k, _ := MakeKey("send_message", msgIn); !strings.HasPrefix(k, "send_message:") {
		t.Fatalf("message field must be signed, got %q", k)
	}
}
