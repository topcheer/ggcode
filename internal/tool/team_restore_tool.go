package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/swarm"
)

// TeamRestoreTool restores a persisted team after a restart (persistent
// teams #5829): loads the manifest, replays each member's session journal,
// and restarts the idle loops with memory intact. Resolves by team name or
// ID. Refuses while another live instance owns the team's board.
type TeamRestoreTool struct {
	Manager *swarm.Manager
}

func (t TeamRestoreTool) Name() string { return "team_restore" }

func (t TeamRestoreTool) Description() string {
	return "Restore a persisted team after a restart: members come back with their memory (session journals replayed) " +
		"and the task board is reloaded. Resolves by team name or team ID. Use after a host/process restart to resume " +
		"an existing team instead of recreating it; refuses if another live instance owns the board."
}

func (t TeamRestoreTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {
			"type": "string",
			"description": "Team name or team ID to restore"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"name",
		"description"
	]
}`)
}

func (t TeamRestoreTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "team_restore: swarm manager not available"}, nil
	}
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}
	snap, err := t.Manager.RestoreTeamByName(args.Name)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	out, _ := json.Marshal(snap)
	return Result{Content: fmt.Sprintf("Team %s (%s) restored with %d teammate(s); board reloaded.\n%s\n",
		snap.ID, snap.Name, len(snap.Teammates), string(out))}, nil
}
