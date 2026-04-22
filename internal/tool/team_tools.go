package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/swarm"
)

// ————————————————————————————————————————
// TeamCreate
// ————————————————————————————————————————

type TeamCreateTool struct {
	Manager *swarm.Manager
}

func (t TeamCreateTool) Name() string { return "team_create" }
func (t TeamCreateTool) Description() string {
	return "Create a collaboration team. Teammates can be spawned to work on tasks in parallel. " +
		"Returns team info including ID."
}
func (t TeamCreateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string", "description": "Team name (e.g., 'research-team', 'build-squad')"},
			"leader_id": {"type": "string", "description": "ID of the leader agent (defaults to current agent)"}
		},
		"required": ["name"]
	}`)
}
func (t TeamCreateTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "team_create: swarm manager not available"}, nil
	}
	var args struct {
		Name     string `json:"name"`
		LeaderID string `json:"leader_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}

	snap := t.Manager.CreateTeam(args.Name, args.LeaderID)
	out, _ := json.Marshal(snap)
	return Result{Content: string(out) + "\n"}, nil
}

// ————————————————————————————————————————
// TeamDelete
// ————————————————————————————————————————

type TeamDeleteTool struct {
	Manager *swarm.Manager
}

func (t TeamDeleteTool) Name() string { return "team_delete" }
func (t TeamDeleteTool) Description() string {
	return "Delete a team and shutdown all its teammates. Use this when the team's work is done."
}
func (t TeamDeleteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID to delete"}
		},
		"required": ["team_id"]
	}`)
}
func (t TeamDeleteTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "team_delete: swarm manager not available"}, nil
	}
	var args struct {
		TeamID string `json:"team_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if err := t.Manager.DeleteTeam(args.TeamID); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Team %s deleted.\n", args.TeamID)}, nil
}

// ————————————————————————————————————————
// TeammateSpawn
// ————————————————————————————————————————

type TeammateSpawnTool struct {
	Manager *swarm.Manager
}

func (t TeammateSpawnTool) Name() string { return "teammate_spawn" }
func (t TeammateSpawnTool) Description() string {
	return "Spawn a teammate (worker agent) in a team. The teammate enters an idle loop, waiting for tasks via messages. " +
		"Use send_message to assign work. Returns teammate info."
}
func (t TeammateSpawnTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID to spawn the teammate in"},
			"name": {"type": "string", "description": "Teammate name (e.g., 'researcher', 'coder', 'tester')"},
			"color": {"type": "string", "description": "ANSI color code for TUI display (e.g., '32' for green)"},
			"tools": {"type": "array", "items": {"type": "string"}, "description": "Allowed tool names (defaults to all non-swarm tools)"}
		},
		"required": ["team_id", "name"]
	}`)
}
func (t TeammateSpawnTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "teammate_spawn: swarm manager not available"}, nil
	}
	var args struct {
		TeamID string   `json:"team_id"`
		Name   string   `json:"name"`
		Color  string   `json:"color"`
		Tools  []string `json:"tools"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.TeamID) == "" {
		return Result{IsError: true, Content: "team_id is required"}, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}

	snap, err := t.Manager.SpawnTeammate(args.TeamID, args.Name, args.Color, args.Tools)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	out, _ := json.Marshal(snap)
	return Result{Content: string(out) + "\n"}, nil
}

// ————————————————————————————————————————
// TeammateList
// ————————————————————————————————————————

type TeammateListTool struct {
	Manager *swarm.Manager
}

func (t TeammateListTool) Name() string { return "teammate_list" }
func (t TeammateListTool) Description() string {
	return "List all teammates in a team with their status (idle/working/shutting_down)."
}
func (t TeammateListTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID"}
		},
		"required": ["team_id"]
	}`)
}
func (t TeammateListTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "teammate_list: swarm manager not available"}, nil
	}
	var args struct {
		TeamID string `json:"team_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	snap, ok := t.Manager.GetTeam(args.TeamID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("team %q not found", args.TeamID)}, nil
	}
	if len(snap.Teammates) == 0 {
		return Result{Content: "No teammates.\n"}, nil
	}

	sort.Slice(snap.Teammates, func(i, j int) bool {
		return snap.Teammates[i].ID < snap.Teammates[j].ID
	})

	var sb strings.Builder
	for _, tm := range snap.Teammates {
		task := ""
		if tm.CurrentTask != "" {
			task = fmt.Sprintf(" — %s", tm.CurrentTask)
		}
		fmt.Fprintf(&sb, "- %s [%s] %s%s\n", tm.ID, tm.Status, tm.Name, task)
	}
	return Result{Content: sb.String()}, nil
}

// ————————————————————————————————————————
// TeammateShutdown
// ————————————————————————————————————————

type TeammateShutdownTool struct {
	Manager *swarm.Manager
}

func (t TeammateShutdownTool) Name() string { return "teammate_shutdown" }
func (t TeammateShutdownTool) Description() string {
	return "Shutdown a specific teammate. The teammate will stop processing tasks."
}
func (t TeammateShutdownTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"team_id": {"type": "string", "description": "Team ID"},
			"teammate_id": {"type": "string", "description": "Teammate ID to shutdown"}
		},
		"required": ["team_id", "teammate_id"]
	}`)
}
func (t TeammateShutdownTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "teammate_shutdown: swarm manager not available"}, nil
	}
	var args struct {
		TeamID     string `json:"team_id"`
		TeammateID string `json:"teammate_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if err := t.Manager.ShutdownTeammate(args.TeamID, args.TeammateID); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: fmt.Sprintf("Teammate %s in team %s shut down.\n", args.TeammateID, args.TeamID)}, nil
}
