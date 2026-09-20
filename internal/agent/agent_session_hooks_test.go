package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// Session lifecycle hook wiring tests (on_session_start / on_session_end).
// Frontier parity: Claude Code SessionStart fires once per session with a
// source (startup/resume) and its stdout is added to context; SessionEnd
// flushes state on teardown (https://code.claude.com/docs/en/hooks).

func newSessionHookAgent(t *testing.T) *Agent {
	t.Helper()
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
			Usage: provider.TokenUsage{InputTokens: 1, OutputTokens: 1},
		},
	}
	return NewAgent(mp, tool.NewRegistry(), "", 5)
}

func runSessionHookTurn(t *testing.T, a *Agent) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30e9)
	defer cancel()
	return a.RunStream(ctx, "hi", func(provider.StreamEvent) {})
}

func TestSessionStartHookFiresOncePerAgent(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "starts")
	a := newSessionHookAgent(t)
	a.SetHookConfig(hooks.HookConfig{
		OnSessionStart: []hooks.Hook{{Match: "*", Command: "echo started >> " + counter}},
	})
	if err := runSessionHookTurn(t, a); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if err := runSessionHookTurn(t, a); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("on_session_start hook never ran: %v", err)
	}
	if n := strings.Count(string(data), "started"); n != 1 {
		t.Fatalf("on_session_start fired %d times, want 1", n)
	}
}

func TestSessionStartSourceStartupVsResume(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "src")
	hookCfg := hooks.HookConfig{
		OnSessionStart: []hooks.Hook{{Match: "*", Command: "echo $GGCODE_SESSION_SOURCE >> " + srcFile}},
	}

	fresh := newSessionHookAgent(t)
	fresh.SetHookConfig(hookCfg)
	if err := runSessionHookTurn(t, fresh); err != nil {
		t.Fatalf("fresh run: %v", err)
	}

	resumed := newSessionHookAgent(t)
	resumed.AddMessage(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "earlier turn"}}})
	resumed.SetHookConfig(hookCfg)
	if err := runSessionHookTurn(t, resumed); err != nil {
		t.Fatalf("resumed run: %v", err)
	}

	data, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("hooks never ran: %v", err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 2 || lines[0] != "startup" || lines[1] != "resume" {
		t.Fatalf("sources = %v, want [startup resume]", lines)
	}
}

func TestSessionStartHookCanBlock(t *testing.T) {
	a := newSessionHookAgent(t)
	a.SetHookConfig(hooks.HookConfig{
		OnSessionStart: []hooks.Hook{{Match: "*", Command: "echo locked >&2; exit 2"}},
	})
	err := runSessionHookTurn(t, a)
	if err == nil || !strings.Contains(err.Error(), "session start blocked") {
		t.Fatalf("want session-start block error, got %v", err)
	}
}

func TestSessionStartHookOutputInjectedAsContext(t *testing.T) {
	a := newSessionHookAgent(t)
	a.SetHookConfig(hooks.HookConfig{
		OnSessionStart: []hooks.Hook{{Match: "*", Command: "echo branch: main"}},
	})
	if err := runSessionHookTurn(t, a); err != nil {
		t.Fatalf("run: %v", err)
	}
	cm, ok := a.contextManager.(*ctxpkg.Manager)
	if !ok {
		t.Fatal("context manager is not the concrete Manager")
	}
	found := false
	for _, m := range cm.Messages() {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "[Session start hooks]") && strings.Contains(b.Text, "branch: main") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("session-start hook stdout was not injected as context")
	}
}

func TestSessionEndHookFiresOnClose(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ended")
	a := newSessionHookAgent(t)
	a.SetHookConfig(hooks.HookConfig{
		OnSessionEnd: []hooks.Hook{{Match: "*", Command: "touch " + marker}},
	})
	a.Close()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("on_session_end hook did not run on Close: %v", err)
	}
	a.Close() // idempotent; must not hang or double-fire
}
