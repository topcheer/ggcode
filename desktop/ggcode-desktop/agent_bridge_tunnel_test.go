package main

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestBuildTunnelAskUserQuestionsPreservesPromptMetadata(t *testing.T) {
	req := tool.AskUserRequest{
		Title: "Clarify rollout",
		Questions: []tool.AskUserQuestion{
			{
				ID:            "scope",
				Title:         "Scope",
				Prompt:        "Which scope should we use?",
				Kind:          tool.AskUserKindSingle,
				AllowFreeform: true,
				Placeholder:   "Optional notes",
				Choices:       []tool.AskUserChoice{{ID: "small", Label: "Small"}},
			},
		},
	}

	got := buildTunnelAskUserQuestions(req)
	if len(got) != 1 {
		t.Fatalf("expected 1 question, got %d", len(got))
	}
	if got[0].Prompt != "Which scope should we use?" {
		t.Fatalf("expected prompt to be preserved, got %q", got[0].Prompt)
	}
	if !got[0].AllowFreeform {
		t.Fatal("expected allow_freeform to be preserved")
	}
	if got[0].Placeholder != "Optional notes" {
		t.Fatalf("expected placeholder to be preserved, got %q", got[0].Placeholder)
	}
}

func TestBuildAskUserResponseFromTunnelBuildsStructuredAnswers(t *testing.T) {
	req := tool.AskUserRequest{
		Title: "Clarify rollout",
		Questions: []tool.AskUserQuestion{
			{
				ID:      "scope",
				Title:   "Scope",
				Prompt:  "Which scope should we use?",
				Kind:    tool.AskUserKindSingle,
				Choices: []tool.AskUserChoice{{ID: "small", Label: "Small"}},
			},
			{
				ID:     "notes",
				Title:  "Notes",
				Prompt: "Anything else?",
				Kind:   tool.AskUserKindText,
			},
		},
	}

	resp := buildAskUserResponseFromTunnel(req, tool.AskUserStatusSubmitted, []tunnel.AskUserAnswer{
		{QuestionID: "scope", ChoiceIDs: []string{"small"}},
		{QuestionID: "notes", FreeformText: "Ship tonight"},
	})

	if resp.Status != tool.AskUserStatusSubmitted {
		t.Fatalf("expected submitted status, got %q", resp.Status)
	}
	if resp.AnsweredCount != 2 {
		t.Fatalf("expected answered_count=2, got %d", resp.AnsweredCount)
	}
	if resp.Answers[0].SelectedChoices[0] != "Small" {
		t.Fatalf("expected selected choice label, got %+v", resp.Answers[0].SelectedChoices)
	}
	if !resp.Answers[1].Answered {
		t.Fatal("expected text answer to be marked answered")
	}
}

func TestDesktopChatMessagesToTunnelHistoryPreservesSystemAndToolBoundaries(t *testing.T) {
	history := desktopChatMessagesToTunnelHistory([]ChatMessage{
		{Role: "user", Content: "check release", Time: time.Now()},
		{Role: "system", Content: "rerun is still running", Time: time.Now()},
		{Role: "assistant", Content: "I checked the current run.", Time: time.Now()},
		{
			Role:     "tool",
			ToolName: "run_command",
			ToolID:   "tool-1",
			ToolArgs: "gh run list --limit 3",
			ToolRaw:  `{"command":"gh run list --limit 3"}`,
			Content:  "completed success release",
			Time:     time.Now(),
		},
		{Role: "assistant", Content: "The rerun completed successfully.", Time: time.Now()},
	})

	if len(history) != 6 {
		t.Fatalf("expected 6 history entries, got %d: %+v", len(history), history)
	}
	if history[1].Role != "system" || history[1].Content != "rerun is still running" {
		t.Fatalf("unexpected system history entry: %+v", history[1])
	}
	if history[3].Role != "tool_call" || history[4].Role != "tool_result" {
		t.Fatalf("expected tool call/result entries, got %+v", history[3:])
	}
	if history[5].Role != "assistant" || history[5].Content != "The rerun completed successfully." {
		t.Fatalf("unexpected trailing assistant entry: %+v", history[5])
	}
}

func TestPrepareCurrentSessionTunnelLedgerDowngradesPartialReplayLedgerDesktop(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	bridge.sessionStore = store
	bridge.currentSes = &session.Session{
		ID:        "sess-desktop",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("check release")}},
			{Role: "assistant", Content: []provider.ContentBlock{provider.TextBlock("I checked the current run.")}},
		},
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{
				EventID: "ev-000000010",
				Type:    tunnel.EventToolCall,
				Data:    []byte(`{"tool_id":"tool-1","tool_name":"run_command","display_name":"Check status","args":"{\"command\":\"gh run list --limit 3\"}","detail":"gh run list --limit 3"}`),
			},
		},
	}
	if err := store.Save(bridge.currentSes); err != nil {
		t.Fatalf("save session: %v", err)
	}

	bridge.PrepareCurrentSessionTunnelLedger()

	if bridge.currentSes.TunnelEventsComplete {
		t.Fatal("expected partial replay ledger to be downgraded")
	}
}

func TestResetCurrentSessionTunnelLedgerDesktopClearsCanonicalReplay(t *testing.T) {
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), nil)
	bridge.sessionStore = store
	bridge.currentSes = &session.Session{
		ID:                   "sess-desktop-reset",
		CreatedAt:            time.Now().Add(-time.Minute),
		UpdatedAt:            time.Now(),
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{EventID: "ev-000000001", Type: tunnel.EventUserMessage, Data: []byte(`{"text":"hello"}`)},
		},
	}
	if err := store.Save(bridge.currentSes); err != nil {
		t.Fatalf("save session: %v", err)
	}

	bridge.ResetCurrentSessionTunnelLedger()

	if len(bridge.currentSes.TunnelEvents) != 0 {
		t.Fatalf("expected reset ledger to clear tunnel events, got %d", len(bridge.currentSes.TunnelEvents))
	}
	if bridge.currentSes.TunnelEventsComplete {
		t.Fatal("expected reset ledger to require fresh canonical replay")
	}
}

func TestDesktopTunnelSnapshotMatchesDetectsMidShareProjectionGap(t *testing.T) {
	seeded := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		Status:      tunnel.StatusData{Status: tunnel.StatusRunning, Message: "bash"},
		History: []tunnel.HistoryEntry{
			{Role: "system", Content: "Starting tunnel..."},
			{Role: "tool_call", ToolID: "tool-1", ToolName: "bash", ToolDisplayName: "Run bash", ToolArgs: `{"command":"sleep 1"}`},
		},
	}
	latest := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{Workspace: "/tmp/project", Version: "dev"},
		Status:      tunnel.StatusData{Status: tunnel.StatusIdle, Message: ""},
		History: []tunnel.HistoryEntry{
			{Role: "system", Content: "Starting tunnel..."},
			{Role: "tool_call", ToolID: "tool-1", ToolName: "bash", ToolDisplayName: "Run bash", ToolArgs: `{"command":"sleep 1"}`},
			{Role: "tool_result", ToolID: "tool-1", ToolName: "bash", Result: "done"},
			{Role: "assistant", Content: "All builds are running."},
		},
	}

	if desktopTunnelSnapshotMatches(seeded, latest) {
		t.Fatal("expected changed live projection to force snapshot reseed")
	}
	if !desktopTunnelSnapshotMatches(latest, latest) {
		t.Fatal("expected identical snapshots to match")
	}
}
