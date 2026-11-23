package tool

import (
	"sort"
	"testing"
)

func teammateTestRegistry(names ...string) *Registry {
	r := NewRegistry()
	for _, n := range names {
		_ = r.Register(&fakeMCPTool{name: n})
	}
	return r
}

func namesOf(r *Registry) []string {
	out := r.ToolNames()
	sort.Strings(out)
	return out
}

func TestTeammateRegistry_DeniesLeaderAndNestedAgentTools(t *testing.T) {
	var all []string
	all = append(all, TeammateDeniedTools...)
	all = append(all, "read_file", "edit_file", "run_command", "swarm_task_claim",
		"swarm_task_complete", "teammate_results", "teammate_list", "send_message",
		"report_to_leader")

	got := namesOf(TeammateRegistry(teammateTestRegistry(all...), nil))
	want := []string{"edit_file", "read_file", "report_to_leader", "run_command",
		"send_message", "swarm_task_claim", "swarm_task_complete", "teammate_list", "teammate_results"}
	if len(got) != len(want) {
		t.Fatalf("teammate tool set = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("teammate tool set[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// Regression: the teammate_spawn "tools" argument used to be discarded by
// the CLI builder, so an allowlist was never enforced.
func TestTeammateRegistry_AppliesAllowlist(t *testing.T) {
	src := teammateTestRegistry("read_file", "edit_file", "run_command", "team_review_decide")

	got := namesOf(TeammateRegistry(src, []string{"read_file", "run_command", "team_review_decide"}))
	if len(got) != 2 || got[0] != "read_file" || got[1] != "run_command" {
		t.Fatalf("allowlist must keep only allowed AND non-denied tools, got %v", got)
	}
}

func TestTeammateRegistry_DenyWinsOverAllowlist(t *testing.T) {
	src := teammateTestRegistry("a2a_remote", "spawn_agent", "read_file")
	got := namesOf(TeammateRegistry(src, []string{"a2a_remote", "spawn_agent", "read_file"}))
	if len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("denied tools must be dropped even when explicitly allowed, got %v", got)
	}
}

func TestTeammateRegistry_DoesNotMutateSource(t *testing.T) {
	src := teammateTestRegistry("team_review_decide", "read_file")
	_ = TeammateRegistry(src, []string{"read_file"})
	if _, ok := src.Get("team_review_decide"); !ok {
		t.Fatal("source registry must keep leader-only tools")
	}
	if TeammateRegistry(nil, nil) != nil {
		t.Fatal("nil source must return nil")
	}
}
