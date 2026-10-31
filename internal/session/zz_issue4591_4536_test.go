package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMixedCpSession crafts a raw session JSONL (same harness style as
// TestMigrateOldCheckpoint) and returns the loaded Session.
func writeMixedCpSession(t *testing.T, lines []string) *Session {
	t.Helper()
	dir, _ := os.MkdirTemp("", "ggcode_mixed_cp_*")
	t.Cleanup(func() { os.RemoveAll(dir) })
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ses := NewSession("zai", "ep", "model")
	ses.Workspace = "/test"
	f, err := os.Create(filepath.Join(dir, ses.ID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	all := append([]string{`{"type":"meta","session_id":"` + ses.ID + `","workspace":"/test"}`}, lines...)
	if _, err := f.Write([]byte(strings.Join(all, "\n") + "\n")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loaded, err := store.Load(ses.ID)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func ctxText(s *Session) string {
	var b strings.Builder
	for _, m := range s.ContextMessages {
		for _, c := range m.Content {
			b.WriteString(c.Text)
			b.WriteString("|")
		}
	}
	return b.String()
}

// TestIssue4591_LegacyCheckpointDropsStaleSummaryId: new-format cp1 with its
// summary message flushed AFTER a later legacy cp2 - without the fix the stale
// cp1 summary pointer survives cp2 and wins the context rebuild, serving
// pre-cp2 state over cp2's fresher snapshot.
func TestIssue4591_LegacyCheckpointDropsStaleSummaryId(t *testing.T) {
	loaded := writeMixedCpSession(t, []string{
		`{"type":"checkpoint","checkpoint_summary_msg_id":"cp1sum","checkpoint_last_msg_id":"m1","checkpoint_tokens":100}`,
		`{"type":"message","message":{"id":"m1","role":"user","content":[{"type":"text","text":"pre-cp2 msg"}]}}`,
		`{"type":"checkpoint","checkpoint_messages":[{"role":"system","content":[{"type":"text","text":"CP2-SNAPSHOT"}]}],"checkpoint_tokens":200}`,
		// cp1's summary record lands AFTER cp2's line (async flush window).
		`{"type":"message","message":{"id":"cp1sum","role":"system","content":[{"type":"text","text":"STALE-CP1-SUMMARY"}]}}`,
		`{"type":"message","message":{"id":"m2","role":"user","content":[{"type":"text","text":"after cp2"}]}}`,
	})
	got := ctxText(loaded)
	// The defect was authority, not record visibility: the stale summary must
	// not be the context BASE. cp2's snapshot must lead; the late-flushed cp1
	// summary record still appears as an ordinary post-checkpoint message
	// (it IS a log line after cp2) - that is log-faithful, not stale context.
	if !strings.HasPrefix(got, "CP2-SNAPSHOT|") {
		t.Fatalf("context must be rebuilt from the latest (legacy cp2) snapshot, got:\n%s", got)
	}
	if !strings.Contains(got, "after cp2") {
		t.Fatalf("post-checkpoint message missing from context:\n%s", got)
	}
}

// TestIssue4536_NewCheckpointDropsStaleLegacySnapshot: legacy cp then a
// new-format cp whose summary message record is lost - without the fix the
// legacy snapshot survives and silently resurrects pre-new-format context.
func TestIssue4536_NewCheckpointDropsStaleLegacySnapshot(t *testing.T) {
	loaded := writeMixedCpSession(t, []string{
		`{"type":"checkpoint","checkpoint_messages":[{"role":"system","content":[{"type":"text","text":"STALE-LEGACY-SNAPSHOT"}]}],"checkpoint_tokens":100}`,
		`{"type":"message","message":{"id":"m1","role":"user","content":[{"type":"text","text":"before new cp"}]}}`,
		// New-format checkpoint whose summary message (id "lost") never made
		// it to disk (crash-torn / corrupted tail).
		`{"type":"checkpoint","checkpoint_summary_msg_id":"lost","checkpoint_last_msg_id":"m1","checkpoint_tokens":200}`,
		`{"type":"message","message":{"id":"m2","role":"user","content":[{"type":"text","text":"after new cp"}]}}`,
	})
	got := ctxText(loaded)
	if strings.Contains(got, "STALE-LEGACY-SNAPSHOT") {
		t.Fatalf("lost new-format summary must not resurrect the stale legacy snapshot:\n%s", got)
	}
	if !strings.Contains(got, "after new cp") {
		t.Fatalf("recent messages must still be visible via the no-checkpoint path:\n%s", got)
	}
}
