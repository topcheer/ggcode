package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func rawFp(name string, args []byte) string {
	h := sha256.Sum256(append([]byte(name+"|"), args...))
	return hex.EncodeToString(h[:16])
}

func TestNormalizeArgs_KeyOrder(t *testing.T) {
	a := []byte(`{"pattern":"foo","path":"/x"}`)
	b := []byte(`{"path":"/x","pattern":"foo"}`)
	if normalizeArgs(a) != normalizeArgs(b) {
		t.Errorf("normalized forms should match for reordered keys")
	}
}

func TestNormalizeArgs_VolatileFields(t *testing.T) {
	a := []byte(`{"pattern":"foo","trace_id":"abc","timestamp":"2024-01-01"}`)
	b := []byte(`{"pattern":"foo"}`)
	if normalizeArgs(a) != normalizeArgs(b) {
		t.Errorf("volatile fields should be stripped: %q vs %q", normalizeArgs(a), normalizeArgs(b))
	}
}

func TestNormalizeArgs_NestedObjects(t *testing.T) {
	a := []byte(`{"b":{"y":2,"x":1},"a":3}`)
	b := []byte(`{"a":3,"b":{"x":1,"y":2}}`)
	if normalizeArgs(a) != normalizeArgs(b) {
		t.Errorf("nested object key ordering should be normalized")
	}
}

func TestNormalizeArgs_DifferentValues(t *testing.T) {
	a := []byte(`{"pattern":"foo"}`)
	b := []byte(`{"pattern":"bar"}`)
	if normalizeArgs(a) == normalizeArgs(b) {
		t.Errorf("different values should produce different normalized forms")
	}
}

func TestNormalizeArgs_InvalidJSON(t *testing.T) {
	raw := []byte(`not json at all`)
	result := normalizeArgs(raw)
	if result != string(raw) {
		t.Errorf("invalid JSON should fall back to raw string, got %q", result)
	}
}

func TestNormalizeArgs_Empty(t *testing.T) {
	if normalizeArgs(nil) != "" {
		t.Errorf("empty args should produce empty string")
	}
}

func TestToolEquivDetect_ReorderedKeys(t *testing.T) {
	s := newToolEquivDetectState()
	call1 := []byte(`{"pattern":"TODO","path":"/src"}`)
	call2 := []byte(`{"path":"/src","pattern":"TODO"}`)

	// First call - no warning
	hint1 := s.recordCall("grep", call1, rawFp("grep", call1))
	if hint1 != "" {
		t.Errorf("first call should not warn, got: %q", hint1)
	}

	// Reordered keys = DIVERGENT raw fingerprints: this is the
	// semantic-equivalence case. Threshold is 3 (#494) — second call
	// only counts, third one warns.
	hint2 := s.recordCall("grep", call2, rawFp("grep", call2))
	if hint2 != "" {
		t.Errorf("second call is below threshold 3, no warning yet, got: %q", hint2)
	}
	hint3 := s.recordCall("grep", call1, rawFp("grep", call1))
	if hint3 == "" {
		t.Errorf("third semantically-equivalent call (divergent raw) should trigger warning")
	}
}

func TestToolEquivDetect_VolatileFields(t *testing.T) {
	s := newToolEquivDetectState()
	call1 := []byte(`{"query":"test","trace_id":"aaa"}`)
	call2 := []byte(`{"query":"test","trace_id":"bbb","timestamp":"now"}`)

	hint1 := s.recordCall("search", call1, rawFp("search", call1))
	if hint1 != "" {
		t.Errorf("first call should not warn")
	}

	// Volatile-only divergence: below threshold 3 no warning yet (#494).
	hint2 := s.recordCall("search", call2, rawFp("search", call2))
	if hint2 != "" {
		t.Errorf("second call is below threshold 3, no warning yet, got: %q", hint2)
	}
	hint3 := s.recordCall("search", call1, rawFp("search", call1))
	if hint3 == "" {
		t.Errorf("third semantically-equivalent call should trigger warning")
	}
}

func TestToolEquivDetect_ExactMatchNoDoubleWarn(t *testing.T) {
	s := newToolEquivDetectState()
	args := []byte(`{"pattern":"foo"}`)

	// Byte-identical repetition: raw fingerprints are identical, so
	// tool_redundancy already owns the warning — this detector YIELDS
	// (#494 implements the documented :32 contract; the old write-only
	// rawFingerprints map never actually suppressed anything).
	hint1 := s.recordCall("grep", args, rawFp("grep", args))
	if hint1 != "" {
		t.Errorf("first call should not warn")
	}
	for i := 0; i < 4; i++ {
		if h := s.recordCall("grep", args, rawFp("grep", args)); h != "" {
			t.Fatalf("byte-identical repeat %d must NOT double-warn here (tool_redundancy owns it), got: %q", i+2, h)
		}
	}
}

func TestToolEquivDetect_MaxWarnings(t *testing.T) {
	s := newToolEquivDetectState()
	// Fire enough to hit max warnings
	for i := 0; i < 10; i++ {
		call := []byte(`{"q":"val"}`)
		hint := s.recordCall("search", call, rawFp("search", call))
		_ = hint
	}
	if s.warnings > equivMaxWarnings {
		t.Errorf("warnings %d exceeded max %d", s.warnings, equivMaxWarnings)
	}
}

func TestToolEquivDetect_DifferentTools(t *testing.T) {
	s := newToolEquivDetectState()
	call1 := []byte(`{"pattern":"foo"}`)
	call2 := []byte(`{"pattern":"foo"}`)

	hint1 := s.recordCall("grep", call1, rawFp("grep", call1))
	if hint1 != "" {
		t.Errorf("first call should not warn")
	}
	// Same args but different tool name - should NOT trigger
	hint2 := s.recordCall("search", call2, rawFp("search", call2))
	if hint2 != "" {
		t.Errorf("different tool name with same args should not cross-trigger")
	}
}

func TestToolEquivDetect_Reset(t *testing.T) {
	s := newToolEquivDetectState()
	call := []byte(`{"q":"test"}`)
	s.recordCall("search", call, rawFp("search", call))
	s.recordCall("search", call, rawFp("search", call))
	if len(s.normalizedCounts) == 0 {
		t.Fatal("expected counts after recording")
	}
	s.reset()
	if len(s.normalizedCounts) != 0 || s.warnings != 0 {
		t.Errorf("reset should clear all state")
	}
}

func TestNormalizedFingerprint_Deterministic(t *testing.T) {
	fp1 := normalizedFingerprint("grep", `{"pattern":"foo"}`)
	fp2 := normalizedFingerprint("grep", `{"pattern":"foo"}`)
	if fp1 != fp2 {
		t.Errorf("fingerprint should be deterministic")
	}
}

func TestFingerprintToolCallNormalized_MatchesReordered(t *testing.T) {
	fp1 := fingerprintToolCallNormalized("grep", []byte(`{"pattern":"foo","path":"/x"}`))
	fp2 := fingerprintToolCallNormalized("grep", []byte(`{"path":"/x","pattern":"foo"}`))
	if fp1 != fp2 {
		t.Errorf("normalized fingerprint should match for reordered keys: %s vs %s", fp1, fp2)
	}
}
