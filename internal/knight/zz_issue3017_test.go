package knight

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue3017_RecordAccountsMemoryDespiteWriteFailure(t *testing.T) {

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

	if err := b.Record("task2", 100, 100); err == nil {
		t.Fatal("second record must also report the persist error")
	}
	if got := b.Used(); got != 700 {
		t.Fatalf("accumulation across failing writes broken, Used()=%d want 700", got)
	}

	if b.CanSpend() {

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

	b2 := NewBudget(dir, config.KnightConfig{DailyTokenBudget: 1000})
	if got := b2.Used(); got != 15 {
		t.Fatalf("persisted record must reload for a fresh budget, Used()=%d want 15", got)
	}
}
