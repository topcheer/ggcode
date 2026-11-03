package tui

import (
	"strings"
	"testing"
)

func TestTUISlashUsageVendorAppended(t *testing.T) {
	m := newTestModel()
	d := tuiSlashDeps{m: &m}
	m.activeVendor = "zai"

	out, err := d.SessionUsageSummary()
	if err != nil {
		t.Fatalf("usage must never hard-error: %v", err)
	}

	if out == "" {
		t.Fatal("empty output")
	}
}

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
