package tool

import (
	"strings"
	"testing"
	"time"
)

func TestIssue4708RelinkInsideClosedIntervalRejected(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on"}`)

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

	r := kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on","valid_from":"2024-12-15T00:00:00Z"}`)
	if !strings.Contains(r.Content, "overlaps") {
		t.Fatalf("re-assertion inside the closed interval must be rejected as overlap, got: %s", r.Content)
	}

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

	r = kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on","valid_from":"2025-09-01T00:00:00Z"}`)
	if !strings.Contains(r.Content, "Re-validated") {
		t.Fatalf("re-assertion after the interval closed must succeed as re-validation, got: %s", r.Content)
	}
}

func TestIssue4708TraceShowsBothRelationsBetweenSamePair(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on"}`)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to"}`)

	r := kgMustOK(t, tool, `{"action":"trace","id":"a"}`)
	hasDepends := strings.Contains(r.Content, "--[depends-on]-->")
	hasRelates := strings.Contains(r.Content, "--[relates-to]-->")
	if !hasDepends || !hasRelates {
		t.Fatalf("trace must show BOTH relation types between the same pair, got: %s", r.Content)
	}
}
