package tool

// Validation-path coverage for iterm2.go (sa-141). Interactive AppleScript
// execution is intentionally NOT exercised - only deterministic branches.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestIterm2CloneNilSafetySa141(t *testing.T) {
	var nilTool *Iterm2Tool
	cloned := nilTool.Clone()
	if cloned == nil {
		t.Fatal("nil Clone() returned nil")
	}
	orig := NewIterm2Tool("/tmp/wd")
	c2, ok := orig.Clone().(*Iterm2Tool)
	if !ok || c2.WorkingDir != "/tmp/wd" {
		t.Fatalf("Clone() = %+v, want preserved WorkingDir", c2)
	}
	if orig.Name() != "iterm2" {
		t.Fatalf("Name() = %q", orig.Name())
	}
}

func TestIterm2WorkingDirSa141(t *testing.T) {
	t2 := &Iterm2Tool{WorkingDir: "/custom/dir"}
	if got := t2.workingDir(); got != "/custom/dir" {
		t.Fatalf("workingDir = %q, want /custom/dir", got)
	}
	// Blank WorkingDir falls back to process cwd (non-empty).
	if got := (&Iterm2Tool{}).workingDir(); got == "" || got == "." {
		t.Fatalf("workingDir fallback = %q, want process cwd", got)
	}
}

func TestCountDroppedControlRunesSa141(t *testing.T) {
	if got := countDroppedControlRunes("a\x01b\tc\nd\re\x7f"); got != 1 {
		// 0x7f (DEL) is >= 0x20 so not counted; only \x01 counts.
		t.Fatalf("countDroppedControlRunes = %d, want 1", got)
	}
	if got := countDroppedControlRunes("clean"); got != 0 {
		t.Fatalf("countDroppedControlRunes(clean) = %d, want 0", got)
	}
}

func TestIterm2ExecuteValidationSa141(t *testing.T) {
	t2 := NewIterm2Tool(t.TempDir())
	ctx := context.Background()

	// Malformed JSON.
	r, err := t2.Execute(ctx, json.RawMessage(`{bad json`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "invalid input") {
		t.Fatalf("invalid JSON -> (%+v,%v)", r, err)
	}

	// Missing action.
	r, err = t2.Execute(ctx, json.RawMessage(`{"description":"x"}`))
	if err != nil || !r.IsError || r.Content != "action is required" {
		t.Fatalf("missing action -> (%+v,%v)", r, err)
	}

	// Outside iTerm2 (TERM_PROGRAM forced away from iTerm.app): deterministic
	// not-detected error, no AppleScript is spawned.
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	r, err = t2.Execute(ctx, json.RawMessage(`{"action":"list","description":"x"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "iTerm2 is not detected") {
		t.Fatalf("not detected -> (%+v,%v)", r, err)
	}

	// Inside iTerm2 an unknown action reaches the switch default without
	// spawning AppleScript.
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	r, err = t2.Execute(ctx, json.RawMessage(`{"action":"bogus_action","description":"x"}`))
	if err != nil || !r.IsError || !strings.Contains(r.Content, "unsupported iterm2 action") {
		t.Fatalf("unsupported action -> (%+v,%v)", r, err)
	}
}
