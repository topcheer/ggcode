package a2a

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #5632: list defaults to THIS workspace's fleet - foreign-workspace seats
// collapse to one summary line; all=true expands everyone.
func TestListInstancesWorkspaceDefault(t *testing.T) {
	reg := &Registry{}
	reg.selfInfo = &InstanceInfo{ID: "self", Workspace: "/ws/home"}

	rt := &RemoteTool{
		registry: reg,
		cache: []InstanceInfo{
			{ID: "s-1", Workspace: "/ws/home", Endpoint: "http://h:1", Status: "ready", Nick: "seat_one"},
			{ID: "s-2", Workspace: "/ws/away", Endpoint: "http://h:2", Status: "ready"},
		},
		cacheAt: time.Now(),
	}

	got, err := rt.listInstances(context.Background(), false)
	if err != nil {
		t.Fatalf("listInstances: %v", err)
	}
	s := got.Content
	if !strings.Contains(s, "Seat: seat_one") {
		t.Fatalf("same-workspace seat missing:\n%s", s)
	}
	if !strings.Contains(s, "this workspace") {
		t.Fatalf("header does not note the default filter:\n%s", s)
	}
	if !strings.Contains(s, "External workspaces") || !strings.Contains(s, "/ws/away×1") {
		t.Fatalf("foreign seat not collapsed to summary line:\n%s", s)
	}
	if strings.Contains(s, "http://h:2") {
		t.Fatalf("foreign endpoint leaked into default view:\n%s", s)
	}

	all, err := rt.listInstances(context.Background(), true)
	if err != nil {
		t.Fatalf("listInstances all: %v", err)
	}
	if !strings.Contains(all.Content, "http://h:2") {
		t.Fatalf("all=true did not expand foreign seat:\n%s", all.Content)
	}
}

// #5340: identical instance sets in different discovery orders must render
// byte-identical text (prompt-cache stability for the Remote Agents section).
func TestFormatRemoteAgents_DeterministicOrder(t *testing.T) {
	inst := []InstanceInfo{
		{ID: "i-2", Workspace: "/w/charlie", Status: "online"},
		{ID: "i-1", Workspace: "/w/bravo", Status: "online"},
		{ID: "i-3", Workspace: "/w/alpha", Status: "online"},
	}
	meta := map[string]RemoteAgentMeta{
		"i-1": {Team: "ggcode", Role: "reviewer", Languages: []string{"go"}},
	}
	a := FormatRemoteAgents(inst, meta)
	b := FormatRemoteAgents([]InstanceInfo{inst[1], inst[2], inst[0]}, meta)
	if a == "" || a != b {
		t.Fatalf("rendering not order-stable:\n---\n%s\n---\n%s", a, b)
	}
	if i, j := strings.Index(a, "alpha"), strings.Index(a, "bravo"); !(i < j) {
		t.Fatalf("not name-sorted:\n%s", a)
	}
}
