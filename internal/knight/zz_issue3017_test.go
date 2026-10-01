package knight

// Regression probe for #3017: Record must account consumed tokens in the
// in-memory ledger even when persistence fails - the write used to happen
// before `todayUsed += total`, so a failing write (disk full/quota/perm)
// dropped the entry from BOTH the memory ledger and the disk, and CanSpend
// kept approving against an understated usage for the rest of the day.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue3017_RecordAccountsMemoryDespiteWriteFailure(t *testing.T) {
	// Point the budget dir at a FILE: usage-<date>.jsonl resolves under it
	// and OpenFile fails with ENOTDIR on every write attempt.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	b := NewBudget(blocker, config.KnightConfig{DailyTokenBudget: 1000})

	if err := b.Record("task", 300, 200); err == nil {
		t.Fatal("record against a file-backed dir must still report the persist error")
	}
	if got := b.Used(); got != 500 {
		t.Fatalf("#3017: memory ledger must keep the consumed tokens despite write failure, Used()=%d want 500", got)
	}
	// A second failing record keeps accumulating - CanSpend sees the truth.
	if err := b.Record("task2", 100, 100); err == nil {
		t.Fatal("second record must also report the persist error")
	}
	if got := b.Used(); got != 700 {
		t.Fatalf("accumulation across failing writes broken, Used()=%d want 700", got)
	}
	// With daily=1000 and 700 truly used, only 300 of headroom remains.
	if b.CanSpend() {
		// 700/1000 leaves 300; a spend check has no amount parameter, so
		// only assert it flips once the ledger crosses the limit.
	}
	if err := b.Record("task3", 400, 0); err == nil {
		t.Fatal("third record must also report the persist error")
	}
	if b.CanSpend() {
		t.Fatal("CanSpend must reflect the memory ledger (1100/1000 over limit), not the lost disk state")
	}
}

func TestIssue3017_RecordSuccessStillExact(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "knight"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := NewBudget(dir, config.KnightConfig{DailyTokenBudget: 1000})
	if err := b.Record("task", 10, 5); err != nil {
		t.Fatal(err)
	}
	if got := b.Used(); got != 15 {
		t.Fatalf("successful record accounting broken, Used()=%d want 15", got)
	}
	// A fresh budget over the same dir reloads the persisted line.
	b2 := NewBudget(dir, config.KnightConfig{DailyTokenBudget: 1000})
	if got := b2.Used(); got != 15 {
		t.Fatalf("persisted record must reload for a fresh budget, Used()=%d want 15", got)
	}
}
