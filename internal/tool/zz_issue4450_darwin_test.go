//go:build darwin

package tool

import (
	"context"
	"strings"
	"testing"
)

func TestIssue4450_EscMapKeyWithModifiersRejected(t *testing.T) {
	tool := &Iterm2Tool{}
	for _, key := range []string{"enter", "escape", "f1", "up", "tab"} {
		for _, mods := range []string{"ctrl", "control", "alt", "cmd"} {
			res := tool.executeSendKey(context.Background(), "", key, mods)
			if !res.IsError {
				t.Fatalf("%s+%s must be rejected (was: bare sequence + false success, #4450)", mods, key)
			}
			if !strings.Contains(res.Content, "unsupported key combo") {
				t.Fatalf("%s+%s error should name the combo, got: %s", mods, key, res.Content)
			}
		}
	}

	res := tool.executeSendKey(context.Background(), "definitely-missing-session", "enter", "")
	if res.IsError && strings.Contains(res.Content, "unsupported key combo") {
		t.Fatal("unmodified escMap key must not hit the modifier gate")
	}
}

func TestIssue4450_ResizeInvalidAxisRejected(t *testing.T) {
	tool := &Iterm2Tool{}
	for _, axis := range []string{"width", "diag", "HorizontalX"} {
		res := tool.executeResize(context.Background(), "", axis, 20)
		if !res.IsError {
			t.Fatalf("axis %q must be rejected (was: silent vertical resize + echoed typo, #4450)", axis)
		}
		if !strings.Contains(res.Content, "invalid axis") {
			t.Fatalf("axis %q error should name it, got: %s", axis, res.Content)
		}
	}
}
