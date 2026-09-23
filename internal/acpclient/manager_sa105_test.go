package acpclient

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	acpgo "github.com/topcheer/ggcode-acp-go"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

// fakePolicy is a deterministic PermissionPolicy for adapter tests.
type fakePolicy struct {
	decision    permission.Decision
	checkErr    error
	allowedPath bool
}

var _ permission.PermissionPolicy = (*fakePolicy)(nil)

func (f *fakePolicy) Check(toolName string, input json.RawMessage) (permission.Decision, error) {
	return f.decision, f.checkErr
}

func (f *fakePolicy) Mode() permission.PermissionMode {
	var zero permission.PermissionMode
	return zero
}

func (f *fakePolicy) IsDangerous(command string) bool { return false }

func (f *fakePolicy) BlocksAutoApprove(toolName string, input json.RawMessage) bool { return false }

func (f *fakePolicy) AllowedPath(path string) bool { return f.allowedPath }

func (f *fakePolicy) AllowedPathForTool(toolName, path string) bool { return f.allowedPath }

func (f *fakePolicy) SetOverride(toolName string, decision permission.Decision) {}

func (f *fakePolicy) AllowCommandPattern(pattern string) {}

func TestToExternalDecision(t *testing.T) {
	tests := []struct {
		name string
		in   permission.Decision
		want acpgo.Decision
	}{
		{name: "allow", in: permission.Allow, want: acpgo.Allow},
		{name: "deny", in: permission.Deny, want: acpgo.Deny},
		{name: "ask", in: permission.Ask, want: acpgo.Ask},
		{name: "unknown falls back to ask", in: permission.Decision(42), want: acpgo.Ask},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toExternalDecision(tt.in); got != tt.want {
				t.Errorf("toExternalDecision(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestToToolPromptResult(t *testing.T) {
	t.Run("nil result maps to nil", func(t *testing.T) {
		if got := toToolPromptResult(nil); got != nil {
			t.Errorf("toToolPromptResult(nil) = %v, want nil", got)
		}
	})

	t.Run("full mapping", func(t *testing.T) {
		in := &acpgo.PromptResult{
			Text:       "done",
			StopReason: acpgo.StopReason("end_turn"),
			ToolCalls: []acpgo.ToolCallSummary{
				{Name: "read_file", Title: "Read", Status: "completed"},
				{Name: "run_command", Title: "Run", Status: "failed"},
			},
		}
		got := toToolPromptResult(in)
		if got == nil {
			t.Fatal("toToolPromptResult() = nil, want result")
		}
		if got.Text != "done" {
			t.Errorf("Text = %q, want %q", got.Text, "done")
		}
		if got.StopReason != "end_turn" {
			t.Errorf("StopReason = %q, want %q", got.StopReason, "end_turn")
		}
		if len(got.ToolCalls) != 2 {
			t.Fatalf("len(ToolCalls) = %d, want 2", len(got.ToolCalls))
		}
		wantFirst := tool.ACPToolCallSummary{Name: "read_file", Title: "Read", Status: "completed"}
		if got.ToolCalls[0] != wantFirst {
			t.Errorf("ToolCalls[0] = %+v, want %+v", got.ToolCalls[0], wantFirst)
		}
		wantSecond := tool.ACPToolCallSummary{Name: "run_command", Title: "Run", Status: "failed"}
		if got.ToolCalls[1] != wantSecond {
			t.Errorf("ToolCalls[1] = %+v, want %+v", got.ToolCalls[1], wantSecond)
		}
	})

	t.Run("empty tool calls", func(t *testing.T) {
		got := toToolPromptResult(&acpgo.PromptResult{Text: "hi"})
		if got == nil || len(got.ToolCalls) != 0 {
			t.Errorf("expected empty ToolCalls, got %+v", got)
		}
	})
}

func TestPermissionPolicyAdapterCheck(t *testing.T) {
	t.Run("nil inner asks", func(t *testing.T) {
		a := permissionPolicyAdapter{inner: nil}
		decision, err := a.Check("read_file", nil)
		if err != nil || decision != acpgo.Ask {
			t.Errorf("nil inner Check = (%v, %v), want (Ask, nil)", decision, err)
		}
	})

	t.Run("decisions map through", func(t *testing.T) {
		tests := []struct {
			name string
			in   permission.Decision
			want acpgo.Decision
		}{
			{"allow", permission.Allow, acpgo.Allow},
			{"deny", permission.Deny, acpgo.Deny},
			{"ask", permission.Ask, acpgo.Ask},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				a := permissionPolicyAdapter{inner: &fakePolicy{decision: tt.in}}
				decision, err := a.Check("read_file", json.RawMessage(`{}`))
				if err != nil || decision != tt.want {
					t.Errorf("Check = (%v, %v), want (%v, nil)", decision, err, tt.want)
				}
			})
		}
	})

	t.Run("policy error surfaces as ask", func(t *testing.T) {
		sentinel := errors.New("policy boom")
		a := permissionPolicyAdapter{inner: &fakePolicy{checkErr: sentinel}}
		decision, err := a.Check("read_file", nil)
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want %v", err, sentinel)
		}
		if decision != acpgo.Ask {
			t.Errorf("decision = %v, want Ask on error", decision)
		}
	})
}

func TestPermissionPolicyAdapterAllowedPathForTool(t *testing.T) {
	t.Run("nil inner allows", func(t *testing.T) {
		a := permissionPolicyAdapter{inner: nil}
		if !a.AllowedPathForTool("edit_file", "/tmp/x") {
			t.Error("nil inner AllowedPathForTool = false, want true")
		}
	})
	t.Run("delegates to policy", func(t *testing.T) {
		a := permissionPolicyAdapter{inner: &fakePolicy{allowedPath: false}}
		if a.AllowedPathForTool("edit_file", "/etc/passwd") {
			t.Error("AllowedPathForTool = true, want false")
		}
		a2 := permissionPolicyAdapter{inner: &fakePolicy{allowedPath: true}}
		if !a2.AllowedPathForTool("edit_file", "/tmp/x") {
			t.Error("AllowedPathForTool = false, want true")
		}
	})
}

func TestDebugLoggerDebugf(t *testing.T) {
	// Must not panic and must route into the debug ring buffer.
	debugLogger{}.Debugf("probe %s %d", "value", 7)
}

// TestClientManagerNilGuards verifies the defensive nil-receiver and
// nil-inner branches on every ClientManager wrapper method.
func TestClientManagerNilGuards(t *testing.T) {
	managers := map[string]*ClientManager{
		"nil receiver": nil,
		"nil inner":    {},
	}
	for label, m := range managers {
		t.Run(label, func(t *testing.T) {
			if got := m.Available(); got != nil {
				t.Errorf("Available() = %v, want nil", got)
			}
			if title, desc, ok := m.AgentInfo("claude"); title != "" || desc != "" || ok {
				t.Errorf("AgentInfo() = (%q, %q, %v), want empty false", title, desc, ok)
			}
			m.SetWorkingDir(t.TempDir()) // must not panic
			m.SetApprovalHandler(nil)    // must not panic
			m.SetApprovalHandler(func(context.Context, string, string) permission.Decision {
				return permission.Ask
			}) // must not panic
			m.CloseAll() // must not panic
			client, err := m.Get(context.Background(), "claude")
			if client != nil || err != nil {
				t.Errorf("Get() = (%v, %v), want (nil, nil)", client, err)
			}
		})
	}
}

// TestClientNilGuards covers the nil-receiver/nil-inner branches of the
// guarded Client methods. Prompt and PromptStream have no nil guard and
// require a live agent process, so they are intentionally not exercised here.
func TestClientNilGuards(t *testing.T) {
	clients := map[string]*Client{
		"nil receiver": nil,
		"nil inner":    {},
	}
	for label, c := range clients {
		t.Run(label, func(t *testing.T) {
			if err := c.Close(); err != nil {
				t.Errorf("Close() = %v, want nil", err)
			}
		})
	}
}

// TestClientManagerAgainstRealManager exercises the wrapper against a real
// acpgo manager. Construction only performs agent discovery (no process
// spawn); unknown agent names fail fast without launching anything.
func TestClientManagerAgainstRealManager(t *testing.T) {
	tmp := t.TempDir()
	m := NewClientManager(tmp, &fakePolicy{decision: permission.Ask})
	if m == nil || m.inner == nil {
		t.Fatal("NewClientManager returned nil manager")
	}

	if got := m.Available(); got == nil {
		t.Error("Available() = nil, want non-nil slice")
	}

	// Environment-independent: this name is never a discovered agent.
	const unknown = "definitely-not-a-real-acp-agent"
	if title, desc, ok := m.AgentInfo(unknown); ok || title != "" || desc != "" {
		t.Errorf("AgentInfo(unknown) = (%q, %q, %v), want empty false", title, desc, ok)
	}

	m.SetWorkingDir(tmp) // must not panic

	handlerSet := false
	m.SetApprovalHandler(func(context.Context, string, string) permission.Decision {
		handlerSet = true
		return permission.Deny
	})
	if handlerSet {
		t.Error("approval handler invoked during registration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := m.Get(ctx, unknown)
	if err == nil {
		t.Error("Get(unknown) = nil error, want lookup failure")
	}
	if client != nil {
		t.Error("Get(unknown) returned a client, want nil")
	}

	m.CloseAll() // must not panic with zero live clients
}
