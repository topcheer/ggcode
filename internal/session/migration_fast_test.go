package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasNewFormatCheckpoint_NoFile(t *testing.T) {
	if hasNewFormatCheckpoint("/nonexistent/path.jsonl") {
		t.Error("expected false for nonexistent file")
	}
}

func TestHasNewFormatCheckpoint_NoCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	content := `{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"message","session_id":"test","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
`
	os.WriteFile(path, []byte(content), 0644)
	if hasNewFormatCheckpoint(path) {
		t.Error("expected false for file with no checkpoint")
	}
}

func TestHasNewFormatCheckpoint_NewFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	content := `{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"checkpoint","session_id":"test","checkpoint_summary_msg_id":"msg-123","checkpoint_last_msg_id":"msg-456","checkpoint_tokens":5000}
{"type":"message","session_id":"test","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
`
	os.WriteFile(path, []byte(content), 0644)
	if !hasNewFormatCheckpoint(path) {
		t.Error("expected true for file with new-format checkpoint")
	}
}

func TestHasNewFormatCheckpoint_OldFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	content := `{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"checkpoint","session_id":"test","checkpoint_last_msg_id":"msg-456","checkpoint_tokens":5000}
{"type":"message","session_id":"test","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
`
	os.WriteFile(path, []byte(content), 0644)
	if hasNewFormatCheckpoint(path) {
		t.Error("expected false for file with only old-format checkpoint")
	}
}

func TestHasNewFormatCheckpoint_MixedFormatEarlyExit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	// New-format checkpoint appears early; old-format appears later.
	// hasNewFormatCheckpoint should find the new-format one first and return true.
	content := `{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"msg1"}]}}
{"type":"checkpoint","session_id":"test","checkpoint_summary_msg_id":"msg-100","checkpoint_last_msg_id":"msg-101","checkpoint_tokens":3000}
{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"msg2"}]}}
{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"msg3"}]}}
{"type":"checkpoint","session_id":"test","checkpoint_last_msg_id":"msg-200","checkpoint_tokens":6000}
`
	os.WriteFile(path, []byte(content), 0644)
	if !hasNewFormatCheckpoint(path) {
		t.Error("expected true — should find new-format checkpoint before old-format")
	}
}

func TestHasNewFormatCheckpoint_LargeFileEarlyExit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.jsonl")
	// Simulate a large file: 5000 message lines, then a new-format checkpoint,
	// then 5000 more message lines. hasNewFormatCheckpoint should exit after
	// reading ~5001 lines, not 10001.
	var content []byte
	for i := 0; i < 5000; i++ {
		content = append(content, []byte(`{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"filler"}]}}
`)...)
	}
	content = append(content, []byte(`{"type":"checkpoint","session_id":"test","checkpoint_summary_msg_id":"msg-cp","checkpoint_last_msg_id":"msg-last","checkpoint_tokens":10000}
`)...)
	for i := 0; i < 5000; i++ {
		content = append(content, []byte(`{"type":"message","session_id":"test","message":{"role":"user","content":[{"type":"text","text":"trailing"}]}}
`)...)
	}
	os.WriteFile(path, content, 0644)
	if !hasNewFormatCheckpoint(path) {
		t.Error("expected true for large file with new-format checkpoint")
	}
}
