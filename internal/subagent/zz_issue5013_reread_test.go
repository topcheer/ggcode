package subagent

// #5013 Ask-2: same-file reread circuit breaker tests. The breaker must trip
// after >3 identical-path reads with no intervening edit, reset on edit, and
// surface a descriptive termination error (not a bare "cancelled").

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func readArgs(t *testing.T, path string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func multiReadArgs(t *testing.T, paths ...string) string {
	t.Helper()
	files := make([]map[string]any, len(paths))
	for i, p := range paths {
		files[i] = map[string]any{"path": p}
	}
	b, err := json.Marshal(map[string]any{"files": files})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestIssue5013RereadBreaker_TripsAfterLimitWithoutEdit(t *testing.T) {
	b := newRereadBreaker()
	args := readArgs(t, "internal/im/emitter.go")

	for i := 1; i <= 3; i++ {
		if _, _, tripped := b.record("read_file", args); tripped {
			t.Fatalf("tripped at read %d, want only after %d", i, rereadBreakerLimit+1)
		}
	}
	path, n, tripped := b.record("read_file", args)
	if !tripped {
		t.Fatal("breaker must trip on the 4th identical read")
	}
	if path != "internal/im/emitter.go" || n != 4 {
		t.Fatalf("tripped path=%q count=%d, want emitter.go/4", path, n)
	}
	if _, _, again := b.record("read_file", args); again {
		t.Fatal("breaker must latch: no second trip report")
	}
	if !strings.Contains(b.reason(), `"internal/im/emitter.go" read 4 times`) {
		t.Fatalf("reason = %q, want path+count", b.reason())
	}
}

func TestIssue5013RereadBreaker_EditResetsWindow(t *testing.T) {
	b := newRereadBreaker()
	args := readArgs(t, "main.go")

	for i := 0; i < 3; i++ {
		b.record("read_file", args)
	}
	if _, _, tripped := b.record("edit_file", `{"path":"main.go","old_text":"a","new_text":"b"}`); tripped {
		t.Fatal("edit must not trip the breaker")
	}
	if _, _, tripped := b.record("read_file", args); tripped {
		t.Fatal("post-edit reread is verification, must not trip")
	}
	if _, _, tripped := b.record("read_file", args); tripped {
		t.Fatal("second post-edit read still under fresh window")
	}
}

func TestIssue5013RereadBreaker_IgnoresNonReadToolsAndBadJSON(t *testing.T) {
	b := newRereadBreaker()
	for i := 0; i < 10; i++ {
		if _, _, tripped := b.record("grep", `{"pattern":"x","path":"a.go"}`); tripped {
			t.Fatal("grep is not a read-class tool for this breaker")
		}
		if _, _, tripped := b.record("read_file", "not-json"); tripped {
			t.Fatal("unparseable args must never trip")
		}
	}
}

func TestIssue5013RereadBreaker_MultiFileReadCountsPerPath(t *testing.T) {
	b := newRereadBreaker()
	dual := multiReadArgs(t, "a.go", "b.go")
	for i := 0; i < 3; i++ {
		b.record("multi_file_read", dual)
	}
	path, _, tripped := b.record("multi_file_read", dual)
	if !tripped {
		t.Fatal("per-path counting must trip on the 4th multi-read of the same files")
	}
	if path != "a.go" {
		t.Fatalf("first tripped path = %q, want a.go", path)
	}
}

// issue5013LoopRunner re-reads the same file forever until the context dies;
// it models the sa-2/sa-3 runaway shape.
type issue5013LoopRunner struct{}

func (r *issue5013LoopRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent), origin ...string) error {
	args := `{"path":"big.go"}`
	for i := 0; i < 50; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		onEvent(provider.StreamEvent{Type: provider.StreamEventToolCallDone,
			Tool: provider.ToolCallDelta{ID: "t", Name: "read_file", Arguments: json.RawMessage(args)}})
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

func TestIssue5013RereadBreaker_TerminatesLoopingRunner(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{MaxConcurrent: 1, Timeout: 10 * time.Second})
	id := mgr.Spawn("looper", "task", "task", nil, context.Background())

	Run(context.Background(), RunnerConfig{
		Task:       "task",
		Manager:    mgr,
		SubAgentID: id,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) AgentRunner {
			return &issue5013LoopRunner{}
		},
	})

	sa, ok := mgr.Get(id)
	if !ok {
		t.Fatal("sub-agent record missing after Run")
	}
	if sa.Error == nil || !strings.Contains(sa.Error.Error(), "reread breaker") {
		t.Fatalf("run error = %v, want reread-breaker termination", sa.Error)
	}
	if !strings.Contains(sa.Error.Error(), "big.go") {
		t.Fatalf("termination error must name the looped path: %v", sa.Error)
	}
}
