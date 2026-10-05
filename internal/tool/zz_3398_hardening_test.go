package tool

// #3398 follow-up hardening probes (verdict 5990361368): an empty urlBefore
// baseline (Location failed pre-click) must never produce "effect:
// confirmed (navigation ...)" - that marker unlocks the agent-layer spiral
// gate, so a zero-evidence confirm is the wrong direction entirely.
// #3407: the same zero-evidence discipline now covers the STATE branch -
// a non-empty-but-unchanged state attribute (aria-expanded="false" is a
// truthy JS string) must not confirm either; only a change versus the
// pre-click baseline does.

import (
	"strings"
	"testing"
)

func TestClickEffectNote_EmptyBaselineNeverConfirms(t *testing.T) {
	note := clickEffectNote("", "https://example.com/after", "", "")
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("empty baseline must not confirm navigation, got: %q", note)
	}
	if !strings.Contains(note, "no observable effect") {
		t.Fatalf("empty baseline should fall through to the guidance note, got: %q", note)
	}
}

func TestClickEffectNote_RealNavigationStillConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/before", "https://example.com/after", "", "")
	if !strings.Contains(note, "effect: confirmed (navigation to https://example.com/after)") {
		t.Fatalf("genuine navigation must still confirm, got: %q", note)
	}
}

// #3407 core regression: aria-expanded="false" both before AND after the
// click (collapsed menu, click swallowed by overlay/preventDefault) is a
// truthy non-empty JS string - the old code emitted "effect: confirmed
// (state: false)" on zero evidence and unlocked the spiral gate.
func TestClickEffectNote_UnchangedAriaFalseMustNotConfirm(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "false", "false")
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("#3407: unchanged aria-expanded=\"false\" must not confirm (zero-evidence marker), got: %q", note)
	}
	if !strings.Contains(note, "no observable effect") {
		t.Fatalf("swallowed click should produce guidance, got: %q", note)
	}
}

func TestClickEffectNote_StateExpandConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "false", "true")
	if !strings.Contains(note, "effect: confirmed (state: true)") {
		t.Fatalf("false->true expansion must confirm, got: %q", note)
	}
}

// Legitimate collapse (menu toggled open->closed) is a real state CHANGE
// and must stay confirmed - the naive "exclude false from truthy" fix
// would break this case, which is why baseline comparison is the fix.
func TestClickEffectNote_StateCollapseStillConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "true", "false")
	if !strings.Contains(note, "effect: confirmed (state: false)") {
		t.Fatalf("true->false collapse is a genuine effect and must confirm, got: %q", note)
	}
}

// Attribute appearing after the click (element gained state) is a change.
func TestClickEffectNote_StateAppearingConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "", "true")
	if !strings.Contains(note, "effect: confirmed (state: true)") {
		t.Fatalf("absent->present state change must confirm, got: %q", note)
	}
}

// State evaluation failing after the click (empty reading) must never
// confirm even when stateBefore was non-empty - conservative direction.
func TestClickEffectNote_StateReadFailureNeverConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "false", "")
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("state read failure must not confirm, got: %q", note)
	}
}
