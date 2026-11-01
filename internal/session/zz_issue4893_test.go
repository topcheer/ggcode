package session

import (
	"encoding/json"
	"github.com/topcheer/ggcode/internal/provider"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #4893: exports are data-fidelity surfaces - they must bypass the
// recent-message window (24h/500-record). A message record older than the
// window disappears from Load() but must survive LoadWithOptions(_, true)
// and reach ExportMarkdown output.
func TestIssue4893_ExportMarkdownBypassesWindow(t *testing.T) {
	dir := t.TempDir()
	st, err := NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	ses := NewSession("test", "test", "test")
	if err := st.Save(ses); err != nil {
		t.Fatal(err)
	}
	// Append one live message through the normal API...
	live := provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "LIVE-MESSAGE-4893"}}}
	if err := st.AppendMessageToDisk(ses, live); err != nil {
		t.Fatal(err)
	}
	// ...and hand-write one PRE-WINDOW message record with an old timestamp,
	// exactly the shape a >24h-old session has on disk.
	old := time.Now().Add(-48 * time.Hour)
	oldMsg := provider.Message{ID: "msg_pre_window_4893", Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "PRE-WINDOW-MESSAGE-4893"}}}
	rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &oldMsg, Timestamp: old}
	b, _ := json.Marshal(rec)
	f, err := os.OpenFile(st.sessionPath(ses.ID), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	f.Close()

	full, err := st.LoadWithOptions(ses.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Messages) != 2 {
		t.Fatalf("full load must carry both records, got %d", len(full.Messages))
	}

	md, err := st.ExportMarkdown(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "LIVE-MESSAGE-4893") {
		t.Fatal("ExportMarkdown must render the session's messages")
	}
	if !strings.Contains(md, "PRE-WINDOW-MESSAGE-4893") {
		t.Fatal("ExportMarkdown must render the PRE-WINDOW record (full-fidelity load)")
	}
}
