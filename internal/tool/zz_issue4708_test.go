package tool

import (
	"strings"
	"testing"
	"time"
)

// #4708: bi-temporal consistency double defect.
//   A. doLink must reject a re-assertion whose valid_from lands inside the
//      prior CLOSED interval (the comment always promised "Reject on
//      interval overlap" but closed edges fell straight through to relink,
//      so trace as_of inside the overlap showed two parallel edges).
//   B. trace must dedupe by (node, edgeType): a second relation to an
//      already-visited node is a distinct edge, not a duplicate to drop.

func TestIssue4708RelinkInsideClosedIntervalRejected(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on"}`)
	// Backdate a REAL closed interval that ended in the past: [2024, 2025-06).
	// link(T1) -> invalidate(T2); not-valid-now (ValidUntil < now) so the
	// link loop reaches the closed-interval branch under test.
	store, err := tool.load()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	closed := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	store.Edges[0].ValidFrom = &started
	store.Edges[0].ValidUntil = &closed
	if err := tool.save(store); err != nil {
		t.Fatal(err)
	}

	// Re-assert at 2024-12-15 - INSIDE [2024, 2025-06): must be rejected, not
	// appended as a parallel [2024-12-15, inf) edge.
	r := kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on","valid_from":"2024-12-15T00:00:00Z"}`)
	if !strings.Contains(r.Content, "overlaps") {
		t.Fatalf("re-assertion inside the closed interval must be rejected as overlap, got: %s", r.Content)
	}
	// No parallel edge was appended.
	store2, _ := tool.load()
	n := 0
	for _, e := range store2.Edges {
		if e.From == "a" && e.To == "b" && e.Type == "depends-on" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("edge count after rejected overlap = %d, want 1 (no parallel append)", n)
	}

	// Re-assert after the interval closed (2025-09 > 2025-06, still in the
	// past so it is valid-now): legitimate re-assertion.
	r = kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on","valid_from":"2025-09-01T00:00:00Z"}`)
	if !strings.Contains(r.Content, "Re-validated") {
		t.Fatalf("re-assertion after the interval closed must succeed as re-validation, got: %s", r.Content)
	}
}

func TestIssue4708TraceShowsBothRelationsBetweenSamePair(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on"}`)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to"}`)
	// NOTE: two different TYPES to the same target is NOT the duplicate
	// rejection case - both must exist for the trace to be complete.
	r := kgMustOK(t, tool, `{"action":"trace","id":"a"}`)
	hasDepends := strings.Contains(r.Content, "--[depends-on]-->")
	hasRelates := strings.Contains(r.Content, "--[relates-to]-->")
	if !hasDepends || !hasRelates {
		t.Fatalf("trace must show BOTH relation types between the same pair, got: %s", r.Content)
	}
}
