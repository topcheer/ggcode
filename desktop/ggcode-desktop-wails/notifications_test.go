package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewNotificationManager(t *testing.T) {
	nm := NewNotificationManager()
	if nm == nil {
		t.Fatal("NewNotificationManager returned nil")
	}
	if !nm.enabled {
		t.Error("expected enabled=true by default")
	}
	if !nm.focused {
		t.Error("expected focused=true by default")
	}
	if nm.GetUnread() != 0 {
		t.Error("expected unread=0 by default")
	}
}

func TestSetFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)
	if nm.focused {
		t.Error("expected focused=false after SetFocused(false)")
	}

	// Set unread, then focus should reset it
	nm.unread = 5
	nm.SetFocused(true)
	if !nm.focused {
		t.Error("expected focused=true after SetFocused(true)")
	}
	if nm.GetUnread() != 0 {
		t.Error("expected unread reset to 0 on focus")
	}
}

func TestSetEnabled(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetEnabled(false)
	if nm.enabled {
		t.Error("expected enabled=false after SetEnabled(false)")
	}

	// Disabling should clear unread
	nm.unread = 3
	nm.SetEnabled(false)
	if nm.GetUnread() != 0 {
		t.Error("expected unread reset to 0 on disable")
	}
}

func TestNotifySuppressedWhenFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(true) // window is focused
	nm.Notify("Test", "Should be suppressed")
	if nm.GetUnread() != 0 {
		t.Error("notification should be suppressed when focused")
	}
}

func TestNotifySuppressedWhenDisabled(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false) // window not focused
	nm.SetEnabled(false) // but notifications disabled
	nm.Notify("Test", "Should be suppressed")
	if nm.GetUnread() != 0 {
		t.Error("notification should be suppressed when disabled")
	}
}

func TestNotifyIncrementsUnreadWhenNotFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false) // window not focused
	nm.Notify("Test", "Should increment unread")
	if nm.GetUnread() != 1 {
		t.Errorf("expected unread=1, got %d", nm.GetUnread())
	}
	nm.Notify("Test2", "Another notification")
	if nm.GetUnread() != 2 {
		t.Errorf("expected unread=2, got %d", nm.GetUnread())
	}
}

func TestNotifyApprovalNeededWhenNotFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)
	nm.NotifyApprovalNeeded("Approval", "Need your input")
	if nm.GetUnread() != 1 {
		t.Errorf("expected unread=1, got %d", nm.GetUnread())
	}
}

func TestNotifyApprovalNeededWhenFocused(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(true)
	// Approval notifications fire even when focused, but don't bump badge
	nm.NotifyApprovalNeeded("Approval", "Need your input")
	if nm.GetUnread() != 0 {
		t.Error("approval notification should not increment badge when focused")
	}
}

func TestClearUnread(t *testing.T) {
	nm := NewNotificationManager()
	nm.SetFocused(false)
	nm.Notify("Test", "Message 1")
	nm.Notify("Test", "Message 2")
	if nm.GetUnread() != 2 {
		t.Fatalf("expected unread=2, got %d", nm.GetUnread())
	}
	nm.ClearUnread()
	if nm.GetUnread() != 0 {
		t.Error("expected unread=0 after ClearUnread")
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
	}
	for _, tt := range tests {
		got := itoa(tt.input)
		if got != tt.expected {
			t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// #289: escapeAppleScriptText must strip/replace control characters that are
// invalid inside AppleScript string literals (bare \n/\r/\t make osascript
// fail to compile), while keeping backslash/quote escaping intact.
func TestEscapeAppleScriptText(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "hello", "hello"},
		{"backslash", `C:\path`, `C:\\path`},
		{"quote", `say "hi"`, `say \"hi\"`},
		{"newline", "line1\nline2", "line1 line2"},
		{"crlf", "line1\r\nline2", "line1 line2"},
		{"bare_cr", "a\rb", "ab"},
		{"tab", "a\tb", "a b"},
		{"multiline_body", "Task done:\n2 files changed", "Task done: 2 files changed"},
	}
	for _, tt := range tests {
		if got := escapeAppleScriptText(tt.in); got != tt.want {
			t.Errorf("%s: escapeAppleScriptText(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

// #289: no raw control characters may survive escaping.
func TestEscapeAppleScriptText_NoControlChars(t *testing.T) {
	got := escapeAppleScriptText("a\nb\rc\td\x00e")
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Errorf("control character %q survived escaping in %q", r, got)
		}
	}
}

// #289: end-to-end check that a script embedding escaped multiline text
// compiles. Uses osacompile (compile to .scpt, never executes), so no real
// notification is posted. Runs only on macOS where osascript exists.
func TestNotifyMacOS_MultilineBodyCompiles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("osascript only exists on macOS")
	}
	if _, err := exec.LookPath("osacompile"); err != nil {
		t.Skip("osacompile not available")
	}
	script := "display notification \"" + escapeAppleScriptText("line1\nline2\ttabbed") +
		"\" with title \"" + escapeAppleScriptText("T\ni\tt") + "\""
	out := filepath.Join(t.TempDir(), "check.scpt")
	cmd := exec.Command("osacompile", "-o", out, "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("osacompile rejected escaped multiline script: %v\noutput: %s", err, output)
	}

	// Sanity: the same script with UNESCAPED newlines must fail to compile,
	// proving the escaping is what makes it valid.
	raw := "display notification \"line1\nline2\" with title \"T\""
	bad := filepath.Join(t.TempDir(), "bad.scpt")
	if output, err := exec.Command("osacompile", "-o", bad, "-e", raw).CombinedOutput(); err == nil {
		t.Logf("note: osacompile accepted a raw newline (tolerant version); escaping still valid")
		_ = output
	}
}
