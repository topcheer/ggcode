package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4992: multi_file_read deduped by path alone, silently dropping a second
// same-file window requested in one call. The dedup key is now
// (path, offset, limit): distinct windows survive (second block marked
// "(cont.)"), identical duplicate windows collapse and surface in the
// summary as deduped=N while requested= reflects the original entry count.

func writeIssue4992Fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.txt")
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		b.WriteString(uniq4992Line(i))
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func uniq4992Line(i int) string {
	return "L" + string(rune('A'+i-1)) + "X9"
}

func runIssue4992Read(t *testing.T, entries []map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"files": entries})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := (MultiFileRead{}).Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	return res.Content
}

func TestMultiFileReadSameFileDualWindow(t *testing.T) {
	path := writeIssue4992Fixture(t)
	got := runIssue4992Read(t, []map[string]any{
		{"path": path, "offset": 1, "limit": 3},
		{"path": path, "offset": 25, "limit": 3},
	})

	// Both windows must be present - the pre-fix behavior dropped the second.
	for _, want := range []string{uniq4992Line(1), uniq4992Line(25)} {
		if !strings.Contains(got, want) {
			t.Errorf("second-window regression: %q missing from result:\n%s", want, got)
		}
	}
	// The second same-file block is explicitly marked as a continuation.
	if !strings.Contains(got, "(cont.)") {
		t.Errorf("continuation marker missing for second window:\n%s", got)
	}
	// Summary must reflect the original request: 2 entries, none deduped.
	if !strings.Contains(got, "requested=2") || !strings.Contains(got, "deduped=0") {
		t.Errorf("summary must report requested=2 deduped=0:\n%s", got)
	}
}

func TestMultiFileReadIdenticalWindowDedupe(t *testing.T) {
	path := writeIssue4992Fixture(t)
	got := runIssue4992Read(t, []map[string]any{
		{"path": path, "offset": 5, "limit": 4},
		{"path": path, "offset": 5, "limit": 4},
	})

	if !strings.Contains(got, uniq4992Line(5)) {
		t.Errorf("window content missing:\n%s", got)
	}
	if n := strings.Count(got, "=== FILE:"); n != 1 {
		t.Errorf("identical duplicate windows must collapse to one block, got %d blocks:\n%s", n, got)
	}
	if !strings.Contains(got, "requested=2") || !strings.Contains(got, "deduped=1") {
		t.Errorf("summary must report requested=2 deduped=1:\n%s", got)
	}
}

func TestMultiFileReadSchemaDocumentsWindowSemantics(t *testing.T) {
	schema := string((MultiFileRead{}).Parameters())
	for _, want := range []string{"different offset/limit windows", "collapsed"} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema must document same-file window semantics (%q missing): %s", want, schema)
		}
	}
}
