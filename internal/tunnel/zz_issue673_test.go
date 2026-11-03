package tunnel

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/debug"
)

func TestIssue673StaleEpochDropLogged(t *testing.T) {
	debug.EnableForTest(t)

	s, err := NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewProjectionStore: %v", err)
	}

	if _, err := s.CutAuthority("sess-673"); err != nil {
		t.Fatalf("CutAuthority: %v", err)
	}

	if err := s.Append(GatewayMessage{
		SessionID:      "sess-673",
		Type:           EventActivity,
		EventID:        "evt-old-authority",
		AuthorityEpoch: 1,
	}); err != nil {
		t.Fatalf("stale-epoch drop must return nil (drop, not failure): %v", err)
	}

	found := false
	for _, filter := range []string{"tunnel", ""} {
		for _, e := range debug.RingHistory(200, filter) {
			if strings.Contains(e.Message, "stale-epoch") && strings.Contains(e.Message, "sess-673") {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("stale-epoch drop was silent — no debug ring entry (#673)")
	}

	if err := s.Append(GatewayMessage{
		SessionID:      "sess-673",
		Type:           EventActivity,
		EventID:        "evt-new-authority",
		AuthorityEpoch: 2,
	}); err != nil {
		t.Fatalf("current-epoch append must succeed: %v", err)
	}
	events, err := s.ReplayEvents("sess-673")
	if err != nil {
		t.Fatalf("ReplayEvents: %v", err)
	}
	sawNew := false
	for _, ev := range events {
		if ev.EventID == "evt-old-authority" {
			t.Fatalf("stale-epoch event must not be persisted")
		}
		if ev.EventID == "evt-new-authority" {
			sawNew = true
		}
	}
	if !sawNew {
		t.Fatalf("current-epoch event missing from replay")
	}
}
