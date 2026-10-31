package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// #4436 Bug3: a panic inside the delegated PromptStream goroutine must
// complete the sub-agent with an error instead of leaving it "running"
// forever (goroutine died, no Complete call).

type panickingAgentRegistry struct{ inner *mockACPAgentRegistry }

func (p *panickingAgentRegistry) Available() []string { return p.inner.Available() }
func (p *panickingAgentRegistry) AgentInfo(name string) (string, string, bool) {
	return p.inner.AgentInfo(name)
}
func (p *panickingAgentRegistry) SetWorkingDir(dir string) { p.inner.SetWorkingDir(dir) }
func (p *panickingAgentRegistry) Get(ctx context.Context, name string) (ACPAgentClient, error) {
	return &panickingACPAgentClient{}, nil
}

type panickingACPAgentClient struct{ mockACPAgentClient }

func (c *panickingACPAgentClient) PromptStream(
	ctx context.Context,
	prompt string,
	onEvent func(ACPPromptEvent),
) (*ACPPromptResult, error) {
	panic("boom from stream")
}

func TestIssue4436DelegatePanicCompletesSubAgent(t *testing.T) {
	reg := &mockACPAgentRegistry{
		agents: map[string]agentEntry{
			"boom": {"Boom Agent", "panics on stream"},
		},
	}
	// Wrap so Get returns the panicking client.
	reg2 := &panickingAgentRegistry{inner: reg}

	mgr := subagent.NewManager(config.SubAgentConfig{})
	tool := DelegateTool{
		Manager:         reg2,
		SubAgentManager: mgr,
		WorkingDir:      "",
	}

	res, err := tool.Execute(context.Background(), []byte(`{"agent":"boom","prompt":"go"}`))
	if err != nil {
		t.Fatalf("execute returned err: %v", err)
	}
	if res.IsError {
		t.Fatalf("execute reported error: %s", res.Content)
	}
	// Extract agent id from the startup message.
	fields := strings.Fields(res.Content)
	id := ""
	for i, f := range fields {
		if f == "agent_id" && i+1 < len(fields) {
			id = strings.Trim(fields[i+1], `"`)
			break
		}
	}
	if id == "" {
		t.Fatalf("no agent_id in output: %s", res.Content)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sa, ok := mgr.Get(id); ok {
			if sa.Status != subagent.StatusRunning && sa.Status != subagent.StatusPending {
				// The fix guarantees Complete() runs on panic: the sub-agent
				// reaches a terminal state (failed with an error recorded,
				// never stuck running).
				if sa.Status == subagent.StatusFailed || sa.Status == subagent.StatusCompleted || sa.Status == subagent.StatusCancelled {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sub-agent %q still running after panic (not completed)", id)
}
