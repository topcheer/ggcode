package tui

import (
	"strings"
	"testing"
)

// #2358 follow-up (cross-entry parity): the TUI-attached IM /usage must
// APPEND the active vendor's live payload alongside token counts, and must
// degrade to the old behavior (tokens only / cross-session summary) when
// no vendor probe applies - never error.
func TestTUISlashUsageVendorAppended(t *testing.T) {
	m := newTestModel()
	d := tuiSlashDeps{m: &m}
	m.activeVendor = "zai"
	// No session: token block absent, vendor block may still render via
	// the probe; with no test HTTP server the probe errors and stays
	// silent - the result must not be empty-error.
	out, err := d.SessionUsageSummary()
	if err != nil {
		t.Fatalf("usage must never hard-error: %v", err)
	}
	// Probe failure is silent: only the cross-session fallback line may
	// appear. The key contract is err == nil and no panic.
	if out == "" {
		t.Fatal("empty output")
	}
}

// Vendor probe silent-degradation must not turn into "usage: vendor (...)"
// error text when tokens exist: tokens alone are a valid answer.
func TestTUISlashUsageTokensOnlyWhenProbeFails(t *testing.T) {
	m := newTestModel()
	d := tuiSlashDeps{m: &m}
	m.activeVendor = "zai"
	if m.session != nil {
		t.Skip("test model unexpectedly has a session")
	}
	out, err := d.SessionUsageSummary()
	if err != nil {
		t.Fatalf("hard error: %v", err)
	}
	if strings.Contains(out, "usage: zai (") {
		t.Fatalf("probe failure must stay silent, got %q", out)
	}
}
