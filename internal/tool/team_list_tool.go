package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/swarm"
)

// TeamListTool lists every team the current session can see: live teams
// (restored or created in this process) with their members and status, plus
// persisted-but-not-restored teams with their manifest roster. It closes the
// discovery gap between "the prompt snapshot said a team exists" and
// team_restore: the leader can see what exists NOW and who is in it.
type TeamListTool struct {
	Manager *swarm.Manager
}

func (t TeamListTool) Name() string { return "team_list" }

func (t TeamListTool) Description() string {
	return "List all teams visible to this session with their members: live teams (name, team_id, leader, each teammate's status/current task) " +
		"and persisted-but-not-restored teams (members from the manifest, last active). Use it to discover teams before team_restore / teammate_spawn / " +
		"swarm_task_create, or to find a teammate's id for send_message / team_review_decide."
}

func (t TeamListTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
			}
		},
		"required": ["description"]
	}`)
}

func (t TeamListTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "team_list: swarm manager not available"}, nil
	}
	var args struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	var b strings.Builder
	live := t.Manager.ListTeams()
	sort.Slice(live, func(i, j int) bool { return live[i].Name < live[j].Name })

	fmt.Fprintf(&b, "Teams visible to this session: %d live, ", len(live))
	liveIDs := make(map[string]bool, len(live))
	for _, team := range live {
		liveIDs[team.ID] = true
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "─── %s (%s) [live] ───\n", team.Name, team.ID)
		fmt.Fprintf(&b, "leader: %s • board tasks: %d\n", orDash(team.LeaderID), team.TaskCount)
		for _, tm := range team.Teammates {
			line := fmt.Sprintf("  - %s (%s) %s", tm.Name, tm.ID, tm.Status)
			if tm.CurrentTask != "" {
				line += " • " + tm.CurrentTask
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		if len(team.Teammates) == 0 {
			b.WriteString("  (no teammates)\n")
		}
	}

	persisted := swarm.DescribeVisibleTeams(t.Manager.SessionNamespace())
	restorable := 0
	for _, p := range persisted {
		if liveIDs[p.TeamID] {
			continue // already live - listed above
		}
		if restorable == 0 {
			b.WriteString("\n\nPersisted (not restored) teams:\n")
		}
		restorable++
		fmt.Fprintf(&b, "─── %s (%s) [persisted] ───\n", p.Name, p.TeamID)
		fmt.Fprintf(&b, "leader: %s • %d member(s) • last active %s\n", orDash(p.LeaderID), len(p.Members), p.LastActive.Format(time.RFC3339))
		for _, m := range p.Members {
			line := fmt.Sprintf("  - %s (%s)", m.Name, m.ID)
			if m.Role != "" {
				line += " • " + m.Role
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("  restore with: team_restore{name: \"" + p.Name + "\"}\n")
	}
	fmt.Fprintf(&b, "\n%d persisted-not-restored.\n", restorable)

	return Result{Content: b.String()}, nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
