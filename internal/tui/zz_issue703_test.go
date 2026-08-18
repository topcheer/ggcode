package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
)

// Issue #703: the /cost all display layer never consumed HasPricing —
// unpriced sessions rendered a fake-precise "$0.0000" in the per-session
// list (violating the #683 single-session contract on the aggregate path),
// and an all-unpriced grand total led with "$0.0000 (partial: N ...)"
// instead of disclosing that pricing coverage was zero.

// write703CostFile writes one .cost.json snapshot into HOME/.ggcode/cost/.
func write703CostFile(t *testing.T, home, name, json string) {
	t.Helper()
	dir := filepath.Join(home, ".ggcode", "cost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(json), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// lastSystemText703 returns the text of the most recent SystemItem in the
// model's chat list — /cost all writes its report as a system message.
func lastSystemText703(m *Model) string {
	n := m.chatList.Len()
	for i := n - 1; i >= 0; i-- {
		if s, ok := m.chatList.ItemAt(i).(*chat.SystemItem); ok {
			return s.Text()
		}
	}
	return ""
}

// TestIssue703_CostAllUnpricedRowShowsNoPricingData pins the mixed case:
// a priced session renders its dollar figure, an unpriced session renders
// "(no pricing data)" — never "$0.0000" — and the grand total stays a
// partial-sum annotation because pricing coverage is nonzero.
func TestIssue703_CostAllUnpricedRowShowsNoPricingData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	write703CostFile(t, home, "priced.cost.json",
		`{"provider":"openai","model":"gpt-4o","input_tokens":1000,"output_tokens":500,"total_cost_usd":1.5,"has_pricing":true}`)
	write703CostFile(t, home, "unpriced.cost.json",
		`{"provider":"custom","model":"mystery-7b","input_tokens":2000,"output_tokens":100,"total_cost_usd":0}`)

	m := newTestModel()
	m.handleCostAllCommand()

	out := lastSystemText703(&m)
	if out == "" {
		t.Fatal("no system message produced by /cost all")
	}
	if !strings.Contains(out, "(no pricing data)") {
		t.Errorf("unpriced session row must show \"(no pricing data)\"; output:\n%s", out)
	}
	if !strings.Contains(out, "$1.50") {
		t.Errorf("priced session row must show $1.50; output:\n%s", out)
	}
	if strings.Contains(out, "$0.0000") {
		t.Errorf("fake-precise $0.0000 must not appear for unpriced sessions; output:\n%s", out)
	}
	if !strings.Contains(out, "partial: 1 sessions without pricing data") {
		t.Errorf("mixed case grand total must stay a partial-sum annotation; output:\n%s", out)
	}
}

// TestIssue703_CostAllFullyUnpricedTotalDropsDollars pins the all-unpriced
// case: when EVERY session lacks pricing, the grand total must replace the
// dollar figure entirely (agg.HasPricing is the wired hook) instead of
// leading with "$0.0000".
func TestIssue703_CostAllFullyUnpricedTotalDropsDollars(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	write703CostFile(t, home, "a.cost.json",
		`{"provider":"custom","model":"mystery-7b","input_tokens":1000,"output_tokens":100,"total_cost_usd":0}`)
	write703CostFile(t, home, "b.cost.json",
		`{"provider":"other","model":"unknown-xl","input_tokens":500,"output_tokens":50,"total_cost_usd":0}`)

	m := newTestModel()
	m.handleCostAllCommand()

	out := lastSystemText703(&m)
	if out == "" {
		t.Fatal("no system message produced by /cost all")
	}
	if !strings.Contains(out, "(no pricing data for any session)") {
		t.Errorf("all-unpriced grand total must print \"(no pricing data for any session)\"; output:\n%s", out)
	}
	if strings.Contains(out, "$0.0000") {
		t.Errorf("all-unpriced grand total must not lead with a fake-precise $0.0000; output:\n%s", out)
	}
}
