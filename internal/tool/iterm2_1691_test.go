package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #1691 case 2: control characters in input text must be REJECTED with an
// explicit error, not silently stripped by escapeAS (corrupted delivery).
func TestIterm2InputControlCharRejected1691(t *testing.T) {
	var it Iterm2Tool
	res := it.executeInput(context.Background(), "s1", "ok\x1b[Atext")
	if !res.IsError {
		t.Fatal("control char in input must be an explicit error")
	}
	if !strings.Contains(res.Content, "U+") {
		t.Fatalf("error must name the character, got: %s", res.Content)
	}
	// Plain text unaffected - checked at the VALIDATOR level only (no
	// real AppleScript from unit tests).
	if bad := asUnsupportedControl("plain text \t tab \n nl \r ok"); bad != 0 {
		t.Fatalf("tab/newline/CR must stay allowed, got U+%04X", bad)
	}
}

// #1691 case 2: set_title boundary.
func TestIterm2TitleControlCharRejected1691(t *testing.T) {
	var it Iterm2Tool
	res := it.executeSetTitle(context.Background(), "s1", "bad\x07title")
	if !res.IsError {
		t.Fatal("control char in title must be an explicit error")
	}
}

// #1691 case 3: waitForHealthy must honor ctx cancellation.
type imHealthSnapStub struct{ healthy bool }

func TestIMWaitForHealthyCtxCancel1691(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A stub tool without a Manager: waitForHealthy returns before
	// touching Snapshot when ctx is already cancelled.
	var it IMTool
	if it.waitForHealthy(ctx, "tg", 15*0+time.Millisecond) {
		t.Fatal("cancelled ctx must abort immediately")
	}
}
