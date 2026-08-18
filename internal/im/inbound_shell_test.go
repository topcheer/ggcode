package im

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestIsInboundShellCommand(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"$ ls -la", true},
		{"! go build ./...", true},
		{"  $ echo hi  ", true},
		{"$", false},        // bare prefix, no command
		{"! ", false},       // whitespace-only command
		{"/restart", false}, // slash route, not shell
		{"hello $world", false},
		{"plain message", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsInboundShellCommand(tc.in); got != tc.want {
			t.Errorf("IsInboundShellCommand(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSplitInboundShellCommand(t *testing.T) {
	cmd, ok := splitInboundShellCommand("  $ git status  ")
	if !ok || cmd != "git status" {
		t.Fatalf("splitInboundShellCommand = (%q, %v), want (\"git status\", true)", cmd, ok)
	}
	if _, ok := splitInboundShellCommand("no prefix"); ok {
		t.Fatal("non-prefixed text should not split")
	}
}

func TestRouteInboundText_ShellRoute(t *testing.T) {
	// Shell passthrough must route BEFORE approval/ask/message so it runs
	// immediately even while the agent is mid-turn (TUI-parity UX).
	r := RouteInboundText("$ go test ./...", true, true)
	if r.Kind != InboundRouteShell {
		t.Fatalf("busy-agent route: got %q, want shell (immediate execution, not queued)", r.Kind)
	}
	r = RouteInboundText("! ls", false, false)
	if r.Kind != InboundRouteShell {
		t.Fatalf("idle route: got %q, want shell", r.Kind)
	}
	// Slash still wins over shell prefixes (slash checked first).
	r = RouteInboundText("/model", true, true)
	if r.Kind != InboundRouteSlash {
		t.Fatalf("slash priority: got %q, want slash", r.Kind)
	}
	// Approval reply with a $ in it must NOT hijack the approval channel…
	// wait: "$ y" would be treated as shell. That is the documented trade-off:
	// approval replies are y/n/a - they never start with $ or !.
}

func TestHandleShellInbound_ExecutesAndPushesResult(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
	b.handleShellInbound("$ printf imshell-ok")

	// handleShellInbound runs async; poll briefly for the result message.
	for i := 0; i < 100; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "imshell-ok") {
				return // pass
			}
		}
		sleepMs(20)
	}
	t.Fatal("shell result was not pushed back to the emitter")
}

// captureEmitter records EmitText calls for assertions.
type captureEmitter struct {
	mu    sync.Mutex
	texts []string
}

func (c *captureEmitter) EmitText(s string) error {
	c.mu.Lock()
	c.texts = append(c.texts, s)
	c.mu.Unlock()
	return nil
}

func sleepMs(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func TestHandleShellInbound_UsageHintOnBarePrefix(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
	b.handleShellInbound("$")
	em.mu.Lock()
	defer em.mu.Unlock()
	if len(em.texts) == 0 || !strings.Contains(em.texts[0], "Usage") {
		t.Fatalf("expected usage hint, got %v", em.texts)
	}
}

// waitForShellResult polls until predicate matches an emitted text or fails.
func waitForShellResult(t *testing.T, em *captureEmitter, pred func(string) bool, what string) string {
	t.Helper()
	for i := 0; i < 150; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if pred(tx) {
				return tx
			}
		}
		sleepMs(20)
	}
	t.Fatalf("%s was not pushed back", what)
	return ""
}

// TestHandleShellInbound_TruncationPreservesUTF8 (issue #727): truncating at a
// byte cap must not split a multi-byte rune: Telegram rejects invalid UTF-8
// with a 400 and the result would never reach the user.
func TestHandleShellInbound_TruncationPreservesUTF8(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
	// 1200 CJK chars = ~3600 bytes > 3500 cap; cap lands mid-rune.
	body := strings.Repeat("中", 1200)
	b.handleShellInbound("$ printf '%s' '" + body + "'")

	out := waitForShellResult(t, em, func(s string) bool { return strings.Contains(s, "truncated") }, "truncated CJK output")
	if !utf8.ValidString(out) {
		t.Fatalf("truncated output is not valid UTF-8: %q", out[:80])
	}
	if !strings.Contains(out, "中") {
		t.Fatal("expected CJK content in truncated output")
	}
}

func TestHandleShellInbound_TruncationEnglish(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
	b.handleShellInbound("$ printf '%s' " + strings.Repeat("a", 4000))

	out := waitForShellResult(t, em, func(s string) bool { return strings.Contains(s, "truncated") }, "truncated English output")
	if !utf8.ValidString(out) {
		t.Fatal("English truncated output must be valid UTF-8")
	}
	if !strings.Contains(out, strings.Repeat("a", 100)) {
		t.Fatal("expected body content in truncated output")
	}
}

func TestHandleShellInbound_ExactlyAtCapNotTruncated(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
	b.handleShellInbound("$ printf '%s' " + strings.Repeat("b", inboundShellMaxOutput))

	out := waitForShellResult(t, em, func(s string) bool { return strings.Contains(s, "exit") }, "at-cap output")
	if strings.Contains(out, "truncated") {
		t.Fatal("output exactly at cap must not be truncated")
	}
}

func TestHandleShellInbound_NonzeroExitPushesOutput(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
	b.handleShellInbound("$ sh -c 'echo boom >&2; exit 3'")

	for i := 0; i < 100; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "boom") && strings.Contains(tx, "exit 3") {
				return // pass: stderr captured + exit code surfaced
			}
		}
		sleepMs(20)
	}
	t.Fatal("stderr/exit-code output was not pushed back")
}
