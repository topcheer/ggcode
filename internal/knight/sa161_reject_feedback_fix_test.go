package knight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRejectLines writes a JSONL file mixing encoded entries and raw strings.
func writeRejectLines(t *testing.T, path string, items ...any) {
	t.Helper()
	var buf []byte
	for _, item := range items {
		switch v := item.(type) {
		case string:
			buf = append(buf, []byte(v+"\n")...)
		case rejectFeedbackEntry:
			line, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			buf = append(buf, line...)
			buf = append(buf, '\n')
		default:
			t.Fatalf("unsupported item type %T", item)
		}
	}
	if err := os.WriteFile(path, buf, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestSa161LoadSkipsCorruptLinesAndAlwaysTrims verifies the sa-161 fix: a
// corrupt line no longer makes load() discard every trailing entry, and the
// end-of-load trimOld runs even when corruption was seen.
func TestSa161LoadSkipsCorruptLinesAndAlwaysTrims(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stale := rejectFeedbackEntry{Time: now.Add(-rejectCoolDownWindow - time.Hour), Name: "old-skill", Action: "reject"}
	freshA := rejectFeedbackEntry{Time: now.Add(-2 * time.Hour), Name: "skill-a", Action: "reject"}
	freshB := rejectFeedbackEntry{Time: now.Add(-time.Hour), Name: "skill-b", Action: "rollback"}

	path := filepath.Join(dir, "rej.jsonl")
	writeRejectLines(t, path, stale, "{broken json", freshA, `{"time":"not-a-time"}`, freshB)
	s := newRejectFeedbackStore(path)
	s.load()
	if len(s.entries) != 2 || s.entries[0].Name != "skill-a" || s.entries[1].Name != "skill-b" {
		t.Fatalf("load = %+v, want corrupt lines skipped and stale trimmed, only [skill-a skill-b]", s.entries)
	}

	// User-visible surface: recovered entries remain queryable.
	if last, ok := s.LastFor("project", "Skill-B"); !ok || last.Name != "skill-b" {
		t.Fatalf("LastFor after corrupt load = (%+v, %v), want skill-b", last, ok)
	}
}

// TestSa161LoadTornTailLine covers the crash-mid-append case: a torn final
// line must not discard the earlier valid entries.
func TestSa161LoadTornTailLine(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	fresh := rejectFeedbackEntry{Time: now.Add(-time.Hour), Name: "keep-me", Action: "reject"}
	line, err := json.Marshal(fresh)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	torn := append(append([]byte{}, line[:len(line)/2]...), []byte("...")...)
	path := filepath.Join(dir, "torn.jsonl")
	body := append(append(append([]byte{}, line...), '\n'), torn...)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := newRejectFeedbackStore(path)
	s.load()
	if len(s.entries) != 1 || s.entries[0].Name != "keep-me" {
		t.Fatalf("load = %+v, want [keep-me]", s.entries)
	}
}

// TestSa161LoadAllCorruptIdempotent: an all-garbage file loads to zero
// entries without panicking, and repeated loads stay idempotent.
func TestSa161LoadAllCorruptIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.jsonl")
	if err := os.WriteFile(path, []byte("not json\n{bad}\n\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := newRejectFeedbackStore(path)
	s.load()
	if len(s.entries) != 0 {
		t.Fatalf("all-corrupt load = %+v, want zero entries", s.entries)
	}
	s.load()
	if len(s.entries) != 0 {
		t.Fatalf("second load = %+v, want idempotent zero entries", s.entries)
	}
	if got := s.Recent(10); len(got) != 0 {
		t.Fatalf("Recent = %+v, want empty", got)
	}
}

// TestSa161RecentExcludesExpiredDespiteCorruption is the regression for the
// originally reported symptom: with a corrupt line present, the old early
// return skipped trimOld, so an expired entry stayed in Recent() for the
// whole process lifetime.
func TestSa161RecentExcludesExpiredDespiteCorruption(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	expired := rejectFeedbackEntry{Time: now.Add(-rejectCoolDownWindow - 24*time.Hour), Name: "expired-skill", Action: "reject"}
	path := filepath.Join(dir, "expired.jsonl")
	writeRejectLines(t, path, expired, "<corrupt>")
	s := newRejectFeedbackStore(path)
	s.load()
	if got := s.Recent(10); len(got) != 0 {
		t.Fatalf("Recent() after corrupt load = %+v, want expired entry trimmed (early return used to skip trimOld)", got)
	}
}
