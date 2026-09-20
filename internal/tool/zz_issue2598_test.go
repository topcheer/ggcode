package tool

import (
	"strings"
	"testing"
)

// #2598 case A: F-keys must resolve to escape sequences (keyboard events),
// not fall through to the regular-character branch that types literal "f5".
// kitty_impl.go is untagged (kitty ships on linux+macos), so no build tag.
func TestIssue2598_FKeysResolveToEscapeSeq(t *testing.T) {
	want := map[string]string{
		"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
		"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
		"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
	}
	for key, seq := range want {
		got, ok := kittyKeyEscapeSeq(key)
		if !ok || got != seq {
			t.Errorf("kittyKeyEscapeSeq(%q) = %q,%v want %q,true", key, got, ok, seq)
		}
	}
	// Unknown multi-character keys still report false (callers handle).
	if _, ok := kittyKeyEscapeSeq("zoom"); ok {
		t.Error("unknown key must stay unmapped")
	}
}

// #2598 case B: positional text payloads must be separated from options
// with "--" so "-"-prefixed text is sent as text, not parsed as an option.
func TestIssue2598_SendTextArgsCarrySeparator(t *testing.T) {
	args := kittySendTextArgs(0, "--help")
	want := []string{"send-text", "--", "--help"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args = %v, want %v", args, want)
	}
	args = kittySendTextArgs(7, "plain text")
	want = []string{"send-text", "--match=id:7", "--", "plain text"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args with match = %v, want %v", args, want)
	}
}
