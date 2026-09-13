package tui

// #2178 regression: the slack create input's ONLY purpose is entering
// bot/app tokens (`name xoxb-... xapp-...`, positional) - the echo was
// raw, so every keystroke (or a paste) went into the frame verbatim.
// The signal panel's same-shape input carries a phone number (PII).

import (
	"strings"
	"testing"
)

func TestMaskSlackCreateEcho(t *testing.T) {
	in := "mybot xoxb-1234567890-secret xapp-1-abcdef"
	got := maskSlackCreateEcho(in)
	if !strings.HasPrefix(got, "mybot ") {
		t.Fatalf("name field must stay readable: %q", got)
	}
	if strings.Contains(got, "xoxb-1234567890") || strings.Contains(got, "xapp-1-abcdef") {
		t.Fatalf("token fields leaked: %q", got)
	}
	// Name-only (mid-typing): verbatim.
	if got := maskSlackCreateEcho("mybot"); got != "mybot" {
		t.Fatalf("name-only must echo verbatim: %q", got)
	}
	// Buffer keeps the real value for the Enter parse (caller side).
	if got := maskSignalCreateEcho("mybase http://localhost:5050 +15551234567"); strings.Contains(got, "15551234567") {
		t.Fatalf("signal account (PII) leaked: %q", got)
	}
	if got := maskSignalCreateEcho("mybase http://localhost:5050"); !strings.Contains(got, "localhost:5050") {
		t.Fatalf("first two signal fields must stay readable: %q", got)
	}
}
