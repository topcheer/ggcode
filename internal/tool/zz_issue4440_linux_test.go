//go:build linux

package tool

import (
	"context"
	"testing"
)

func TestIssue4440LinuxModifierClickRejectsUnknownButton(t *testing.T) {
	_, err := executeDesktopControl(context.Background(), desktopParams{
		Action: "modifier_click", X: 10, Y: 10, Text: "ctrl", Button: "bogus",
	})
	if err == nil {
		t.Fatal("unknown button must be rejected, got nil error")
	}
}
