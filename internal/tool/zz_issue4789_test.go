package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #4789 (S-19) A-half: write_file computed blindWriteWarning AFTER
// atomicWriteFile, so a brand-new file was falsely warned about overwriting
// the bytes the call itself had just written, and the byte count in the
// warning for genuinely unread files reflected the NEW content size instead
// of the prior content size. The warning must be captured before the write
// (mirroring multi_file_write).
func TestIssue4789WriteFileNewFileNoFalseBlindWarn(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	dir := t.TempDir()
	fp := filepath.Join(dir, "brand_new.md")

	w := WriteFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": "hello S-19"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Created") {
		t.Errorf("expected Created message, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "without having read it") {
		t.Errorf("S-19 bug: brand-new file falsely warned about blind overwriting: %s", result.Content)
	}
}

func TestIssue4789WriteFileUnreadExistingStillWarns(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	dir := t.TempDir()
	fp := filepath.Join(dir, "unread.md")
	if err := os.WriteFile(fp, []byte("1234567890123"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := WriteFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": strings.Repeat("x", 45)})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "without having read it") {
		t.Errorf("unread existing file must still trigger blind-write warning, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "(13 bytes)") {
		t.Errorf("blind-write warning must report the PRIOR content size (13 bytes), got: %s", result.Content)
	}
}

func TestIssue4789WriteFileAfterReadNoWarn(t *testing.T) {
	defaultFileTracker.Reset()
	defer defaultFileTracker.Reset()

	dir := t.TempDir()
	fp := filepath.Join(dir, "read.md")
	if err := os.WriteFile(fp, []byte("prior"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(fp)

	w := WriteFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": "fresh"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if strings.Contains(result.Content, "without having read it") {
		t.Errorf("file read in this session must not warn, got: %s", result.Content)
	}
}
