package agentruntime

import (
	"reflect"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestGroupWorkspaceSessions(t *testing.T) {
	workingDir := t.TempDir()
	sessions := []*session.Session{
		{ID: "current", Workspace: workingDir},
		{ID: "other", Workspace: t.TempDir()},
		nil,
	}

	current, others := GroupWorkspaceSessions(sessions, workingDir)
	if len(current) != 1 || current[0].ID != "current" {
		t.Fatalf("current sessions = %+v", current)
	}
	if len(others) != 1 || others[0].ID != "other" {
		t.Fatalf("other sessions = %+v", others)
	}
}

func TestFilterWorkspaceSessionsSortsAndMatchesNormalizedWorkspace(t *testing.T) {
	workingDir := t.TempDir()
	normalized := session.NormalizeWorkspacePath(workingDir)
	newer := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	older := time.Date(2026, 1, 1, 3, 4, 5, 0, time.UTC)

	sessions := []*session.Session{
		{ID: "other", Workspace: t.TempDir(), UpdatedAt: newer.Add(time.Hour)},
		{ID: "old", Workspace: workingDir, UpdatedAt: older},
		nil,
		{ID: "new", Workspace: normalized, UpdatedAt: newer},
	}

	filtered := FilterWorkspaceSessions(sessions, workingDir)
	gotIDs := make([]string, 0, len(filtered))
	for _, ses := range filtered {
		gotIDs = append(gotIDs, ses.ID)
	}
	wantIDs := []string{"new", "old"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("filtered IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func TestSummarizeWorkspaceSessions(t *testing.T) {
	workingDir := t.TempDir()
	updated := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	sessions := []*session.Session{{
		ID:        "s1",
		Title:     "Test session",
		Workspace: workingDir,
		Vendor:    "openai",
		Endpoint:  "default",
		Model:     "gpt-test",
		UpdatedAt: updated,
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}}

	summaries := SummarizeWorkspaceSessions(sessions, workingDir)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	got := summaries[0]
	if got.ID != "s1" || got.Title != "Test session" || got.Workspace != workingDir || got.Vendor != "openai" || got.Endpoint != "default" || got.Model != "gpt-test" || got.MsgCount != 2 || !got.UpdatedAt.Equal(updated) {
		t.Fatalf("unexpected summary: %+v", got)
	}
}

func TestRegisterCronTools(t *testing.T) {
	registry := tool.NewRegistry()
	scheduler := NewWorkspaceCronScheduler(t.TempDir(), nil)
	defer scheduler.Shutdown()
	RegisterCronTools(registry, scheduler)

	for _, name := range []string{"cron_create", "cron_delete", "cron_list"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("expected %s to be registered", name)
		}
	}

	RegisterCronTools(nil, scheduler)
	RegisterCronTools(registry, nil)
}
