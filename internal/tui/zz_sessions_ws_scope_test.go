package tui

// The sessions panel must default to the CURRENT workspace's sessions only:
// the store is global (every workspace's sessions in one directory), and the
// old unfiltered list mixed unrelated workspaces' sessions into the panel -
// each shown "locked" by whichever live instance was working in that
// workspace. A toggles to all-workspaces (user expectation: with 2 live
// instances, at most 2 sessions should appear locked in one's own workspace
// view).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

func TestBuildSessionInspectorItemsDefaultFiltersToCurrentWorkspace(t *testing.T) {
	dir := t.TempDir()
	here, _ := os.Getwd()
	other := filepath.Join(dir, "other-ws")
	same := []*session.Session{
		{ID: "cur-1", Workspace: here, Title: "current one"},
		{ID: "other-1", Workspace: other, Title: "other one"},
		{ID: "other-2", Workspace: "", Title: "legacy no-workspace"},
	}
	items := buildSessionInspectorItems(same, LangEnglish, dir, nil, false)
	if len(items) != 1 {
		t.Fatalf("default scope must list ONLY current-workspace sessions, got %d items", len(items))
	}
	if items[0].ID != "cur-1" {
		t.Fatalf("expected cur-1, got %s", items[0].ID)
	}
}

func TestBuildSessionInspectorItemsEmptyCurrentWorkspaceShowsHint(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other-ws")
	sessions := []*session.Session{{ID: "other-1", Workspace: other, Title: "elsewhere"}}
	items := buildSessionInspectorItems(sessions, LangEnglish, dir, nil, false)
	if len(items) != 1 || !items[0].Disabled {
		t.Fatalf("empty current-workspace scope must yield one disabled hint item, got %+v", items)
	}
	if items[0].Title == "" || items[0].Summary == "" {
		t.Fatalf("hint item must carry title and summary, got %+v", items[0])
	}
}

func TestBuildSessionInspectorItemsAllWorkspacesToggle(t *testing.T) {
	dir := t.TempDir()
	here, _ := os.Getwd()
	other := filepath.Join(dir, "other-ws")
	sessions := []*session.Session{
		{ID: "cur-1", Workspace: here},
		{ID: "other-1", Workspace: other},
	}
	items := buildSessionInspectorItems(sessions, LangEnglish, dir, nil, true)
	if len(items) != 2 {
		t.Fatalf("all-workspaces scope must list everything, got %d", len(items))
	}
}
