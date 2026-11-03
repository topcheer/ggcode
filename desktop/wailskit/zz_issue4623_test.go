package wailskit

import (
	"os"
	"strings"
	"testing"
)

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
		body = body[:end+2]
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

	if !strings.Contains(head, "ses := b.currentSes") {
		t.Fatal("session pointer must be snapshotted under the lock (#4623)")
	}
	tail := body[apply:]
	if strings.Contains(tail, "b.currentSes.") {
		t.Fatal("SwitchModel tail must use the locked snapshot, not unlocked b.currentSes reads (#4623)")
	}
}
