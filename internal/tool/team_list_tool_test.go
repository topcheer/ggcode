package tool

import (
	"context"
	"strings"
	"testing"
)

func TestTeamListTool_LiveTeamWithMembers(t *testing.T) {
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("dev-squad", "leader")
	if _, err := mgr.SpawnTeammate(team.ID, "coder", "32", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	res, err := TeamListTool{Manager: mgr}.Execute(context.Background(), mustJSON(t, map[string]string{"description": "list teams"}))
	if err != nil || res.IsError {
		t.Fatalf("team_list: %v %s", err, res.Content)
	}
	for _, want := range []string{"1 live", "dev-squad", "[live]", "coder", "leader: leader"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("output must contain %q, got:\n%s", want, res.Content)
		}
	}
	if !strings.Contains(res.Content, "0 persisted-not-restored") {
		t.Fatalf("no persisted teams expected, got:\n%s", res.Content)
	}
}

func TestTeamListTool_NoManager(t *testing.T) {
	res, _ := TeamListTool{}.Execute(context.Background(), mustJSON(t, map[string]string{"description": "x"}))
	if !res.IsError {
		t.Fatalf("nil manager must be an error result, got %q", res.Content)
	}
}
