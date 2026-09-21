package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"encoding/json"

	"github.com/topcheer/ggcode/internal/provider"
)

func transferTestSession(t *testing.T, store *JSONLStore, title string) *Session {
	t.Helper()
	ses := &Session{
		ID:        generateID(),
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Title:     title,
		Workspace: "/tmp/source-ws",
		Vendor:    "zai",
		Endpoint:  "coding",
		Model:     "glm-4.7",
	}
	if err := store.Save(ses); err != nil {
		t.Fatalf("Save: %v", err)
	}
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello " + title}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi there"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "second question"}}},
	}
	for _, m := range msgs {
		if err := store.AppendMessageToDisk(ses, m); err != nil {
			t.Fatalf("AppendMessageToDisk: %v", err)
		}
		// Mirror the production flow (contextManager keeps in-memory
		// messages; AppendMessageToDisk only persists) so meta writes pass
		// the HasUserInteraction guard.
		ses.Messages = append(ses.Messages, m)
	}
	return ses
}

func mustStore(t *testing.T, dir string) *JSONLStore {
	t.Helper()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore(%s): %v", dir, err)
	}
	return store
}

// Export → import round trip preserves the full message history and makes
// the session resumable in the importing store under a remapped workspace.
func TestTransferRoundTrip(t *testing.T) {
	src := mustStore(t, filepath.Join(t.TempDir(), "src"))
	dst := mustStore(t, filepath.Join(t.TempDir(), "dst"))
	ses := transferTestSession(t, src, "roundtrip")

	bundlePath := filepath.Join(t.TempDir(), "s.ggsession")
	got, err := src.ExportBundle(ses.ID, bundlePath)
	if err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	if got != bundlePath {
		t.Fatalf("ExportBundle returned %q, want %q", got, bundlePath)
	}
	if info, err := os.Stat(bundlePath); err != nil || info.Size() == 0 {
		t.Fatalf("bundle missing or empty: %v", err)
	}

	imported, err := dst.ImportBundleFile(bundlePath, ImportOptions{Workspace: "/tmp/target-ws"})
	if err != nil {
		t.Fatalf("ImportBundleFile: %v", err)
	}
	if imported.ID != ses.ID {
		t.Fatalf("imported ID = %q, want original %q (no collision expected)", imported.ID, ses.ID)
	}
	if imported.Workspace != "/tmp/target-ws" {
		t.Fatalf("imported workspace = %q, want remapped", imported.Workspace)
	}
	if len(imported.Messages) != 3 {
		t.Fatalf("imported messages = %d, want 3", len(imported.Messages))
	}
	if !strings.Contains(imported.Messages[0].Content[0].Text, "roundtrip") {
		t.Fatalf("first message text lost: %+v", imported.Messages[0])
	}

	// Session must be visible to the workspace-filtered listing (resume path).
	listed, err := dst.ListForWorkspace("/tmp/target-ws")
	if err != nil {
		t.Fatalf("ListForWorkspace: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != ses.ID {
		t.Fatalf("ListForWorkspace = %d entries, want the imported session", len(listed))
	}

	// Index entry should carry the authoritative on-disk message count.
	idx, err := dst.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	if len(idx) != 1 || idx[0].MsgCount != 3 {
		t.Fatalf("index entry MsgCount = %+v, want 3", idx)
	}
}

// Importing the same bundle twice must never overwrite the first import.
func TestTransferImportCollisionMintsNewID(t *testing.T) {
	src := mustStore(t, filepath.Join(t.TempDir(), "src"))
	ses := transferTestSession(t, src, "collide")
	bundlePath := filepath.Join(t.TempDir(), "s.ggsession")
	if _, err := src.ExportBundle(ses.ID, bundlePath); err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}

	dst := mustStore(t, filepath.Join(t.TempDir(), "dst"))
	first, err := dst.ImportBundleFile(bundlePath, ImportOptions{KeepWorkspace: true})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	second, err := dst.ImportBundleFile(bundlePath, ImportOptions{KeepWorkspace: true})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("collision not handled: both imports share ID %s", first.ID)
	}
	listed, err := dst.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List = %d entries, want 2", len(listed))
	}
	// Original workspace preserved with KeepWorkspace.
	if second.Workspace != "/tmp/source-ws" {
		t.Fatalf("KeepWorkspace not honored: %q", second.Workspace)
	}
}

// Export of a pinned/tagged session preserves those flags through import.
func TestTransferPreservesPinnedAndTags(t *testing.T) {
	src := mustStore(t, filepath.Join(t.TempDir(), "src"))
	ses := transferTestSession(t, src, "meta")
	if err := src.SetPinned(ses, true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	if err := src.SetTags(ses, []string{"rust", "perf"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	bundlePath := filepath.Join(t.TempDir(), "s.ggsession")
	if _, err := src.ExportBundle(ses.ID, bundlePath); err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}
	dst := mustStore(t, filepath.Join(t.TempDir(), "dst"))
	if _, err := dst.ImportBundleFile(bundlePath, ImportOptions{KeepWorkspace: true}); err != nil {
		t.Fatalf("ImportBundleFile: %v", err)
	}
	idx, err := dst.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	if len(idx) != 1 {
		t.Fatalf("index entries = %d, want 1", len(idx))
	}
	if !idx[0].Pinned {
		t.Fatal("pinned flag lost in transfer")
	}
	if len(idx[0].Tags) != 2 {
		t.Fatalf("tags lost in transfer: %v", idx[0].Tags)
	}
}

// Malformed bundles must fail cleanly without leaving partial session files.
func TestTransferImportRejectsCorruptBundle(t *testing.T) {
	dst := mustStore(t, filepath.Join(t.TempDir(), "dst"))
	bundlePath := filepath.Join(t.TempDir(), "bad.ggsession")
	bundle := TransferBundle{
		Version:   TransferBundleVersion,
		SessionID: generateID(),
		Records:   []string{`{"type":"meta"`, `{not json`},
	}
	if err := writeGzipAtomic(bundlePath, &bundle); err != nil {
		t.Fatalf("writing corrupt bundle: %v", err)
	}
	if _, err := dst.ImportBundleFile(bundlePath, ImportOptions{KeepWorkspace: true}); err == nil {
		t.Fatal("import of corrupt bundle succeeded, want error")
	}
	entries, _ := os.ReadDir(dst.Dir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			t.Fatalf("partial session file left behind: %s", e.Name())
		}
	}
}

// Path-traversal IDs in a hostile bundle must not escape the store dir.
func TestTransferImportSanitizesHostileID(t *testing.T) {
	dst := mustStore(t, filepath.Join(t.TempDir(), "dst"))
	bundlePath := filepath.Join(t.TempDir(), "evil.ggsession")
	bundle := TransferBundle{
		Version:   TransferBundleVersion,
		SessionID: "../../evil",
		Records: []string{
			`{"type":"meta","session_id":"../../evil","title":"evil"}`,
			`{"type":"message","session_id":"../../evil","message":{"role":"user","content":[{"type":"text","text":"x"}]}}`,
		},
	}
	if err := writeGzipAtomic(bundlePath, &bundle); err != nil {
		t.Fatalf("writing bundle: %v", err)
	}
	ses, err := dst.ImportBundleFile(bundlePath, ImportOptions{KeepWorkspace: true})
	if err != nil {
		t.Fatalf("ImportBundleFile: %v", err)
	}
	if ses.ID == "../../evil" || strings.Contains(ses.ID, "/") {
		t.Fatalf("hostile ID not sanitized: %q", ses.ID)
	}
	if _, err := os.Stat(filepath.Join(dst.Dir(), "..", "evil.jsonl")); err == nil {
		t.Fatal("bundle escaped the store directory")
	}
}

// Plain-JSON (uncompressed) bundles remain importable.
func TestTransferImportAcceptsPlainJSON(t *testing.T) {
	src := mustStore(t, filepath.Join(t.TempDir(), "src"))
	ses := transferTestSession(t, src, "plain")

	raw, err := os.ReadFile(src.sessionPath(ses.ID))
	if err != nil {
		t.Fatalf("reading source session: %v", err)
	}
	bundle := TransferBundle{
		Version:         TransferBundleVersion,
		SessionID:       ses.ID,
		Title:           ses.Title,
		SourceWorkspace: ses.Workspace,
		MessageCount:    countMessageRecords(splitNonEmptyLines(raw)),
		Records:         splitNonEmptyLines(raw),
	}
	bundlePath := filepath.Join(t.TempDir(), "plain.json")
	plain, err := json.Marshal(&bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := writeFileAtomic(bundlePath, plain); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	dst := mustStore(t, filepath.Join(t.TempDir(), "dst"))
	if _, err := dst.ImportBundleFile(bundlePath, ImportOptions{KeepWorkspace: true}); err != nil {
		t.Fatalf("ImportBundleFile: %v", err)
	}
}

// Exporting a missing session reports ErrSessionNotFound.
func TestTransferExportMissingSession(t *testing.T) {
	store := mustStore(t, filepath.Join(t.TempDir(), "s"))
	_, err := store.ExportBundle("does-not-exist", "")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
}
