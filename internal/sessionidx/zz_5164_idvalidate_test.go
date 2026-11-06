package sessionidx

import (
	"os"
	"path/filepath"
	"testing"
)

// #5164 CodeQL pins: session ids arrive from tool arguments and are joined
// into filesystem paths; a traversal id must never escape the sessions dir,
// and a caller-supplied limit must never drive an unbounded allocation.

func TestValidSessionID(t *testing.T) {
	good := []string{"20260716-180903-c595", "abc_DEF.123", "x"}
	for _, id := range good {
		if !ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = false, want true", id)
		}
	}
	bad := []string{"", "..", "../../etc/passwd", "a/b", `a\b`, "a\x00b", "with space", "semi;colon"}
	for _, id := range bad {
		if ValidSessionID(id) {
			t.Errorf("ValidSessionID(%q) = true, want false", id)
		}
	}
	// Bounded length.
	long := make([]byte, maxSessionIDLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if ValidSessionID(string(long)) {
		t.Error("oversized id must be rejected")
	}
}

func TestLoadRejectsTraversalID(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir, "../../etc/passwd"); err == nil {
		t.Fatal("Load must reject a traversal session id")
	}
	if ix, err := Load(dir, "safe-id"); err != nil || ix == nil {
		t.Fatalf("Load must accept a valid id: ix=%v err=%v", ix, err)
	}
}

func TestScanSourceRejectsTraversalID(t *testing.T) {
	dir := t.TempDir()
	// A file OUTSIDE the sessions dir that must never be readable.
	outside := filepath.Join(filepath.Dir(dir), "outside-secret.jsonl")
	body := `{"type":"message","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"token-secret"}]}}` + "\n"
	if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	if res := scanSourceForTerms(dir, "../outside-secret", []string{"token-secret"}, 5); len(res) != 0 {
		t.Fatal("degraded scan must refuse a traversal id")
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{-5: 20, 0: 20, 1: 1, 20: 20, maxQueryLimit: maxQueryLimit, maxQueryLimit + 1: maxQueryLimit}
	for in, want := range cases {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}
