package main

import (
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/plugin"
)

func TestMCPSnapshotToWebUI(t *testing.T) {
	snapshot := []plugin.MCPServerInfo{
		{Name: "github", Status: plugin.MCPStatusConnected, ToolNames: []string{"search_code", "list_issues"}, Error: ""},
		{Name: "slack", Status: plugin.MCPStatusPending, Error: ""},
		{Name: "disabled-server", Status: plugin.MCPStatusFailed, Disabled: true, Error: "connection refused"},
	}

	result := mcpSnapshotToWebUI(snapshot)
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}

	gh, ok := result["github"]
	if !ok {
		t.Fatal("expected 'github' entry")
	}
	if !gh.Connected {
		t.Error("expected github Connected=true")
	}
	if gh.Pending {
		t.Error("expected github Pending=false")
	}
	if len(gh.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(gh.Tools))
	}

	slack := result["slack"]
	if slack.Connected {
		t.Error("expected slack Connected=false")
	}
	if !slack.Pending {
		t.Error("expected slack Pending=true")
	}

	dis := result["disabled-server"]
	if !dis.Disabled {
		t.Error("expected Disabled=true")
	}
	if dis.Error != "connection refused" {
		t.Errorf("expected error 'connection refused', got %q", dis.Error)
	}
}

func TestMCPSnapshotToWebUI_Empty(t *testing.T) {
	result := mcpSnapshotToWebUI(nil)
	if result == nil {
		t.Error("expected non-nil map")
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}

func TestA2AInstancesToWebUI(t *testing.T) {
	instances := []a2a.InstanceInfo{
		{ID: "proj-a", Workspace: "/home/user/proj-a", Endpoint: "http://localhost:8080", Status: "ready", StartedAt: "2025-01-01T00:00:00Z"},
		{ID: "proj-b", Workspace: "/home/user/proj-b", Endpoint: "http://localhost:8081", Status: "busy", StartedAt: "2025-01-01T01:00:00Z"},
	}

	result := a2aInstancesToWebUI(instances)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}

	if result[0].ID != "proj-a" {
		t.Errorf("expected ID 'proj-a', got %q", result[0].ID)
	}
	if result[0].Status != "ready" {
		t.Errorf("expected Status 'ready', got %q", result[0].Status)
	}
	if result[0].Endpoint != "http://localhost:8080" {
		t.Errorf("wrong endpoint: %q", result[0].Endpoint)
	}
	if result[0].Workspace != "/home/user/proj-a" {
		t.Errorf("wrong workspace: %q", result[0].Workspace)
	}

	if result[1].Status != "busy" {
		t.Errorf("expected Status 'busy', got %q", result[1].Status)
	}
}

func TestA2AInstancesToWebUI_Empty(t *testing.T) {
	result := a2aInstancesToWebUI(nil)
	if result == nil {
		t.Error("expected non-nil slice")
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestIMSnapshotToWebUI(t *testing.T) {
	snap := im.StatusSnapshot{
		Adapters: []im.AdapterState{
			{Name: "feishu", Platform: im.PlatformFeishu, Healthy: true, Status: "running"},
			{Name: "discord", Platform: im.PlatformDiscord, Healthy: false, Status: "stopped", LastError: "auth failed"},
		},
	}

	result := imSnapshotToWebUI(snap, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}

	if result[0].Adapter != "feishu" {
		t.Errorf("expected Adapter 'feishu', got %q", result[0].Adapter)
	}
	if result[0].Platform != string(im.PlatformFeishu) {
		t.Errorf("expected Platform 'feishu', got %q", result[0].Platform)
	}
	if !result[0].Healthy {
		t.Error("expected feishu Healthy=true")
	}

	if result[1].Adapter != "discord" {
		t.Errorf("expected Adapter 'discord', got %q", result[1].Adapter)
	}
	if result[1].Healthy {
		t.Error("expected discord Healthy=false")
	}
	if result[1].LastError != "auth failed" {
		t.Errorf("expected LastError 'auth failed', got %q", result[1].LastError)
	}
}

func TestIMSnapshotToWebUI_Empty(t *testing.T) {
	result := imSnapshotToWebUI(im.StatusSnapshot{}, nil)
	if result == nil {
		t.Error("expected non-nil slice")
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}
