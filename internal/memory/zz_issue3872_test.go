package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3872_BudgetPercentUsesEffectiveBudget(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	forceUses(t, am, "k-impl", 100)
	forceConsumption(t, am, "k-impl", 2)

	eff := am.EffectiveInlineBudget()
	if eff == 0 || eff >= maxTotalInlineBytes {
		t.Fatalf("expected floor-scaled budget in (0, ceiling), got %d (ceiling %d)", eff, maxTotalInlineBytes)
	}

	content := "# probe\n\n" + strings.Repeat("x", eff/2)
	if werr := os.WriteFile(filepath.Join(dir, "probe-impl.md"), []byte(content), 0o644); werr != nil {
		t.Fatalf("seed entry: %v", werr)
	}

	rep := am.HealthReport("")
	if rep.InlineBytes == 0 {
		t.Fatalf("expected non-zero inline bytes; loadForPrompt returned empty (inline=%d)", rep.InlineEntries)
	}

	want := rep.InlineBytes * 100 / eff
	if rep.BudgetPercent != want {
		t.Fatalf("BudgetPercent denominator must be EffectiveInlineBudget (%d): want %d%%, got %d%%", eff, want, rep.BudgetPercent)
	}

	old := rep.InlineBytes * 100 / maxTotalInlineBytes
	if rep.BudgetPercent == old {
		t.Fatalf("percentage identical under both denominators (eff=%d ceiling=%d) - test not discriminating", eff, maxTotalInlineBytes)
	}
	if rep.EffectiveBudgetBytes != eff {
		t.Fatalf("EffectiveBudgetBytes not reported: want %d, got %d", eff, rep.EffectiveBudgetBytes)
	}

	if formatted := rep.FormatHealthReport(); !strings.Contains(formatted, "effective inline budget") {
		t.Fatalf("FormatHealthReport must label the denominator as the effective inline budget in bytes; got: %s", formatted)
	}
}
