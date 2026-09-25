package runeval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordFromReport(t *testing.T) {
	r := Report{
		EfficiencyScore:     72,
		TurnCount:           5,
		ToolCalls:           18,
		ToolErrors:          2,
		WastedRepeatCalls:   3,
		OverheadTokens:      1000,
		TotalTokens:         10000,
		WastedTokenEstimate: 250,
	}
	at := time.Unix(1700000000, 0)
	rec := RecordFromReport(r, "sess-1234", at)
	if rec.At != 1700000000 {
		t.Errorf("At = %d, want 1700000000", rec.At)
	}
	if rec.SessionID != "sess-1234" || rec.Score != 72 || rec.Turns != 5 ||
		rec.ToolCalls != 18 || rec.ToolErrors != 2 || rec.WastedRepeats != 3 ||
		rec.WastedTokens != 250 || rec.TotalTokens != 10000 {
		t.Errorf("unexpected record: %+v", rec)
	}
	if rec.OverheadPct != 10 {
		t.Errorf("OverheadPct = %v, want 10", rec.OverheadPct)
	}
	zero := RecordFromReport(Report{}, "", at)
	if zero.OverheadPct != 0 {
		t.Errorf("OverheadPct with no tokens = %v, want 0", zero.OverheadPct)
	}
}

func TestAppendLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runreports.jsonl")
	if _, err := LoadHistory(path, 0); err != nil {
		t.Fatalf("missing store should not error: %v", err)
	}
	base := time.Unix(1700000000, 0)
	for i, score := range []int{80, 65, 90} {
		rec := ReportRecord{At: base.Add(time.Duration(i) * time.Minute).Unix(),
			SessionID: "s", Score: score}
		if err := AppendReport(path, rec); err != nil {
			t.Fatalf("AppendReport: %v", err)
		}
	}
	recs, err := LoadHistory(path, 0)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(recs) != 3 || recs[0].Score != 80 || recs[2].Score != 90 {
		t.Fatalf("unexpected history: %+v", recs)
	}
	// limit returns the most recent records, oldest first.
	tail, err := LoadHistory(path, 2)
	if err != nil {
		t.Fatalf("LoadHistory limit: %v", err)
	}
	if len(tail) != 2 || tail[0].Score != 65 || tail[1].Score != 90 {
		t.Fatalf("unexpected tail: %+v", tail)
	}
}

func TestLoadHistorySkipsCorruptLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runreports.jsonl")
	content := "{\"at\":1,\"score\":50}\nnot json\n\n{\"at\":2,\"score\":60}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err := LoadHistory(path, 0)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(recs) != 2 || recs[0].Score != 50 || recs[1].Score != 60 {
		t.Fatalf("unexpected records: %+v", recs)
	}
}

func TestRenderDeltaNoBaseline(t *testing.T) {
	if got := RenderDelta(Report{EfficiencyScore: 70}, ReportRecord{}); got != "" {
		t.Errorf("RenderDelta with zero baseline = %q, want empty", got)
	}
}

func TestRenderDeltaShowsRegression(t *testing.T) {
	prev := ReportRecord{At: time.Now().Add(-2 * time.Hour).Unix(),
		SessionID: "abcdef123456", Score: 80, ToolErrors: 0, WastedRepeats: 1,
		OverheadPct: 10, TotalTokens: 100}
	cur := Report{EfficiencyScore: 65, ToolErrors: 3, WastedRepeatCalls: 4,
		OverheadTokens: 5000, TotalTokens: 20000}
	got := RenderDelta(cur, prev)
	for _, want := range []string{"score 80 → 65 (-15)", "tool errors 0 → 3",
		"wasted repeats 1 → 4", "overhead 10% → 25%", "abcdef12"} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderDelta missing %q in: %s", want, got)
		}
	}
}

func TestRenderDeltaImprovement(t *testing.T) {
	prev := ReportRecord{At: time.Now().Unix(), Score: 60}
	got := RenderDelta(Report{EfficiencyScore: 85}, prev)
	if !strings.Contains(got, "score 60 → 85 (+25)") {
		t.Errorf("RenderDelta = %q, want +25 improvement", got)
	}
	if strings.Contains(got, "tool errors") {
		t.Errorf("unchanged metrics should be omitted: %s", got)
	}
}

func TestRenderHistory(t *testing.T) {
	if got := RenderHistory(nil); !strings.Contains(got, "No recorded runs yet") {
		t.Errorf("empty history = %q", got)
	}
	var recs []ReportRecord
	base := time.Unix(1700000000, 0)
	for i := 0; i < 12; i++ {
		recs = append(recs, ReportRecord{At: base.Add(time.Duration(i) * time.Minute).Unix(),
			Score: 50 + i, ToolCalls: 10 + i})
	}
	got := RenderHistory(recs)
	if !strings.Contains(got, "last 10") {
		t.Errorf("history should cap display at 10: %s", got)
	}
	if !strings.Contains(got, "score  52") || strings.Contains(got, "score  50") {
		t.Errorf("history should show only newest rows: %s", got)
	}
	// Rows carry MM-DD HH:MM timestamps regardless of timezone.
	if !strings.Contains(got, "sess -") {
		t.Errorf("history rows should include session column: %s", got)
	}
}
