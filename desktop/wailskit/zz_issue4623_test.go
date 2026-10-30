package wailskit

import (
	"os"
	"strings"
	"testing"
)

// #4623: SwitchModel must take the agent pointer, write b.resolved, and
// snapshot the session inside ONE critical section - the old two-lock
// sequence let a concurrent ResetAgent (LoadSession/ResumeSession) swap the
// agent between the sections so ApplyProviderToAgent patched a discarded
// instance while b.resolved advertised the new model. Unlocked reads of
// b.currentSes were also a data race against setSessionState.
func TestIssue4623_SwitchModelSingleCriticalSection(t *testing.T) {
	data, err := os.ReadFile("chat.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	fn := strings.Index(text, "func (b *ChatBridge) SwitchModel")
	if fn < 0 {
		t.Fatal("SwitchModel not found")
	}
	body := text[fn:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end+2] // clamp to the function body; later functions may legitimately read b.currentSes under their own locks
	}
	apply := strings.Index(body, "agentruntime.ApplyProviderToAgent")
	if apply < 0 {
		t.Fatal("ApplyProviderToAgent call not found in SwitchModel")
	}
	head := body[:apply]
	unlocks := strings.Count(head, "b.mu.Unlock()")
	if unlocks != 1 {
		t.Fatalf("exactly one Unlock must sit between section start and ApplyProviderToAgent, found %d (#4623)", unlocks)
	}
	// The session snapshot must come from under the lock, not from a later
	// unlocked read of b.currentSes.
	if !strings.Contains(head, "ses := b.currentSes") {
		t.Fatal("session pointer must be snapshotted under the lock (#4623)")
	}
	tail := body[apply:]
	if strings.Contains(tail, "b.currentSes.") {
		t.Fatal("SwitchModel tail must use the locked snapshot, not unlocked b.currentSes reads (#4623)")
	}
}
