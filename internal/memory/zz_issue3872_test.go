package memory

// #3872 probe: HealthReport.BudgetPercent must use the EFFECTIVE inline
// budget (EffectiveInlineBudget, LIMBO-scaled to [25%,100%] of the ceiling)
// as its denominator, not the ceiling constant maxTotalInlineBytes. With
// consumption rate below the floor the effective budget is ceiling/4,
// so a store can be fully saturated against its actual budget while the
// ceiling-based percentage reads at most 25% - understating saturation 4x
// and masking the right curation moment.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3872_BudgetPercentUsesEffectiveBudget(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	// Force the LIMBO floor: consumption rate far below floor ratio.
	// consumed must reach minConsumedSamples (2) to leave the fail-open
	// regime; rate 2/100 = 0.02 clamps to budgetFloorRatio 0.25.
	forceUses(t, am, "k-impl", 100)
	forceConsumption(t, am, "k-impl", 2)

	eff := am.EffectiveInlineBudget()
	if eff == 0 || eff >= maxTotalInlineBytes {
		t.Fatalf("expected floor-scaled budget in (0, ceiling), got %d (ceiling %d)", eff, maxTotalInlineBytes)
	}

	// Populate the store on disk so inline injection is non-zero and
	// measurable: one PERSISTENT entry (category is derived from the key
	// suffix by classifyMemory) sized at half the effective budget.
	content := "# probe\n\n" + strings.Repeat("x", eff/2)
	if werr := os.WriteFile(filepath.Join(dir, "probe-impl.md"), []byte(content), 0o644); werr != nil {
		t.Fatalf("seed entry: %v", werr)
	}

	rep := am.HealthReport("")
	if rep.InlineBytes == 0 {
		t.Fatalf("expected non-zero inline bytes; loadForPrompt returned empty (inline=%d)", rep.InlineEntries)
	}

	// BudgetPercent must be computed against the effective budget.
	want := rep.InlineBytes * 100 / eff
	if rep.BudgetPercent != want {
		t.Fatalf("BudgetPercent denominator must be EffectiveInlineBudget (%d): want %d%%, got %d%%", eff, want, rep.BudgetPercent)
	}
	// And it must differ from the old ceiling-based math, proving the fix.
	old := rep.InlineBytes * 100 / maxTotalInlineBytes
	if rep.BudgetPercent == old {
		t.Fatalf("percentage identical under both denominators (eff=%d ceiling=%d) - test not discriminating", eff, maxTotalInlineBytes)
	}
	if rep.EffectiveBudgetBytes != eff {
		t.Fatalf("EffectiveBudgetBytes not reported: want %d, got %d", eff, rep.EffectiveBudgetBytes)
	}

	// The formatted report must label bytes, not "token budget" (#3872 unit mix).
	if formatted := rep.FormatHealthReport(); !strings.Contains(formatted, "effective inline budget") {
		t.Fatalf("FormatHealthReport must label the denominator as the effective inline budget in bytes; got: %s", formatted)
	}
}
