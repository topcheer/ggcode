package sessionidx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #5149 self-cap pins: a live session whose index outgrows the resident
// cap must stop ingesting postings (heap stays bounded), keep advancing
// IndexedTo (no re-decode churn), and degrade recall queries to a bounded
// source scan so results stay CORRECT.

func seedSelfCapSession(t *testing.T, dir, id string, lines int) string {
	t.Helper()
	src := filepath.Join(dir, id+".jsonl")
	var b strings.Builder
	for i := 0; i < lines; i++ {
		b.WriteString(`{"type":"message","timestamp":"2026-01-01T00:00:` + pad2(i%60) + `Z","message":{"role":"user","content":[{"type":"text","text":"unicorns graze quietly in meadow number ` + itoa(i) + `"}]}}` + "\n")
	}
	if err := os.WriteFile(src, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return src
}

func pad2(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func TestSelfCap_StopsIngestionAndKeepsTailing(t *testing.T) {
	dir := t.TempDir()
	const id = "selfcap1"
	src := seedSelfCapSession(t, dir, id, 40)

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// 64 bytes admits ~1 posting; 40 distinct lines must trip the cap.
	ix.SetSelfBudget(64)
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !ix.Capped() {
		t.Fatal("expected capped=true after budget exceeded")
	}
	if got := ix.Size(); got > 64+80*40 { // last admitted line may overshoot by one line's worth
		t.Fatalf("Size()=%d, budget blown beyond one-line slack", got)
	}

	// Second Refresh: IndexedTo already at EOF -> no-op; still capped.
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh2: %v", err)
	}
	if !ix.Capped() {
		t.Fatal("cap flag must persist")
	}

	// Query on the capped index itself returns nothing (degraded signal).
	if hits := ix.Query([]string{"unicorns"}, 10); len(hits) != 0 {
		t.Fatalf("capped Index.Query must return nil, got %d", len(hits))
	}
	_ = src
}

func TestSelfCap_ManagerQueryDegradesToSourceScan(t *testing.T) {
	dir := t.TempDir()
	const id = "selfcap2"
	seedSelfCapSession(t, dir, id, 40)

	m := NewManager(dir, id)
	m.Start()
	defer m.Stop()

	// Find the self index and cap it hard (simulates a huge transcript).
	self := m.Self()
	if self == nil {
		t.Fatal("self index nil")
	}
	self.SetSelfBudget(64)
	if err := self.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !self.Capped() {
		t.Fatal("self must be capped")
	}

	// Recall must still FIND the term - via the bounded source scan.
	res := m.Query([]string{"unicorns"}, "", 10)
	if len(res) == 0 {
		t.Fatal("capped self query must degrade to source scan and still match")
	}
	for _, r := range res {
		if !strings.Contains(r.Line, "unicorns") {
			t.Fatalf("source-scan hit missing term: %q", r.Line[:min(len(r.Line), 80)])
		}
	}
}

func TestSelfCap_UnlimitedDefaultUnaffected(t *testing.T) {
	dir := t.TempDir()
	const id = "selfcap3"
	seedSelfCapSession(t, dir, id, 10)

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// No SetSelfBudget -> 0 -> uncapped trigram path unchanged.
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if ix.Capped() {
		t.Fatal("uncapped index must not be flagged capped")
	}
	if hits := ix.Query([]string{"unicorns"}, 10); len(hits) == 0 {
		t.Fatal("uncapped query must hit via trigrams")
	}
}

func TestDefaultSelfBudget_EnvOverride(t *testing.T) {
	t.Setenv("GGCODE_SESSIONIDX_SELF_BUDGET", "1048576")
	if got := DefaultSelfBudget(); got != 1<<20 {
		t.Fatalf("env override: got %d", got)
	}
	t.Setenv("GGCODE_SESSIONIDX_SELF_BUDGET", "notanumber")
	if got := DefaultSelfBudget(); got != 256<<20 {
		t.Fatalf("bad env must fall back to default, got %d", got)
	}
}

func TestSelfCap_SkipDecodeForOversizedIdx(t *testing.T) {
	dir := t.TempDir()
	const id = "selfcap4"
	seedSelfCapSession(t, dir, id, 10)

	// Simulate an oversized persisted idx without writing 64MB: shrink the
	// skip threshold, then write a file bigger than it.
	old := idxSkipDecodeBytes
	idxSkipDecodeBytes = 1024
	defer func() { idxSkipDecodeBytes = old }()
	big := make([]byte, 4096)
	if err := os.MkdirAll(IdxDir(dir), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(IdxPath(dir, id), big, 0o600); err != nil {
		t.Fatalf("write idx: %v", err)
	}

	ix, err := Load(dir, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !ix.Capped() {
		t.Fatal("oversized idx must load capped (decode skipped)")
	}
	if ix.Size() != 0 {
		t.Fatalf("no postings may be decoded, Size=%d", ix.Size())
	}
	// Refresh is a no-op: no ingestion, capped persists.
	if err := ix.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !ix.Capped() {
		t.Fatal("cap must persist across refresh")
	}
}
