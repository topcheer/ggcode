package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4992: same-file dual-window multi_file_read must return BOTH windows.
// The old bare-path dedup key silently dropped the second window.
func TestIssue4992SameFileDualWindowsBothReturned(t *testing.T) {
	withFreshTracker(t)
	dir := t.TempDir()
	var sb strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&sb, "line%d\n", i)
	}
	f := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(f, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": f, "offset": 1, "limit": 5},
			{"path": f, "offset": 100, "limit": 5},
		},
	})
	res, err := MultiFileRead{}.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("unexpected failure: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "line1") || !strings.Contains(res.Content, "line5") {
		t.Fatalf("first window missing, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "line100") || !strings.Contains(res.Content, "line104") {
		t.Fatalf("second window was silently dropped (#4992 regression), got: %s", res.Content)
	}
	if strings.Contains(res.Content, "coalesced=") {
		t.Fatalf("distinct windows must not be reported as coalesced, got: %s", res.Content)
	}
}

func TestIssue4992ExactDuplicateWindowsCoalescedWithNotice(t *testing.T) {
	withFreshTracker(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "dup.txt")
	if err := os.WriteFile(f, []byte("aa\nbb\ncc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": f, "offset": 1, "limit": 2},
			{"path": f, "offset": 1, "limit": 2},
		},
	})
	res, err := MultiFileRead{}.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("unexpected failure: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "coalesced=1") {
		t.Fatalf("exact duplicate window should be coalesced with an explicit notice, got: %s", res.Content)
	}
	if got := strings.Count(res.Content, "=== FILE:"); got != 1 {
		t.Fatalf("exact duplicate window must yield exactly one block, got %d blocks: %s", got, res.Content)
	}
}
