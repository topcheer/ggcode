package sessionidx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #5710 regression pins: SetLiveID and TailMissingSources must apply the
// self budget (the 256MB-approx cap) so a transcript-heavy live session or
// host tail build cannot materialize an uncapped trigram index (observed as
// 1-3.3GB heapInuse on fresh instances within their first hour).

// writeTranscript appends n message lines of ~textKB text each to <dir>/<id>.jsonl
// in the real on-disk record shape consumed by indexLine.
func writeTranscript(t *testing.T, dir, id string, n, textKB int) {
	t.Helper()
	var sb strings.Builder
	ts := time.Unix(1700000000, 0)
	chunk := strings.Repeat("配置会话索引内存", 128) // ~3KB per repeat
	for i := 0; i < n; i++ {
		sb.Reset()
		sb.WriteString("{\"type\":\"message\",\"timestamp\":\"")
		sb.WriteString(ts.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano))
		sb.WriteString("\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"")
		written := 0
		for written < textKB*1024 {
			sb.WriteString(chunk)
			written += len(chunk)
		}
		sb.WriteString("\"}]}}\n")
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// SetLiveID over an over-cap transcript must end capped with the postings
// dropped (retroactive SetSelfBudget), not an uncapped full index.
func TestIssue5710SetLiveIDAppliesSelfBudget(t *testing.T) {
	// 40KB budget: approxBytes charges 40B per text byte, so ~1.1KB of
	// message text already exceeds it.
	t.Setenv("GGCODE_SESSIONIDX_SELF_BUDGET", "40960")
	dir := t.TempDir()
	id := "20261116-203000-aaaa1111bbbb2222"
	writeTranscript(t, dir, id, 2, 8) // 2 lines x 8KB text = 16KB text -> approx 640KB > 40KB

	m := NewManager(dir, "")
	m.SetLiveID(id)

	ix := m.Self()
	if ix == nil {
		t.Fatal("self index missing after SetLiveID")
	}
	if !ix.Capped() {
		t.Fatal("#5710: SetLiveID built an uncapped index for an over-budget transcript")
	}
	ix.mu.RLock()
	nTrig := len(ix.data.Trigrams)
	ix.mu.RUnlock()
	if nTrig != 0 {
		t.Fatalf("#5710: capped SetLiveID index retained %d trigrams (postings must be dropped)", nTrig)
	}
}

// Under the cap the live index still works - the budget must not over-block.
func TestIssue5710SetLiveIDIndexesUnderCap(t *testing.T) {
	t.Setenv("GGCODE_SESSIONIDX_SELF_BUDGET", "4194304") // 4MB: plenty for ~2KB text
	dir := t.TempDir()
	id := "20261116-203001-aaaa1111bbbb2222"
	writeTranscript(t, dir, id, 1, 1)

	m := NewManager(dir, "")
	m.SetLiveID(id)

	ix := m.Self()
	if ix == nil || ix.Capped() {
		t.Fatal("under-cap live session must be indexed without capping")
	}
	ix.mu.RLock()
	nTrig := len(ix.data.Trigrams)
	ix.mu.RUnlock()
	if nTrig == 0 {
		t.Fatal("under-cap live session indexed no trigrams")
	}
}

// Tail builds must skip sources whose raw size already implies an
// over-budget index (a capped build never persists, so retrying every cycle
// is churn), and must cap the ones it does build.
func TestIssue5710TailSkipsOverCapSources(t *testing.T) {
	// 40KB budget -> skip gate at 1024 raw bytes.
	t.Setenv("GGCODE_SESSIONIDX_SELF_BUDGET", "40960")
	dir := t.TempDir()
	small := "20261116-203002-aaaa1111bbbb2222"
	writeTranscript(t, dir, small, 1, 0) // one tiny line (~400 bytes raw)
	big := "20261116-203003-cccc3333dddd4444"
	writeTranscript(t, dir, big, 2, 8) // ~16KB raw: over the 1024B gate

	m := NewManager(dir, "")
	built := m.TailMissingSources(200 << 20)
	if built != 1 {
		t.Fatalf("#5710: tail built %d indexes, want 1 (only the under-gate source)", built)
	}
	// The small session's idx must exist on disk (persisted build).
	smallIdx := filepath.Join(dir, "idx", small+".idx")
	if _, err := os.Stat(smallIdx); err != nil {
		t.Fatalf("small source must get a persisted idx: %v", err)
	}
	// The big session must NOT get an idx, and a second pass must not retry it.
	bigIdx := filepath.Join(dir, "idx", big+".idx")
	if _, err := os.Stat(bigIdx); err == nil {
		t.Fatal("#5710: over-cap source must not be tail-built (uncapped index / churn)")
	}
	if again := m.TailMissingSources(200 << 20); again != 0 {
		t.Fatalf("#5710: second tail pass retried the skipped source (built %d)", again)
	}
}

func TestIssue5710MaxIndexableSourceBytes(t *testing.T) {
	if got := MaxIndexableSourceBytes(0); got != 0 {
		t.Fatalf("disabled budget must disable the gate, got %d", got)
	}
	if got := MaxIndexableSourceBytes(256 << 20); got != (256<<20)/40 {
		t.Fatalf("gate math wrong: %d", got)
	}
}
