package tool

// #1705 case 2: swarm_task_create accepted any assignee string - a typo'd
// or name-form assignee stranded the task permanently (metadata carried
// it, the idle runner skipped it), and swarm_task_complete was
// double-fireable with no idempotence guard.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func new1705Env(t *testing.T) (*SwarmTaskCreateTool, *SwarmTaskCompleteTool, string, string) {
	t.Helper()
	mgr := swarmTestManager(t)
	team := mgr.CreateTeam("t1705", "leader")
	tmSnap, err := mgr.SpawnTeammate(team.ID, "worker1705", "32", nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	return &SwarmTaskCreateTool{Manager: mgr}, &SwarmTaskCompleteTool{Manager: mgr}, team.ID, tmSnap.ID
}

func TestIssue1705CreateRejectsUnknownAssignee(t *testing.T) {
	create, _, teamID, tmID := new1705Env(t)
	_ = tmID
	res, err := create.Execute(context.Background(), json.RawMessage(
		`{"team_id":"`+teamID+`","subject":"s","description":"d","assignee":"tm-999-typo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("unknown assignee must be rejected before the task lands on the board")
	}
}

func TestIssue1705CreateAcceptsRealTeammateID(t *testing.T) {
	create, _, teamID, tmID := new1705Env(t)
	res, err := create.Execute(context.Background(), json.RawMessage(
		`{"team_id":"`+teamID+`","subject":"s","description":"d","assignee":"`+tmID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("real teammate ID must pass the gate: %s", res.Content)
	}
}

func TestIssue1705CompleteIsIdempotentGuarded(t *testing.T) {
	create, complete, teamID, _ := new1705Env(t)
	created, err := create.Execute(context.Background(), json.RawMessage(
		`{"team_id":"`+teamID+`","subject":"s","description":"d"}`))
	if err != nil || created.IsError {
		t.Fatalf("create: %v %s", err, created.Content)
	}
	start := strings.Index(created.Content, "{")
	end := strings.LastIndex(created.Content, "}")
	if start < 0 || end < start {
		t.Fatalf("no JSON object in create output: %s", created.Content)
	}
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created.Content[start:end+1]), &task); err != nil {
		t.Fatalf("parse create output: %v", err)
	}
	first, err := complete.Execute(context.Background(), json.RawMessage(
		`{"team_id":"`+teamID+`","task_id":"`+task.ID+`"}`))
	if err != nil || first.IsError {
		t.Fatalf("first complete must succeed: %v %s", err, first.Content)
	}
	second, err := complete.Execute(context.Background(), json.RawMessage(
		`{"team_id":"`+teamID+`","task_id":"`+task.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !second.IsError {
		t.Fatal("duplicate complete must be rejected (idempotence guard)")
	}
}
