package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// withEnv sets env[k]=v for the duration of fn, restoring the prior value
// (or absence) afterwards.
func withEnv(t *testing.T, k, v string, fn func()) {
	t.Helper()
	old, had := os.LookupEnv(k)
	if err := os.Setenv(k, v); err != nil {
		t.Skipf("cannot set %s: %v", k, err)
	}
	defer func() {
		if had {
			_ = os.Setenv(k, old)
		} else {
			_ = os.Unsetenv(k)
		}
	}()
	fn()
}

// TestIsDumbTerminal verifies the entry-level environment probe that routes
// --plain / TERM=dumb / blank TERM away from the Bubble Tea frontend (#4604).
func TestIsDumbTerminal(t *testing.T) {
	cases := []struct {
		term string
		want bool
	}{
		{"dumb", true},
		{"DUMB", true},   // case-insensitive
		{" dumb ", true}, // surrounding whitespace
		{"", true},       // blank TERM on restricted environments
		{"xterm-256color", false},
		{"screen", false}, // screen sessions keep the TUI path
	}
	old, had := os.LookupEnv("TERM")
	if !had {
		t.Skipf("TERM not set in test environment")
	}
	defer func() { _ = os.Setenv("TERM", old) }()
	for _, c := range cases {
		setTerm := c.term
		if err := os.Setenv("TERM", setTerm); err != nil {
			t.Skipf("cannot set TERM: %v", err)
		}
		if got := isDumbTerminal(); got != c.want {
			t.Errorf("isDumbTerminal(TERM=%q) = %v, want %v", c.term, got, c.want)
		}
	}
}

// TestPlainApprovalDefaultAllow verifies the #4604 design decision: the
// linear frontend approves tools by default (no dialog exists) and only
// GGCODE_PLAIN_APPROVE=deny switches to rejection.
func TestPlainApprovalDefaultAllow(t *testing.T) {
	old, had := os.LookupEnv("GGCODE_PLAIN_APPROVE")
	defer func() {
		if had {
			_ = os.Setenv("GGCODE_PLAIN_APPROVE", old)
		} else {
			_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")
		}
	}()
	_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")
	if d := plainApproval(context.Background(), "run_command", "{}"); d != permission.Allow {
		t.Errorf("default approval = %v, want Allow", d)
	}

	os.Setenv("GGCODE_PLAIN_APPROVE", "deny")
	if d := plainApproval(context.Background(), "run_command", "{}"); d != permission.Deny {
		t.Errorf("GGCODE_PLAIN_APPROVE=deny approval = %v, want Deny", d)
	}

	os.Setenv("GGCODE_PLAIN_APPROVE", "DENY")
	if d := plainApproval(context.Background(), "run_command", "{}"); d != permission.Deny {
		t.Errorf("GGCODE_PLAIN_APPROVE=DENY (case-insensitive) = %v, want Deny", d)
	}
}

// TestHandlePlainCommandLang verifies /lang switching semantics on the nil-cfg
// path (pure in-memory, no persistence): valid targets switch plainLang and
// keep the loop up; unsupported languages and missing args are refused
// without changing the active language.
func TestHandlePlainCommandLang(t *testing.T) {
	orig := plainLang
	defer func() { plainLang = orig }()

	plainLang = "en"
	if handlePlainCommand("/lang fr", nil, nil) {
		t.Fatal("/lang must not exit the loop")
	}
	if plainLang != "en" {
		t.Errorf("unsupported language must not change plainLang, got %q", plainLang)
	}

	if handlePlainCommand("/lang", nil, nil) {
		t.Fatal("missing arg must not exit the loop")
	}
	if plainLang != "en" {
		t.Errorf("missing arg must not change plainLang, got %q", plainLang)
	}

	if handlePlainCommand("/lang zh-CN", nil, nil) {
		t.Fatal("valid switch must not exit the loop")
	}
	if plainLang != "zh-CN" {
		t.Errorf("plainLang = %q, want zh-CN", plainLang)
	}

	if handlePlainCommand("/lang en", nil, nil) {
		t.Fatal("switch back must not exit the loop")
	}
	if plainLang != "en" {
		t.Errorf("plainLang = %q, want en", plainLang)
	}
}

// TestPlainStreamPrinterToolStatusPairing verifies the linear frontend's
// tool-call/result pairing contract: call lines carry no trailing newline,
// the matching ToolResult appends " (ok)"/" (error)", unmatched results are
// dropped, and Done closes at most one unterminated tail line.
func TestPlainStreamPrinterToolStatusPairing(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "a", Name: "glob"}},
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "a"}},
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "b", Name: "run_command"}},
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "b"}, IsError: true},
		// Result with no preceding call line: must be dropped entirely.
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "ghost"}},
	}
	pending := make(map[string]bool)
	printer, closeTurn := plainStreamPrinter(pending)
	var out strings.Builder
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	for _, e := range events {
		printer(e)
	}
	closeTurn()
	_ = w.Close()
	os.Stdout = orig
	_, _ = io.Copy(&out, r)

	got := out.String()
	for _, want := range []string{"\n[tool] glob (ok)\n", "\n[tool] run_command (error)\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("output must contain %q, got:\n%q", want, got)
		}
	}
	if strings.Count(got, "[tool]") != 2 {
		t.Errorf("ghost result must not print a line, got:\n%q", got)
	}
	if len(pending) != 0 {
		t.Errorf("pending map must be drained after close, got %v", pending)
	}
}

// TestPlainStreamPrinterDoneClosesUnterminatedTail verifies a call whose
// result never arrives gets exactly one closing newline (not one per call),
// so the next turn's output starts on a fresh line.
func TestPlainStreamPrinterDoneClosesUnterminatedTail(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "a", Name: "slow"}},
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "b", Name: "slower"}},
		// Per-iteration provider Dones (with or without Usage) must be inert:
		// tool results arrive between iterations and the turn only ends when
		// RunStream returns.
		{Type: provider.StreamEventDone},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{}},
	}
	pending := make(map[string]bool)
	printer, closeTurn := plainStreamPrinter(pending)
	var out strings.Builder
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	for _, e := range events {
		printer(e)
	}
	closeTurn()
	_ = w.Close()
	os.Stdout = orig
	_, _ = io.Copy(&out, r)

	// Two unterminated calls: 2 leading newlines (one per [tool] line) +
	// exactly one tail-closing newline + one separator newline = 4 total -
	// never one closing newline per call.
	got := out.String()
	if n := strings.Count(got, "\n"); n != 4 {
		t.Errorf("expected exactly 4 newlines (2 call-leads + 1 tail close + 1 separator), got %d:\n%q", n, got)
	}
	if len(pending) != 0 {
		t.Errorf("pending map must be drained, got %v", pending)
	}
}

// TestPlainStreamPrinterIntermediateDoneKeepsPairing reproduces the real
// multi-iteration event order: ToolCallDone -> provider-stream Done ->
// ToolResult. StreamEventDone is inert by design; pairing survives and the
// turn only closes when plainCloseTurn runs after RunStream returns.
func TestPlainStreamPrinterIntermediateDoneKeepsPairing(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "a", Name: "glob"}},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{}}, // intermediate, still inert
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "a"}},
	}
	pending := make(map[string]bool)
	printer, closeTurn := plainStreamPrinter(pending)
	var out strings.Builder
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	for _, e := range events {
		printer(e)
	}
	closeTurn()
	_ = w.Close()
	os.Stdout = orig
	_, _ = io.Copy(&out, r)

	got := out.String()
	if !strings.Contains(got, "[tool] glob (ok)\n") {
		t.Errorf("intermediate Done must not break pairing, got:\n%q", got)
	}
	if n := strings.Count(got, "\n"); n != 3 { // 1 leading + 1 after (ok) + 1 separator
		t.Errorf("expected 3 newlines, got %d:\n%q", n, got)
	}
}

// TestPlainStreamPrinterReasoningStreams verifies reasoning (thinking) deltas
// stream as a prefixed "[think] " line, and the line closes when text, a tool
// call, or the turn boundary follows. glm/DeepSeek put the tool-turn narration
// in the reasoning stream - without this branch plain users see silent tool
// calls with no explanation.
func TestPlainStreamPrinterReasoningStreams(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventReasoning, Text: "let me check "},
		{Type: provider.StreamEventReasoning, Text: "the directory"},
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "a", Name: "glob"}},
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "a"}},
		{Type: provider.StreamEventText, Text: "5 files."},
	}
	pending := make(map[string]bool)
	printer, closeTurn := plainStreamPrinter(pending)
	var out strings.Builder
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	for _, e := range events {
		printer(e)
	}
	closeTurn()
	_ = w.Close()
	os.Stdout = orig
	_, _ = io.Copy(&out, r)

	got := out.String()
	// think line opened with prefix, closed by the tool boundary
	if !strings.Contains(got, "\n[think] let me check the directory\n") {
		t.Errorf("reasoning must stream as one [think] line closed by the tool call, got:\n%q", got)
	}
	if !strings.Contains(got, "\n[tool] glob (ok)\n") {
		t.Errorf("tool pairing must still work after reasoning, got:\n%q", got)
	}
	if !strings.HasSuffix(got, "5 files.\n") {
		t.Errorf("final text must close with the turn separator newline, got:\n%q", got)
	}
}

// TestPlainStreamPrinterThinkOnlyTurnCloses verifies a turn ending on an open
// think line (thinking-only round) gets closed by the turn boundary so the
// next prompt starts clean.
func TestPlainStreamPrinterThinkOnlyTurnCloses(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventReasoning, Text: "pondering..."},
	}
	pending := make(map[string]bool)
	printer, closeTurn := plainStreamPrinter(pending)
	var out strings.Builder
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	for _, e := range events {
		printer(e)
	}
	closeTurn()
	_ = w.Close()
	os.Stdout = orig
	_, _ = io.Copy(&out, r)

	got := out.String()
	if !strings.Contains(got, "\n[think] pondering...\n") {
		t.Errorf("think-only turn must close its line, got:\n%q", got)
	}
}

// TestWirePlainPersistNilAgentGuard verifies the best-effort contract: a nil
// agent (config unavailable path) returns nils instead of panicking, so
// --persist-session never blocks REPL startup.
func TestWirePlainPersistNilAgentGuard(t *testing.T) {
	store, ses := wirePlainPersist(nil, nil)
	if store != nil || ses != nil {
		t.Fatalf("nil agent must yield nil store/session, got %v/%v", store, ses)
	}
}

// TestHandlePlainCommandExit verifies only the exit-family commands terminate
// the linear loop; every other command (including unknown ones) keeps it up.
func TestHandlePlainCommandExit(t *testing.T) {
	old, had := os.LookupEnv("GGCODE_PLAIN_APPROVE")
	defer func() {
		if had {
			_ = os.Setenv("GGCODE_PLAIN_APPROVE", old)
		} else {
			_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")
		}
	}()
	_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")

	for _, cmd := range []string{"/exit", "/quit", "/q"} {
		if !handlePlainCommand(cmd, nil, nil) {
			t.Errorf("%s should exit the loop", cmd)
		}
	}
	for _, cmd := range []string{
		"/help", "/?", "/status", // nil cfg must not panic
		"/nosuchcommand",
		"/model",           // missing arg: usage, no exit
		"/provider",        // missing arg: usage, no exit
		"/endpoint",        // missing arg: usage, no exit
		"/model glm-5.3",   // nil agent/cfg: graceful message, no exit
		"/provider z ai-1", // nil agent/cfg: graceful message, no exit
	} {
		if handlePlainCommand(cmd, nil, nil) {
			t.Errorf("%s must NOT exit the loop", cmd)
		}
	}
}
