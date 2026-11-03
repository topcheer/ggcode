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

func TestIsDumbTerminal(t *testing.T) {
	cases := []struct {
		term string
		want bool
	}{
		{"dumb", true},
		{"DUMB", true},
		{" dumb ", true},
		{"", true},
		{"xterm-256color", false},
		{"screen", false},
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

func TestPlainStreamPrinterToolStatusPairing(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "a", Name: "glob"}},
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "a"}},
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "b", Name: "run_command"}},
		{Type: provider.StreamEventToolResult, Tool: provider.ToolCallDelta{ID: "b"}, IsError: true},

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

func TestPlainStreamPrinterDoneClosesUnterminatedTail(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "a", Name: "slow"}},
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "b", Name: "slower"}},

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

	got := out.String()
	if n := strings.Count(got, "\n"); n != 4 {
		t.Errorf("expected exactly 4 newlines (2 call-leads + 1 tail close + 1 separator), got %d:\n%q", n, got)
	}
	if len(pending) != 0 {
		t.Errorf("pending map must be drained, got %v", pending)
	}
}

func TestPlainStreamPrinterIntermediateDoneKeepsPairing(t *testing.T) {
	events := []provider.StreamEvent{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{ID: "a", Name: "glob"}},
		{Type: provider.StreamEventDone, Usage: &provider.TokenUsage{}},
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
	if n := strings.Count(got, "\n"); n != 3 {
		t.Errorf("expected 3 newlines, got %d:\n%q", n, got)
	}
}

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

func TestWirePlainPersistNilAgentGuard(t *testing.T) {
	store, ses := wirePlainPersist(nil, nil)
	if store != nil || ses != nil {
		t.Fatalf("nil agent must yield nil store/session, got %v/%v", store, ses)
	}
}

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
		"/help", "/?", "/status",
		"/nosuchcommand",
		"/model",
		"/provider",
		"/endpoint",
		"/model glm-5.3",
		"/provider z ai-1",
	} {
		if handlePlainCommand(cmd, nil, nil) {
			t.Errorf("%s must NOT exit the loop", cmd)
		}
	}
}
