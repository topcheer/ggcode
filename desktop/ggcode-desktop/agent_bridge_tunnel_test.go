package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
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

func TestDesktopToolLabelHelpersUseSharedPresentation(t *testing.T) {
	rawArgs := `{"path":"/tmp/test.txt"}`
	if got := toolDisplayName("read_file", rawArgs); got != "Read" {
		t.Fatalf("expected shared display name, got %q", got)
	}
	if got := toolArgSummary("read_file", rawArgs); got != "/tmp/test.txt" {
		t.Fatalf("expected shared detail, got %q", got)
	}
	if got := toolDescription("read_file", rawArgs); got != "Read /tmp/test.txt" {
		t.Fatalf("expected inline shared description, got %q", got)
	}
}

func TestDesktopToolDescriptionUsesSharedCommandPresentation(t *testing.T) {
	rawArgs := `{"command":"echo hi","description":"Run smoke test"}`
	if got := toolDisplayName("run_command", rawArgs); got != "Run smoke test" {
		t.Fatalf("expected description-backed display name, got %q", got)
	}
	if got := toolArgSummary("run_command", rawArgs); got != "" {
		t.Fatalf("expected empty detail for described command, got %q", got)
	}
	if got := toolDescription("run_command", rawArgs); got != "Run smoke test" {
		t.Fatalf("expected shared command description, got %q", got)
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

func TestCurrentTunnelSnapshotHistoryMergesIncompleteLedgerTail(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	bridge.currentSes = &session.Session{
		ID:        "sess-desktop-tail",
		CreatedAt: time.Now().Add(-time.Minute),
		UpdatedAt: time.Now(),
		TunnelEvents: []session.TunnelEvent{
			{
				EventID: "ev-000000001",
				Type:    tunnel.EventUserMessage,
				Data:    []byte(`{"text":"tui 不用改是么?"}`),
			},
			{
				EventID:  "ev-000000002",
				StreamID: "msg-1",
				Type:     tunnel.EventText,
				Data:     []byte(`{"id":"msg-1","chunk":"不用改。","done":false}`),
			},
			{
				EventID:  "ev-000000003",
				StreamID: "msg-1",
				Type:     tunnel.EventTextDone,
				Data:     []byte(`{"id":"msg-1","done":true}`),
			},
		},
	}
	bridge.ui.ChatMsgs = []ChatMessage{
		{Role: "user", Content: "tui 不用改是么?", Time: time.Now()},
	}

	history := bridge.CurrentTunnelSnapshotHistory()
	if len(history) != 2 {
		t.Fatalf("expected merged snapshot history, got %d entries: %+v", len(history), history)
	}
	if history[0].Role != "user" || history[0].Content != "tui 不用改是么?" {
		t.Fatalf("unexpected first history entry: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "不用改。" {
		t.Fatalf("expected trailing assistant reply to be preserved, got %+v", history[1])
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

func TestCurrentSessionTunnelEventsPrefersProjectionStoreDesktop(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	projectionStore, err := tunnel.NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new projection store: %v", err)
	}
	bridge.sessionStore = store
	bridge.tunnelProjectionStore = projectionStore
	bridge.currentSes = &session.Session{
		ID:                   "sess-desktop-projection",
		CreatedAt:            time.Now().Add(-time.Minute),
		UpdatedAt:            time.Now(),
		TunnelEventsComplete: true,
		TunnelEvents: []session.TunnelEvent{
			{EventID: "legacy-1", Type: tunnel.EventUserMessage, Data: []byte(`{"text":"legacy"}`)},
		},
	}
	if err := projectionStore.Append(tunnel.GatewayMessage{
		SessionID: "sess-desktop-projection",
		EventID:   "projection-1",
		Type:      tunnel.EventUserMessage,
		Data:      []byte(`{"text":"projection"}`),
	}); err != nil {
		t.Fatalf("append projection event: %v", err)
	}

	events := bridge.CurrentSessionTunnelEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 projected event, got %d", len(events))
	}
	if events[0].EventID != "projection-1" {
		t.Fatalf("expected projection replay to win, got %q", events[0].EventID)
	}
}

func TestDesktopSubagentCompleteBypassesThrottledUpdate(t *testing.T) {
	bridge := NewAgentBridge(&config.Config{}, nil, &config.ResolvedEndpoint{}, t.TempDir(), NewUIState())
	bridge.subAgentMgr = subagent.NewManager(config.SubAgentConfig{})
	bridge.registerSubagentCallbacks()

	broker := tunnel.NewBroker(nil)
	t.Cleanup(broker.Stop)
	bridge.AttachTunnelBroker(broker)

	var recorded []tunnel.GatewayMessage
	broker.SetEventRecorder(func(msg tunnel.GatewayMessage) {
		recorded = append(recorded, msg)
	})

	agentID := bridge.subAgentMgr.Spawn("researcher", "check mobile state", "", nil, context.Background())
	if ok := bridge.subAgentMgr.SetCancel(agentID, func() {}); !ok {
		t.Fatal("expected SetCancel to mark subagent running")
	}
	bridge.subAgentMgr.Complete(agentID, "done", nil)

	var sawSpawn, sawComplete bool
	for _, msg := range recorded {
		switch msg.Type {
		case tunnel.EventSubagentSpawn:
			var data tunnel.SubagentSpawnData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				t.Fatalf("unmarshal spawn: %v", err)
			}
			if data.AgentID == agentID {
				sawSpawn = true
			}
		case tunnel.EventSubagentComplete:
			var data tunnel.SubagentCompleteData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				t.Fatalf("unmarshal complete: %v", err)
			}
			if data.AgentID == agentID {
				sawComplete = true
			}
		}
	}

	if !sawSpawn {
		t.Fatal("expected subagent spawn to be recorded")
	}
	if !sawComplete {
		t.Fatal("expected subagent completion to be recorded even when updates are throttled")
	}

	sa, ok := bridge.subAgentMgr.Get(agentID)
	if !ok {
		t.Fatalf("expected subagent %s to exist", agentID)
	}
	if got := agentPanelFromSubAgent(sa).Status; got != string(subagent.StatusCompleted) {
		t.Fatalf("expected completed UI panel status, got %q", got)
	}
}

func TestDesktopFlushTunnelTextStreamFallbackAdvancesMsgID(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	broker := tunnel.NewBroker(nil)
	t.Cleanup(broker.Stop)

	var recorded []tunnel.GatewayMessage
	broker.SetEventRecorder(func(msg tunnel.GatewayMessage) {
		recorded = append(recorded, msg)
	})

	bridge.tunnelBroker = broker
	bridge.tunnelMsgID = broker.NextMessageID()
	firstMsgID := bridge.tunnelMsgID
	broker.PushText(firstMsgID, "hello")
	bridge.markTunnelMainStreamActive()

	bridge.flushTunnelTextStream(broker, false)

	if bridge.tunnelMsgID == firstMsgID {
		t.Fatal("expected fallback flush to advance tunnel msg id")
	}

	var textDone bool
	for _, msg := range recorded {
		switch msg.Type {
		case tunnel.EventTextDone:
			textDone = true
		}
	}
	if !textDone {
		t.Fatal("expected text_done event from fallback flush")
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
		Status:      tunnel.StatusData{Status: tunnel.StatusBusy},
		Activity:    tunnel.ActivityData{Activity: "processing"},
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

func TestAgentBridgeSetupAgentRegistersCronTools(t *testing.T) {
	bridge := NewAgentBridge(&config.Config{}, nil, &config.ResolvedEndpoint{}, t.TempDir(), NewUIState())
	bridge.currentSes = session.NewSession("", "", "")
	if err := bridge.setupAgent(); err != nil {
		t.Fatalf("setupAgent: %v", err)
	}
	for _, name := range []string{"cron_create", "cron_delete", "cron_list"} {
		if _, ok := bridge.registry.Get(name); !ok {
			t.Fatalf("expected %s to be registered", name)
		}
	}
}

func TestAgentBridgeHandleCronPromptWhileWorkingQueuesHidden(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	bridge.working = true

	bridge.handleCronPrompt("check status")

	if len(bridge.ui.ChatMsgs) != 1 || bridge.ui.ChatMsgs[0].Role != "system" {
		t.Fatalf("expected cron system message in UI, got %+v", bridge.ui.ChatMsgs)
	}
	pending, ok := bridge.drainPending()
	if !ok {
		t.Fatal("expected hidden cron prompt to be queued")
	}
	if !pending.Hidden || pending.Text != "check status" {
		t.Fatalf("unexpected pending entry: %+v", pending)
	}
}

func TestAgentBridgeHandleCronPromptPushesCronTunnelEvent(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	bridge.working = true
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	bridge.sessionStore = store
	bridge.currentSes = &session.Session{ID: "sess-cron", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	sess := tunnel.NewSession(tunnel.DefaultRelayURL)
	broker := tunnel.NewBroker(sess)
	broker.Stop()
	broker.SetEventRecorder(bridge.RecordTunnelEvent)
	bridge.tunnelBroker = broker

	bridge.handleCronPrompt("check status")

	var sawCron bool
	for _, ev := range bridge.currentSes.TunnelEvents {
		switch ev.Type {
		case tunnel.EventUserMessage:
			var data tunnel.MessageData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				t.Fatalf("unmarshal user_message: %v", err)
			}
			if data.Kind == tunnel.MessageKindCron {
				sawCron = true
				if data.Text != "check status" || data.DisplayText != "⏰ Cron job triggered" {
					t.Fatalf("unexpected cron message data: %+v", data)
				}
			}
		case tunnel.EventSystemMessage:
			t.Fatalf("did not expect cron to emit system_message tunnel event: %s", ev.Data)
		}
	}
	if !sawCron {
		t.Fatal("expected cron user_message tunnel event")
	}
}

func TestAgentBridgeEnsureSessionCreatesProjectionBrokerDesktop(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	projectionStore, err := tunnel.NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("new projection store: %v", err)
	}
	bridge.sessionStore = store
	bridge.tunnelProjectionStore = projectionStore
	bridge.ensureSession()
	bridge.working = true

	bridge.handleCronPrompt("check status")

	events := bridge.CurrentSessionTunnelEvents()
	if len(events) != 1 {
		t.Fatalf("expected cron event to be persisted through projection broker, got %d", len(events))
	}
	if events[0].Type != tunnel.EventUserMessage {
		t.Fatalf("expected user_message event, got %q", events[0].Type)
	}
	var data tunnel.MessageData
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatalf("unmarshal message data: %v", err)
	}
	if data.Kind != tunnel.MessageKindCron || data.Text != "check status" {
		t.Fatalf("unexpected cron projection payload: %+v", data)
	}
}

func TestAgentBridgeDrainPendingInterruptHiddenSkipsPersistence(t *testing.T) {
	bridge := NewAgentBridge(nil, nil, nil, t.TempDir(), NewUIState())
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	bridge.sessionStore = store
	bridge.currentSes = &session.Session{ID: "sess-hidden", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	bridge.QueueHiddenMessage("check status")

	got := bridge.drainPendingInterrupt()
	if got != "check status" {
		t.Fatalf("unexpected drained text: %q", got)
	}
	if len(bridge.currentSes.Messages) != 0 {
		t.Fatalf("expected hidden pending to skip persistence, got %d messages", len(bridge.currentSes.Messages))
	}
}
