package knight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

	if last, ok := s.LastFor("project", "Skill-B"); !ok || last.Name != "skill-b" {
		t.Fatalf("LastFor after corrupt load = (%+v, %v), want skill-b", last, ok)
	}
}

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
