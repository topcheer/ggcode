//go:build darwin

package tool

import (
	"context"
	"testing"
)

func TestIssue4440DarwinModifierClickRejectsUnknownButton(t *testing.T) {
	_, err := modifierClick(context.Background(), 10, 10, "ctrl", "bogus")
	if err == nil {
		t.Fatal("unknown button must be rejected, got nil error")
	}
}
