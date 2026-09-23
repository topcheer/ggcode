package tool

// Pure-helper coverage for kitty_impl.go (sa-141).

import (
	"reflect"
	"testing"
)

func TestKittyMatchIDSa141(t *testing.T) {
	if got := matchID(5); got != "id:5" {
		t.Fatalf("matchID(5) = %q, want id:5", got)
	}
	if got := matchID(0); got != "" {
		t.Fatalf("matchID(0) = %q, want empty (focused window)", got)
	}
	if got := matchID(-3); got != "" {
		t.Fatalf("matchID(-3) = %q, want empty (guard against negative-id fallthrough)", got)
	}
}

func TestKittyErrInvalidWindowIDSa141(t *testing.T) {
	r := errInvalidWindowID(-2)
	if !r.IsError || r.Content == "" {
		t.Fatalf("errInvalidWindowID(-2) = %+v, want error result", r)
	}
}

func TestKittySendTextArgsSa141(t *testing.T) {
	got := kittySendTextArgs(7, "ls -la")
	want := []string{"send-text", "--match=id:7", "--", "ls -la"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kittySendTextArgs(7) = %v, want %v", got, want)
	}
	// windowID 0 targets focused window: no --match flag, but "--" separator
	// must still guard dash-prefixed text from being parsed as options.
	got = kittySendTextArgs(0, "--stdin")
	if !reflect.DeepEqual(got, []string{"send-text", "--", "--stdin"}) {
		t.Fatalf("kittySendTextArgs(0, --stdin) = %v, want [send-text -- --stdin] with match omitted", got)
	}
}

func TestKittyKeyEscapeSeqSa141(t *testing.T) {
	cases := map[string]string{
		"enter": "\r", "return": "\r", "tab": "\t",
		"escape": "\x1b", "esc": "\x1b", "backspace": "\x7f",
		"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
		"home": "\x1b[H", "end": "\x1b[F",
		"pageup": "\x1b[5~", "pagedown": "\x1b[6~", "delete": "\x1b[3~",
		"space": " ",
		"f1":    "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
		"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
		"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
	}
	for key, want := range cases {
		got, ok := kittyKeyEscapeSeq(key)
		if !ok || got != want {
			t.Errorf("kittyKeyEscapeSeq(%q) = (%q,%v), want (%q,true)", key, got, ok, want)
		}
	}
	// Unknown keys (e.g. typed text) fall through to the regular-char branch.
	if got, ok := kittyKeyEscapeSeq("a"); ok || got != "" {
		t.Fatalf("kittyKeyEscapeSeq(a) = (%q,%v), want (\"\",false)", got, ok)
	}
}
