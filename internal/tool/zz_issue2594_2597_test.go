package tool

// #2594 / #2597 regression pins.
//
// #2594: iTerm2's `write text` AppleScript command defaults newline=YES,
// turning the pure `input` action into "type text AND press Return" (kitty
// send-text and ghostty input text are pure input; execution belongs to
// send_key). The generated script must pin `newline NO`.
//
// #2597: the #1692 case-6 fix (8859765c) gave executeZoom a focus-window
// error check but missed the twin site in executeAction - a discarded focus
// error made the action run on the WRONG window with a fake success. The
// discard pattern must not come back.

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
	if !strings.Contains(src, "#2594") {
		t.Error("newline NO rationale comment (#2594) missing")
	}
}

func TestIssue2597_KittyActionFocusErrorChecked(t *testing.T) {
	src := toolSource(t, "kitty_impl.go")
	// Isolate the executeAction function body.
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
