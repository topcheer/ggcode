package hooks

// Regression tests for issue #691 (regression of #684):
// truncateJSONDocument tracked a numeric `depth` but never appended container
// closers, then required json.Valid on the still-unclosed prefix — always
// false for any document that actually needed truncation. Every >64KB
// payload collapsed to the 90-byte truncation marker, destroying the data
// audit hooks were supposed to scan. The structural-boundary path must
// actually produce a repaired prefix that retains real payload fields.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssue691_StructuralCut_RetainsRealPayload(t *testing.T) {
	// The canonical trigger from the issue: a root-unclosed >64KB document.
	// path precedes the giant content so a structural cut must keep both the
	// tool name and the path field.
	big := strings.Repeat("a", 70*1024)
	payload := `{"tool":"write_file","input":{"path":"/tmp/x.go","content":"` + big + `"}}`

	got := truncateHookEnvJSON(payload)
	if len(got) > maxHookEnvValue {
		t.Fatalf("truncated payload still over budget: %d bytes", len(got))
	}
	if got == hookJSONTruncationMarker {
		t.Fatal("structural path collapsed to the marker fallback — dead-code regression (#691)")
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("repaired prefix is not valid JSON: %.160q", got)
	}
	if !strings.Contains(got, `"write_file"`) {
		t.Fatalf("repaired prefix must retain the tool name: %.160q", got)
	}
	if !strings.Contains(got, "/tmp/x.go") {
		t.Fatalf("repaired prefix must retain the path field: %.160q", got)
	}
}

func TestIssue691_TruncateJSONDocument_ClosesNestedContainers(t *testing.T) {
	// Nested object inside array inside object: the closers must be emitted
	// innermost-first with the correct bracket types.
	doc := `{"a":[{"b":1},{"c":"` + strings.Repeat("x", 400) + `"}],"d":true}`
	repaired, ok := truncateJSONDocument(doc, 64)
	if !ok {
		t.Fatal("expected a structural cut inside the budget")
	}
	if !json.Valid([]byte(repaired)) {
		t.Fatalf("repaired prefix not valid JSON: %q", repaired)
	}
	// The cut must close the root object (and any inner array) — the #691
	// defect was that closers were never appended.
	if !strings.HasSuffix(repaired, "}]") && !strings.HasSuffix(repaired, "}") {
		t.Fatalf("repaired prefix does not close open containers: %q", repaired)
	}
	if len(repaired) > 64 {
		t.Fatalf("repaired prefix over limit: %d bytes", len(repaired))
	}
}

func TestIssue691_StructuralCut_SingleGiantStringFallsBackToMarker(t *testing.T) {
	// Entire budget consumed by one string literal: no safe boundary exists,
	// the marker fallback is correct (and remains valid JSON).
	big := strings.Repeat("x", 300*1024)
	payload := `{"content":"` + big + `"}`
	got := truncateHookEnvJSON(payload)
	if got != hookJSONTruncationMarker {
		t.Fatalf("string-only payload must hit the marker fallback, got %.120q", got)
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("marker must be valid JSON: %q", got)
	}
}

func TestIssue691_TruncateJSONDocument_NeverExceedsLimit(t *testing.T) {
	// A deeply nested doc cut near the limit: appended closers must not push
	// the repaired prefix over the byte limit (candidates that would are
	// skipped in favor of a shorter one).
	doc := `{"a":{"b":{"c":{"d":[1,2,3,{"e":"` + strings.Repeat("y", 500) + `"}]}}}}`
	for _, limit := range []int{40, 64, 128, len(doc) - 1} {
		repaired, ok := truncateJSONDocument(doc, limit)
		if !ok {
			t.Fatalf("limit=%d: expected a structural cut", limit)
		}
		if len(repaired) > limit {
			t.Fatalf("limit=%d: repaired prefix is %d bytes", limit, len(repaired))
		}
		if !json.Valid([]byte(repaired)) {
			t.Fatalf("limit=%d: repaired prefix invalid: %q", limit, repaired)
		}
	}
}
