//go:build linux

package tool

import (
	"context"
	"testing"
)

// #4440 Bug 1 (linux half): modifier_click silently left-clicked for
// "middle" and any unknown button. The unknown-button error fires BEFORE
// xdotool is invoked, so this test has no side effects.
func TestIssue4440LinuxModifierClickRejectsUnknownButton(t *testing.T) {
	_, err := executeDesktopControl(context.Background(), desktopParams{
		Action: "modifier_click", X: 10, Y: 10, Text: "ctrl", Button: "bogus",
	})
	if err == nil {
		t.Fatal("unknown button must be rejected, got nil error")
	}
}
