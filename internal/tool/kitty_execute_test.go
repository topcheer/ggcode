package tool

// Kitty tool dispatch coverage (sa-141). Detection-only paths; no kitten
// RPC is ever issued (kitty is installed here, so actions are avoided).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestKittyExecuteValidationSa141(t *testing.T) {
	k := NewKittyTool(t.TempDir())
	ctx := context.Background()

	r, err := k.Execute(ctx, json.RawMessage(`{bad`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("invalid JSON -> (%+v,%v)", r, err)
	}
	r, err = k.Execute(ctx, json.RawMessage(`{"action":" "}`))
	if err != nil || !r.IsError || r.Content != "action is required" {
		t.Fatalf("missing action -> (%+v,%v)", r, err)
	}

	// Force not-detected: both signals cleared.
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	r, err = k.Execute(ctx, json.RawMessage(`{"action":"status"}`))
	if err != nil || r.IsError || !strings.Contains(r.Content, "kitty: not detected") {
		t.Fatalf("status not detected -> (%+v,%v)", r, err)
	}
	r, err = k.Execute(ctx, json.RawMessage(`{"action":"list"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "Kitty is not detected") {
		t.Fatalf("action not detected -> (%+v,%v)", r, err)
	}

	// Detected: status reports platform and binary without RPC.
	t.Setenv("TERM_PROGRAM", "kitty")
	r, err = k.Execute(ctx, json.RawMessage(`{"action":"status"}`))
	if err != nil || r.IsError || !strings.Contains(r.Content, "kitty: detected") {
		t.Fatalf("status detected -> (%+v,%v)", r, err)
	}
	if !strings.Contains(r.Content, "platform:") {
		t.Fatalf("platform line missing: %s", r.Content)
	}
	r, err = k.Execute(ctx, json.RawMessage(`{"action":"bogus_kitty_action"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "unsupported kitty action") {
		t.Fatalf("unknown action -> (%+v,%v)", r, err)
	}
}

func TestKittyAvailableDetectionSa141(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	if kittyAvailable() {
		t.Fatal("kittyAvailable() = true with no signals")
	}
	t.Setenv("KITTY_WINDOW_ID", "3")
	if !kittyAvailable() {
		t.Fatal("kittyAvailable() = false with KITTY_WINDOW_ID set")
	}
}
