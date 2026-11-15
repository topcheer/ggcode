package sessionidx

import (
	"os"
	"testing"
)

// Non-host instances must not hold an unbounded decoded-external heap:
// NewManager now defaults totalBudget to DefaultTotalBudget (256MB) so the
// R1 admission/LRU paths activate for plain instances too.
func TestNewManagerHasTotalBudget(t *testing.T) {
	os.Unsetenv("GGCODE_SESSIONIDX_TOTAL_BUDGET")
	m := NewManager(t.TempDir(), "")
	if m.totalBudget <= 0 {
		t.Fatalf("NewManager totalBudget = %d, want >0 (default cap)", m.totalBudget)
	}
	if m.totalBudget != DefaultTotalBudgetBytes {
		t.Fatalf("NewManager totalBudget = %d, want %d", m.totalBudget, DefaultTotalBudgetBytes)
	}
}

func TestTotalBudgetEnvOverride(t *testing.T) {
	t.Setenv("GGCODE_SESSIONIDX_TOTAL_BUDGET", "1073741824")
	got := DefaultTotalBudget()
	if got != 1<<30 {
		t.Fatalf("DefaultTotalBudget with env = %d, want 1GiB", got)
	}
	t.Setenv("GGCODE_SESSIONIDX_TOTAL_BUDGET", "0")
	if DefaultTotalBudget() != 0 {
		t.Fatal("env=0 must explicitly disable the cap (host escape hatch)")
	}
	t.Setenv("GGCODE_SESSIONIDX_TOTAL_BUDGET", "garbage")
	if DefaultTotalBudget() != DefaultTotalBudgetBytes {
		t.Fatal("invalid env must fall back to default")
	}
}

// Budget-capped admission: an external idx larger than totalBudget is
// skipped at scan time (never decoded).
func TestScanExternalSkipsOverBudget(t *testing.T) {
	os.Unsetenv("GGCODE_SESSIONIDX_TOTAL_BUDGET")
	dir := t.TempDir()
	m := NewManager(dir, "")
	m.totalBudget = 1024 // 1KB: nothing fits
	m.scanExternal()
	for _, e := range m.external {
		if e.decoded {
			t.Fatal("entry decoded despite totalBudget cap")
		}
	}
}
