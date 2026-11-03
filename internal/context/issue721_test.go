package context

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractReadCall_HeadPeekWithLimitIsNotFull(t *testing.T) {
	rc := extractReadCall("read_file", json.RawMessage(`{"path":"/big.go","limit":50}`))
	if len(rc.paths) != 1 || rc.paths[0] != "/big.go" {
		t.Fatalf("unexpected paths: %v", rc.paths)
	}
	if rc.rng.full {
		t.Fatal("read_file with limit but no offset is a partial (head) read, not full")
	}
	if rc.rng.offset != 0 || rc.rng.limit != 50 {
		t.Fatalf("unexpected range: %+v", rc.rng)
	}
}

func TestReadCovers_HeadPeekDoesNotCoverMidSegment(t *testing.T) {
	later := extractReadCall("read_file", json.RawMessage(`{"path":"/x.go","limit":50}`)).rng
	earlier := extractReadCall("read_file", json.RawMessage(`{"path":"/x.go","offset":50,"limit":10}`)).rng
	if readCovers(later, earlier) {
		t.Fatal("head peek (limit-only) must not cover a mid-file segment")
	}
}

func TestCompactSupersededReads_HeadPeekDoesNotSupersedeMidSegment(t *testing.T) {
	m := NewManager(100000)
	addReadPair(m, "read-mid", `{"path":"/big.go","offset":50,"limit":10}`, strings.Repeat("M", 500))
	addReadPair(m, "read-head", `{"path":"/big.go","limit":50}`, strings.Repeat("H", 500))

	freed := m.CompactSupersededReads()
	if freed != 0 {
		t.Fatalf("expected 0 tokens freed - head peek must not supersede mid-file read, got %d", freed)
	}
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && strings.HasPrefix(b.Output, "[superseded:") {
				t.Fatalf("output %s must not be marked superseded", b.ToolID)
			}
		}
	}
}

func TestCompactSupersededReads_HeadPeekStillSupersedesItsOwnRange(t *testing.T) {
	m := NewManager(100000)
	addReadPair(m, "read-top", `{"path":"/h.go","offset":1,"limit":10}`, strings.Repeat("T", 500))
	addReadPair(m, "read-head", `{"path":"/h.go","limit":50}`, strings.Repeat("H", 500))

	if freed := m.CompactSupersededReads(); freed <= 0 {
		t.Fatal("expected tokens freed - head peek covers the top-of-file partial read")
	}
}
