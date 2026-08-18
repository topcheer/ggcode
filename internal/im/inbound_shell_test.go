package im

import (
	"strings"
	"sync"
	"testing"
	"time"
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
