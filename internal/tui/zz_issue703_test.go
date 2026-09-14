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

// #2312 ruling B retired /cost all: the .cost.json store had no live
// writer, so the rendering these tests pinned (mixed/all-unpriced rows)
// could only ever show legacy files. The no-fake-$0 semantics they
// protected were sound and live on in the per-session /cost rate gating;
// the retired command points at the #2150 replacement surface.
func TestIssue2312CostAllRetired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModel()
	m.handleCostAllCommand()

	out := lastSystemText703(&m)
	if out == "" {
		t.Fatal("no system message produced by /cost all")
	}
	if !strings.Contains(out, "#2150") {
		t.Errorf("retired command must point at the replacement surface; output:\n%s", out)
	}
	if strings.Contains(out, "$") {
		t.Errorf("retired command must not render dollar figures from dead data; output:\n%s", out)
	}
}
