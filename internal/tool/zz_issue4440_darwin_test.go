//go:build darwin

package tool

import (
	"context"
	"testing"
)

// #4440 Bug 1 (darwin half): modifier_click silently left-clicked for
// "middle" and any unknown button (the #1665/#3876 fixes covered only the
// plain click). The unknown-button error fires BEFORE any event is posted,
// so this test has no side effects.
func TestIssue4440DarwinModifierClickRejectsUnknownButton(t *testing.T) {
	_, err := modifierClick(context.Background(), 10, 10, "ctrl", "bogus")
	if err == nil {
		t.Fatal("unknown button must be rejected, got nil error")
	}
}
