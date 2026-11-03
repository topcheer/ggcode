package tool

import (
	"os"
	"strings"
	"testing"
)

func toolSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Skipf("source not available: %v", err)
	}
	return string(b)
}

func TestIssue2594_Iterm2InputPinsNewlineNO(t *testing.T) {
	src := toolSource(t, "iterm2_darwin.go")
	if !strings.Contains(src, `write text "%s" newline NO`) {
		t.Error("iterm2WriteText script must pin `newline NO` - without it iTerm2 defaults to appending Return (#2594)")
	}
}

func TestIssue2597_KittyActionFocusErrorChecked(t *testing.T) {
	src := toolSource(t, "kitty_impl.go")

	start := strings.Index(src, "func (k *KittyTool) executeAction(")
	if start < 0 {
		t.Fatal("executeAction not found")
	}
	end := len(src)
	if next := strings.Index(src[start+10:], "\nfunc "); next >= 0 {
		end = start + 10 + next
	}
	body := src[start:end]
	if strings.Contains(body, `_, _ = kittyAtCtx(ctx, "focus-window"`) {
		t.Error("executeAction still discards the focus-window error (#2597) - wrong-window action with fake success")
	}
	if !strings.Contains(body, "focusing window") {
		t.Error("executeAction must surface focus failure like executeZoom does (#1692 case 6 twin site)")
	}
}
