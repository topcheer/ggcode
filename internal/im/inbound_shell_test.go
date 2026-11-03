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
		{"$", false},
		{"! ", false},
		{"/restart", false},
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

	r := RouteInboundText("$ go test ./...", true, true)
	if r.Kind != InboundRouteShell {
		t.Fatalf("busy-agent route: got %q, want shell (immediate execution, not queued)", r.Kind)
	}
	r = RouteInboundText("! ls", false, false)
	if r.Kind != InboundRouteShell {
		t.Fatalf("idle route: got %q, want shell", r.Kind)
	}

	r = RouteInboundText("/model", true, true)
	if r.Kind != InboundRouteSlash {
		t.Fatalf("slash priority: got %q, want slash", r.Kind)
	}

}

func TestHandleShellInbound_ExecutesAndPushesResult(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}
	b.handleShellInbound("$ printf imshell-ok")

	for i := 0; i < 100; i++ {
		em.mu.Lock()
		texts := append([]string(nil), em.texts...)
		em.mu.Unlock()
		for _, tx := range texts {
			if strings.Contains(tx, "imshell-ok") {
				return
			}
		}
		sleepMs(20)
	}
	t.Fatal("shell result was not pushed back to the emitter")
}

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

func waitForShellResult(t *testing.T, em *captureEmitter, pred func(string) bool, what string) string {
	t.Helper()

	for i := 0; i < 400; i++ {
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

func TestHandleShellInbound_TruncationPreservesUTF8(t *testing.T) {
	em := &captureEmitter{}
	b := &DaemonBridge{emitTextOverride: em.EmitText}

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
				return
			}
		}
		sleepMs(20)
	}
	t.Fatal("stderr/exit-code output was not pushed back")
}
