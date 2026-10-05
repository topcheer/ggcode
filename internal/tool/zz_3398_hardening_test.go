package tool

// #3398 follow-up hardening probes (verdict 5990361368): an empty urlBefore
// baseline (Location failed pre-click) must never produce "effect:
// confirmed (navigation ...)" - that marker unlocks the agent-layer spiral
// gate, so a zero-evidence confirm is the wrong direction entirely.

import (
	"context"
	"strings"
	"testing"
)

func TestClickEffectNote_EmptyBaselineNeverConfirms(t *testing.T) {
	b := &Browser{}
	note := b.clickEffectNote(context.Background(), "#btn", "", "https://example.com/after")
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("empty baseline must not confirm navigation, got: %q", note)
	}
	if !strings.Contains(note, "no observable effect") {
		t.Fatalf("empty baseline should fall through to the guidance note, got: %q", note)
	}
}

func TestClickEffectNote_RealNavigationStillConfirms(t *testing.T) {
	b := &Browser{}
	note := b.clickEffectNote(context.Background(), "#btn", "https://example.com/before", "https://example.com/after")
	if !strings.Contains(note, "effect: confirmed (navigation to https://example.com/after)") {
		t.Fatalf("genuine navigation must still confirm, got: %q", note)
	}
}
