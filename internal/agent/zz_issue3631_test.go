package agent

// #3631 probes: volatile-field stripping must not apply to MCP tools.
// timestamp/_t are legitimate semantic parameters (time ranges, query
// windows) for log/monitoring/DB MCP tools; stripping them made three
// queries for DIFFERENT windows normalize to one fingerprint and injected
// a false "results will be identical" assertion. Builtin tools keep
// stripping (their schemas are known); the warning wording is now
// uncertain ("possibly equivalent").

import (
	"strings"
	"testing"
)

func TestIssue3631_MCPVolatileFieldsKept(t *testing.T) {
	// Same query, different timestamp windows: distinct semantics.
	a := []byte(`{"query":"error","timestamp":"2026-10-08T00:00:00Z"}`)
	b := []byte(`{"query":"error","timestamp":"2026-10-09T00:00:00Z"}`)
	if normalizeArgs("mcp__logs__query", a) == normalizeArgs("mcp__logs__query", b) {
		t.Fatal("MCP tool timestamps must NOT be stripped - different windows collapsed to one fingerprint")
	}
	// Builtin tools keep stripping volatile metadata.
	c := []byte(`{"path":"x","trace_id":"aaa"}`)
	d := []byte(`{"path":"x","trace_id":"bbb"}`)
	if normalizeArgs("read_file", c) != normalizeArgs("read_file", d) {
		t.Fatal("builtin volatile stripping regressed")
	}
}

func TestIssue3631_MCPDifferentWindowsNoEquivWarning(t *testing.T) {
	s := newToolEquivDetectState()
	calls := [][]byte{
		[]byte(`{"query":"error","timestamp":"2026-10-07T00:00:00Z"}`),
		[]byte(`{"query":"error","timestamp":"2026-10-08T00:00:00Z"}`),
		[]byte(`{"query":"error","timestamp":"2026-10-09T00:00:00Z"}`),
	}
	var hint string
	for _, c := range calls {
		hint = s.recordCall("mcp__logs__query", c, rawFp("mcp__logs__query", c))
		if strings.Contains(hint, "equivalent arguments") {
			t.Fatalf("different time windows must not be flagged equivalent: %q", hint)
		}
	}
}

func TestIssue3631_WarningWordingUncertain(t *testing.T) {
	s := newToolEquivDetectState()
	// Raw fingerprints differ (trace_id varies) but normalize equal for a
	// builtin tool - the scenario the equivalence warning exists for.
	calls := [][]byte{
		[]byte(`{"pattern":"todo","trace_id":"t1"}`),
		[]byte(`{"pattern":"todo","trace_id":"t2"}`),
		[]byte(`{"pattern":"todo","trace_id":"t3"}`),
	}
	for i, c := range calls {
		h := s.recordCall("grep", c, rawFp("grep", c))
		if i == 2 {
			if !strings.Contains(h, "possibly equivalent") {
				t.Fatalf("warning must use uncertain wording, got: %q", h)
			}
			if strings.Contains(h, "will be identical") {
				t.Fatalf("deterministic 'will be identical' assertion must be gone: %q", h)
			}
		}
	}
}
