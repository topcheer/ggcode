package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4715: ts-less records BEFORE the first real timestamp used to be stamped
// firstReal+1ms..+Nms - AFTER the real record that follows them in file
// order, breaking the file-order==chronological-order invariant (#198) the
// backfill exists to preserve. The prefix must be anchored K+1ms BELOW
// firstReal; the suffix (zero-ts after a real record) keeps the #4670
// max-tracking behavior and lands after its predecessor.
func TestIssue4715_PrefixBackfillLandsBeforeFirstReal(t *testing.T) {
	dir := t.TempDir()
	s, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	real := time.Now().Add(-12 * time.Hour).Truncate(time.Millisecond)

	var lines []string
	for i := 0; i < 3; i++ { // zero-ts prefix
		rec := jsonlRecord{Type: "message", SessionID: "sess", Message: &provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("prefix %d", i)}},
		}}
		b, _ := json.Marshal(rec)
		lines = append(lines, string(b))
	}
	recReal := jsonlRecord{Type: "message", SessionID: "sess", Timestamp: real, Message: &provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "real"}},
	}}
	b, _ := json.Marshal(recReal)
	lines = append(lines, string(b))
	suffix := jsonlRecord{Type: "message", SessionID: "sess", Message: &provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "suffix"}},
	}}
	b, _ = json.Marshal(suffix)
	lines = append(lines, string(b))

	path := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(path, []byte(joinLines(lines)), 0o600); err != nil {
		t.Fatal(err)
	}

	s.backfillTimestamps("sess")

	got := readMsgTimestamps(t, path)
	if len(got) != 5 {
		t.Fatalf("expected 5 message records, got %d", len(got))
	}
	// Prefix strictly increasing and all strictly BEFORE the real record.
	for i := 0; i < 3; i++ {
		if got[i].IsZero() {
			t.Fatalf("prefix record %d still has zero ts", i)
		}
		if !got[i].Before(real) {
			t.Fatalf("prefix record %d stamped %v, must be before first real %v (#4715 direction)", i, got[i], real)
		}
	}
	if !got[0].Before(got[1]) || !got[1].Before(got[2]) {
		t.Fatalf("prefix timestamps must be strictly increasing, got %v", got[:3])
	}
	// Real record keeps its timestamp; suffix lands after it (#4670).
	if !got[3].Equal(real) {
		t.Fatalf("real record timestamp must be untouched, got %v want %v", got[3], real)
	}
	if !got[4].After(real) {
		t.Fatalf("suffix must land after the real record, got %v vs %v", got[4], real)
	}
	// Global monotonicity across the whole file.
	for i := 1; i < len(got); i++ {
		if !got[i].After(got[i-1]) {
			t.Fatalf("file order must be chronological: record %d (%v) not after %d (%v)", i, got[i], i-1, got[i-1])
		}
	}
}

// All-zero-ts sessions keep the historical now-6h anchor (no firstReal to
// count a prefix against).
func TestIssue4715_AllZeroKeepsNowAnchor(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewJSONLStore(dir)
	var lines []string
	for i := 0; i < 2; i++ {
		rec := jsonlRecord{Type: "message", SessionID: "sess2", Message: &provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: fmt.Sprintf("m %d", i)}},
		}}
		b, _ := json.Marshal(rec)
		lines = append(lines, string(b))
	}
	path := filepath.Join(dir, "sess2.jsonl")
	os.WriteFile(path, []byte(joinLines(lines)), 0o600)

	s.backfillTimestamps("sess2")

	got := readMsgTimestamps(t, path)
	if got[0].IsZero() || got[1].IsZero() {
		t.Fatalf("all-zero session must still be backfilled: %v", got)
	}
	floor := time.Now().Add(-6 * time.Hour)
	if got[0].Before(floor.Add(-time.Minute)) {
		t.Fatalf("all-zero anchor drifted below now-6h: %v", got[0])
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func readMsgTimestamps(t *testing.T, path string) []time.Time {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []time.Time
	for _, line := range splitLines(string(data)) {
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "message" {
			continue
		}
		out = append(out, rec.Timestamp)
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
