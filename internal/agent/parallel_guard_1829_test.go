package agent

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func Test1829SpawnFamilySkipsPreExec(t *testing.T) {
	a := &Agent{
		tools:      tool.NewRegistry(),
		speculator: newSpeculator(),
	}
	for _, name := range []string{
		"spawn_agent", "teammate_spawn", "send_message", "swarm_task_create",
		"a2a_send_task", "a2a_remote", "warp",
	} {
		calls := []provider.ToolCallDelta{
			{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/test"}`)},
			{ID: "2", Name: name, Arguments: []byte(`{}`)},
		}
		if got := a.preExecuteReadOnlyTools(context.Background(), calls); got != nil {
			t.Errorf("%s in a mixed batch must skip pre-execution, got %d results", name, len(got))
		}
	}

	pure := []provider.ToolCallDelta{
		{ID: "1", Name: "read_file", Arguments: []byte(`{"path":"/tmp/a"}`)},
		{ID: "2", Name: "grep", Arguments: []byte(`{"pattern":"x"}`)},
	}

	_ = a.preExecuteReadOnlyTools(context.Background(), pure)
}
