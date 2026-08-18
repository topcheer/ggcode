package tunnel

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/debug"
)

// #673 (#666 legacy): dropping a stale-epoch event is intentional (returns
// nil), but it must not be SILENT — a silent nil made the drop
// indistinguishable from a successful persist, so callers (tunnel_host
// AppendProjectionEvent) could not flag projBroken. The drop must be
// observable in the debug ring.
func TestIssue673StaleEpochDropLogged(t *testing.T) {
	debug.EnableForTest(t)

	s, err := NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewProjectionStore: %v", err)
	}
	// Fresh session is epoch 1; the cut moves it to epoch 2.
	if _, err := s.CutAuthority("sess-673"); err != nil {
		t.Fatalf("CutAuthority: %v", err)
	}

	// Late arrival from the superseded authority: dropped, nil error.
	if err := s.Append(GatewayMessage{
		SessionID:      "sess-673",
		Type:           EventActivity,
		EventID:        "evt-old-authority",
		AuthorityEpoch: 1,
	}); err != nil {
		t.Fatalf("stale-epoch drop must return nil (drop, not failure): %v", err)
	}

	// ...but observable in the debug ring.
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

	// Sanity: a current-epoch event is still persisted (the logging changed
	// nothing about the accept path).
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
