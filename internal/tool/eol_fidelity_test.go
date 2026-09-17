package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasBareLF reports whether s contains a line feed that is not part of a
// CRLF pair, i.e. mixed line endings.
func hasBareLF(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' && (i == 0 || s[i-1] != '\r') {
			return true
		}
	}
	return false
}

func TestFileUsesCRLF(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"pure LF", "a\nb\n", false},
		{"pure CRLF", "a\r\nb\r\n", true},
		{"empty", "", false},
		// A single pasted Windows line must not flip an LF file's convention.
		{"mostly LF, stray CRLF", "a\r\nb\nc\nd\ne\n", false},
		// Conversely a CRLF file with one stray LF keeps CRLF convention.
		{"mostly CRLF, stray LF", "a\r\nb\r\nc\r\nd\ne\r\n", true},
	}
	for _, tc := range cases {
		if got := fileUsesCRLF(tc.content); got != tc.want {
			t.Errorf("%s: fileUsesCRLF = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNormalizeNewTextEOL(t *testing.T) {
	crlf := "one\r\ntwo\r\n"
	lf := "one\ntwo\n"
	if got := normalizeNewTextEOL(crlf, "a\nb"); got != "a\r\nb" {
		t.Errorf("CRLF file: new_text not converted: %q", got)
	}
	if got := normalizeNewTextEOL(lf, "a\nb"); got != "a\nb" {
		t.Errorf("LF file: new_text must stay LF: %q", got)
	}
	// Already-CRLF new_text is left untouched.
	if got := normalizeNewTextEOL(crlf, "a\r\nb"); got != "a\r\nb" {
		t.Errorf("already CRLF: rewritten: %q", got)
	}
	// No newlines at all: no-op.
	if got := normalizeNewTextEOL(crlf, "just a line"); got != "just a line" {
		t.Errorf("no newlines: rewritten: %q", got)
	}
	// Mixed new_text: conservative, left as-is (same as previous behavior).
	if got := normalizeNewTextEOL(crlf, "a\r\nb\nc"); got != "a\r\nb\nc" {
		t.Errorf("mixed new_text: rewritten: %q", got)
	}
}

// TestEditFile_CRLFSingleLineOldMultiLineNew pins the residual #1676 case 3
// gap: a single-line old_text contains no CRLF pair even after conversion,
// so the previous oldText-based condition skipped normalization and a
// multi-line LF new_text landed in the CRLF file with mixed endings.
func TestEditFile_CRLFSingleLineOldMultiLineNew(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "notes.md")
	os.WriteFile(fp, []byte("# Title\r\n\r\nold body\r\n"), 0644)

	ef := EditFile{}
	input := json.RawMessage(`{
		"file_path": "` + filepath.ToSlash(fp) + `",
		"old_text": "old body",
		"new_text": "new body\nplus a second line"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("edit failed: %s", result.Content)
	}
	got, _ := os.ReadFile(fp)
	if hasBareLF(string(got)) {
		t.Fatalf("mixed line endings survived single-line edit (#1676 residual):\n%q", got)
	}
	if !strings.Contains(string(got), "new body\r\nplus a second line") {
		t.Fatalf("replacement text missing:\n%q", got)
	}
}

// TestPlanTextEdits_CRLFNewText pins the planTextEdits gap: before this fix
// the batch planner had no new_text CRLF handling at all (only edit_file
// did), so multi_edit_file and multi_file_edit silently introduced mixed
// endings on byte-exact CRLF matches.
func TestPlanTextEdits_CRLFNewText(t *testing.T) {
	content := "alpha\r\nold one\r\nbeta\r\nold two\r\n"
	out, n, msg := planTextEdits(content, []textEdit{
		{OldText: "old one\r\n", NewText: "new one\n"},
		{OldText: "old two\r\n", NewText: "new two\n"},
	})
	if msg != "" {
		t.Fatalf("planTextEdits failed: %s", msg)
	}
	if n != 2 {
		t.Fatalf("applied = %d, want 2", n)
	}
	if hasBareLF(out) {
		t.Fatalf("mixed line endings from batch planner:\n%q", out)
	}
	if !strings.Contains(out, "new one\r\nbeta\r\nnew two") {
		t.Fatalf("unexpected content:\n%q", out)
	}
}

// TestMultiEditFile_CRLFNewText exercises the multi_edit_file tool end to end.
func TestMultiEditFile_CRLFNewText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "notes.md")
	os.WriteFile(fp, []byte("alpha\r\nold one\r\nbeta\r\n"), 0644)

	me := MultiEditFile{}
	input := json.RawMessage(`{
		"file_path": "` + filepath.ToSlash(fp) + `",
		"edits": [{"old_text": "old one\r\n", "new_text": "new one\nextra line\n"}]
	}`)
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("multi_edit_file failed: %s", result.Content)
	}
	got, _ := os.ReadFile(fp)
	if hasBareLF(string(got)) {
		t.Fatalf("mixed line endings survived multi_edit_file:\n%q", got)
	}
}

// TestMultiFileEdit_CRLFNewText exercises the multi_file_edit tool end to end.
func TestMultiFileEdit_CRLFNewText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "notes.md")
	os.WriteFile(fp, []byte("alpha\r\nold one\r\nbeta\r\n"), 0644)

	mfe := MultiFileEdit{}
	input := json.RawMessage(`{
		"files": [{"path": "` + filepath.ToSlash(fp) + `", "edits": [{"old_text": "old one\r\n", "new_text": "new one\nextra line\n"}]}]
	}`)
	result, err := mfe.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("multi_file_edit failed: %s", result.Content)
	}
	got, _ := os.ReadFile(fp)
	if hasBareLF(string(got)) {
		t.Fatalf("mixed line endings survived multi_file_edit:\n%q", got)
	}
}

// TestEditFile_LFFileUntouched guards against over-conversion: an LF file
// (even with a stray CRLF pair) must never receive CRLF from new_text.
func TestEditFile_LFFileUntouched(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "notes.md")
	// One stray CRLF among many LF lines: convention is still LF.
	os.WriteFile(fp, []byte("a\r\nb\nc\nd\nold body\n"), 0644)

	ef := EditFile{}
	input := json.RawMessage(`{
		"file_path": "` + filepath.ToSlash(fp) + `",
		"old_text": "old body",
		"new_text": "new body\nsecond line"
	}`)
	result, err := ef.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("edit failed: %s", result.Content)
	}
	got, _ := os.ReadFile(fp)
	if strings.Contains(string(got), "\r\nnew body\r\nsecond") {
		t.Fatalf("LF file was rewritten to CRLF:\n%q", got)
	}
	if !strings.Contains(string(got), "new body\nsecond line") {
		t.Fatalf("replacement text missing:\n%q", got)
	}
}
