package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// #1547 case A pins: the fan-out writers must expose faithful previews
// (the dry-run gate runs on these).
func Test1547PreviewMultiEditFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	os.WriteFile(p, []byte("package p\n\nfunc f() {}\n"), 0o644)
	tt := MultiEditFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]any{
		"file_path": p,
		"edits": []map[string]string{
			{"old_text": "func f() {}", "new_text": "func g() {}"},
		},
	})
	plans, err := tt.PreviewChanges(input)
	if err != nil || len(plans) != 1 {
		t.Fatalf("preview failed: %v plans=%d", err, len(plans))
	}
	if plans[0].OldContent == plans[0].NewContent || !contains1547(plans[0].NewContent, "func g()") {
		t.Fatalf("preview must simulate the edit, got new=%q", plans[0].NewContent)
	}
}

func Test1547PreviewMultiFileWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.txt")
	os.WriteFile(p, []byte("old"), 0o644)
	tt := MultiFileWrite{WorkingDir: dir}
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{{"path": p, "content": "brand new"}},
	})
	plans, err := tt.PreviewChanges(input)
	if err != nil || len(plans) != 1 {
		t.Fatalf("preview failed: %v plans=%d", err, len(plans))
	}
	if plans[0].OldContent != "old" || plans[0].NewContent != "brand new" {
		t.Fatalf("bad plan: old=%q new=%q", plans[0].OldContent, plans[0].NewContent)
	}
}

func Test1547PreviewBatchReplace(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.txt")
	os.WriteFile(p, []byte("foo1 foo2"), 0o644)
	tt := BatchReplace{WorkingDir: dir}
	// literal
	input, _ := json.Marshal(map[string]any{"pattern": "foo", "replacement": "bar", "files": []string{p}})
	plans, err := tt.PreviewChanges(input)
	if err != nil || len(plans) != 1 || plans[0].NewContent != "bar1 bar2" {
		t.Fatalf("literal preview wrong: err=%v plans=%d new=%q", err, len(plans), plans[0].NewContent)
	}
	// regex
	input2, _ := json.Marshal(map[string]any{"pattern": `foo(\d)`, "replacement": "n$1", "is_regex": true, "files": []string{p}})
	plans2, err := tt.PreviewChanges(input2)
	if err != nil || len(plans2) != 1 || plans2[0].NewContent != "n1 n2" {
		t.Fatalf("regex preview wrong: err=%v new=%q", err, plans2[0].NewContent)
	}
	// no match -> no plan
	input3, _ := json.Marshal(map[string]any{"pattern": "zzz", "replacement": "x", "files": []string{p}})
	plans3, _ := tt.PreviewChanges(input3)
	if len(plans3) != 0 {
		t.Fatalf("no-match must yield no plan, got %d", len(plans3))
	}
}

func contains1547(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}
