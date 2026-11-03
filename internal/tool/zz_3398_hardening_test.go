package tool

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

func TestClickEffectNote_StateCollapseStillConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "true", "false")
	if !strings.Contains(note, "effect: confirmed (state: false)") {
		t.Fatalf("true->false collapse is a genuine effect and must confirm, got: %q", note)
	}
}

func TestClickEffectNote_StateAppearingConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "", "true")
	if !strings.Contains(note, "effect: confirmed (state: true)") {
		t.Fatalf("absent->present state change must confirm, got: %q", note)
	}
}

func TestClickEffectNote_StateReadFailureNeverConfirms(t *testing.T) {
	note := clickEffectNote("https://example.com/page", "https://example.com/page", "false", "")
	if strings.Contains(note, "effect: confirmed") {
		t.Fatalf("state read failure must not confirm, got: %q", note)
	}
}
