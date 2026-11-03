package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3109CoerceNumberRejectsNonFinite(t *testing.T) {
	for _, s := range []string{`"inf"`, `"Infinity"`, `"INF"`, `"-Infinity"`, `"nan"`, `"NaN"`, `"+Inf"`} {
		out, ok := coerceNumber(json.RawMessage(s))
		if ok {
			t.Errorf("coerceNumber(%s) accepted non-finite value, produced %q", s, string(out))
		}
		if string(out) != s {
			t.Errorf("coerceNumber(%s) must return the original value on rejection, got %q", s, string(out))
		}
	}
}

func TestIssue3109CoerceNumberEmitsLegalJSON(t *testing.T) {
	for _, s := range []string{`"3.14"`, `"42"`, `"-0.5"`, `"1e10"`} {
		out, ok := coerceNumber(json.RawMessage(s))
		if !ok {
			t.Errorf("coerceNumber(%s) rejected a finite value", s)
			continue
		}
		var back float64
		if err := json.Unmarshal(out, &back); err != nil {
			t.Errorf("coerceNumber(%s) emitted %q which is not valid JSON: %v", s, string(out), err)
		}
	}
}

func TestIssue3110MoveRemoveFailureDisclosesBothPaths(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old.md")
	if err := os.WriteFile(old, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	patch := "*** Begin Patch\n*** Update File: old.md\n*** Move to: sub/moved.md\n@@\n-hello\n+goodbye\n*** End Patch\n"
	secs, err := parseV4APatch(patch)
	if err != nil || len(secs) != 1 {
		t.Fatalf("parse: %v secs=%d", err, len(secs))
	}
	_, err = applyV4ASection(filepath.Join(dir, "old.md"), secs[0])
	if err == nil {
		t.Fatal("expected move error (read-only parent -> Remove(old.md) must fail)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "partial migration") {
		t.Fatalf("error lacks partial-migration disclosure: %q", msg)
	}
	if !strings.Contains(msg, "moved.md") {
		t.Fatalf("error does not name the written target: %q", msg)
	}
	if !strings.Contains(msg, "old.md") {
		t.Fatalf("error does not name the remaining old path: %q", msg)
	}

	written, rerr := os.ReadFile(filepath.Join(sub, "moved.md"))
	if rerr != nil {
		t.Fatalf("target not actually written (disclosure would be a lie): %v", rerr)
	}
	if !strings.Contains(string(written), "goodbye") {
		t.Fatalf("target content wrong: %q", string(written))
	}
	if _, serr := os.Stat(old); serr != nil {
		t.Fatalf("old file unexpectedly gone: %v", serr)
	}
}
