package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Issue #564 Bug D: by-ID export used the windowed store.Load, silently
// truncating long-running sessions to the 24h window (probe: 500 messages
// → 1) with no truncation marker. The fix mirrors #538's RenameSession
// treatment: LoadWithOptions(id, true) for the full transcript.
func TestIssue564_ExportLoadsFullHistoryNotWindow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp) // NewDefaultStore -> $HOME/.ggcode/sessions
	dir := filepath.Join(tmp, ".ggcode", "sessions")
	id := "sess564export"
	path := filepath.Join(dir, id+".jsonl")

	now := time.Now()
	oldTS := now.Add(-48 * time.Hour) // only user message: outside 24h window
	recentTS := now.Add(-1 * time.Hour)

	var b strings.Builder
	fmt.Fprintf(&b, `{"type":"meta","session_id":%q,"title":"Long running","created_at":%q,"updated_at":%q}`+"\n",
		id, oldTS.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	fmt.Fprintf(&b, `{"type":"message","session_id":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"ancient user plea"}]}}`+"\n",
		id, oldTS.Format(time.RFC3339Nano))
	for i := 0; i < 499; i++ {
		fmt.Fprintf(&b, `{"type":"message","session_id":%q,"timestamp":%q,"message":{"role":"assistant","content":[{"type":"text","text":"recent %d"}]}}`+"\n",
			id, recentTS.Format(time.RFC3339Nano), i)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	md, err := ExportSessionToMarkdown(id)
	if err != nil {
		t.Fatalf("ExportSessionToMarkdown: %v", err)
	}
	if !strings.Contains(md, "ancient user plea") {
		t.Error("export silently truncated: the >24h-old user message is missing (windowed load leaked into export path)")
	}
	if !strings.Contains(md, "recent 498") {
		t.Error("recent messages missing from export")
	}

	js, err := ExportSessionToJSON(id)
	if err != nil {
		t.Fatalf("ExportSessionToJSON: %v", err)
	}
	if !strings.Contains(js, "ancient user plea") {
		t.Error("JSON export silently truncated: >24h-old user message missing")
	}
}
