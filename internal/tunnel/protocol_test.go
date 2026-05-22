//go:build !integration

package tunnel

import (
	"encoding/json"
	"testing"
)

// --- GatewayMessage JSON round-trip ---

func TestGatewayMessageJSON(t *testing.T) {
	msg := GatewayMessage{
		SessionID: "sess-1",
		EventID:   "ev-000000001",
		StreamID:  "msg-1",
		Type:      "text",
		Data:      json.RawMessage(`{"id":"msg-1","chunk":"hello"}`),
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var got GatewayMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "sess-1" || got.EventID != "ev-000000001" || got.StreamID != "msg-1" {
		t.Errorf("metadata mismatch: %+v", got)
	}
	if got.Type != "text" {
		t.Errorf("Type = %q, want %q", got.Type, "text")
	}
	if string(got.Data) != `{"id":"msg-1","chunk":"hello"}` {
		t.Errorf("Data = %s, want original", got.Data)
	}
}

func TestGatewayMessageOmitEmpty(t *testing.T) {
	msg := GatewayMessage{Type: "ping"}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["session_id"]; ok {
		t.Error("session_id should be omitted when empty")
	}
	if _, ok := m["data"]; ok {
		t.Error("data should be omitted when nil")
	}
}

// --- SessionInfoData ---

func TestSessionInfoDataJSON(t *testing.T) {
	d := SessionInfoData{
		Workspace: "/home/user/project",
		Model:     "gpt-4",
		Provider:  "openai",
		Mode:      "auto",
		Version:   "1.3.10",
		Language:  "zh-CN",
		Theme:     "midnight",
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got SessionInfoData
	json.Unmarshal(b, &got)
	if got.Workspace != d.Workspace || got.Model != d.Model || got.Mode != d.Mode || got.Language != d.Language || got.Theme != d.Theme {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// --- TextData ---

func TestTextDataJSON(t *testing.T) {
	d := TextData{ID: "msg-1", Chunk: "hello world", Done: false}
	b, _ := json.Marshal(d)
	var got TextData
	json.Unmarshal(b, &got)
	if got.ID != "msg-1" || got.Chunk != "hello world" || got.Done != false {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestTextDataDoneTrue(t *testing.T) {
	d := TextData{ID: "msg-2", Done: true}
	b, _ := json.Marshal(d)
	var got TextData
	json.Unmarshal(b, &got)
	if !got.Done {
		t.Error("Done should be true")
	}
}

// --- StatusData ---

func TestStatusDataJSON(t *testing.T) {
	d := StatusData{Status: "thinking", Message: "processing"}
	b, _ := json.Marshal(d)
	var got StatusData
	json.Unmarshal(b, &got)
	if got.Status != "thinking" || got.Message != "processing" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestStatusDataOmitEmptyMessage(t *testing.T) {
	d := StatusData{Status: "idle"}
	b, _ := json.Marshal(d)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["message"]; ok {
		t.Error("message should be omitted when empty")
	}
}

// --- ToolCallData ---

func TestToolCallDataJSON(t *testing.T) {
	d := ToolCallData{
		ToolID:      "t1",
		ToolName:    "read_file",
		DisplayName: "Inspect config",
		Args:        `{"path":"/tmp/f"}`,
		Detail:      "reading file",
	}
	b, _ := json.Marshal(d)
	var got ToolCallData
	json.Unmarshal(b, &got)
	if got.ToolID != "t1" || got.ToolName != "read_file" {
		t.Errorf("mismatch: %+v", got)
	}
}

// --- ToolResultData ---

func TestToolResultDataJSON(t *testing.T) {
	d := ToolResultData{
		ToolID:   "t1",
		ToolName: "run_command",
		Result:   "ok",
		IsError:  true,
	}
	b, _ := json.Marshal(d)
	var got ToolResultData
	json.Unmarshal(b, &got)
	if !got.IsError {
		t.Error("IsError should be true")
	}
}

// --- ApprovalRequestData ---

func TestApprovalRequestDataJSON(t *testing.T) {
	d := ApprovalRequestData{
		ID:       "appr-1",
		ToolName: "run_command",
		Input:    "rm -rf /",
	}
	b, _ := json.Marshal(d)
	var got ApprovalRequestData
	json.Unmarshal(b, &got)
	if got.ID != "appr-1" || got.ToolName != "run_command" || got.Input != "rm -rf /" {
		t.Errorf("mismatch: %+v", got)
	}
}

// --- ApprovalResponseData ---

func TestApprovalResponseDataJSON(t *testing.T) {
	d := ApprovalResponseData{ID: "appr-1", Decision: "allow"}
	b, _ := json.Marshal(d)
	var got ApprovalResponseData
	json.Unmarshal(b, &got)
	if got.Decision != "allow" {
		t.Errorf("mismatch: %+v", got)
	}
}

// --- MessageData ---

func TestMessageDataJSON(t *testing.T) {
	d := MessageData{Text: "hello agent"}
	b, _ := json.Marshal(d)
	var got MessageData
	json.Unmarshal(b, &got)
	if got.Text != "hello agent" {
		t.Errorf("mismatch: %+v", got)
	}
}

// --- ModeChangeData ---

func TestModeChangeDataJSON(t *testing.T) {
	d := ModeChangeData{Mode: "auto"}
	b, _ := json.Marshal(d)
	var got ModeChangeData
	json.Unmarshal(b, &got)
	if got.Mode != "auto" {
		t.Errorf("mismatch: %+v", got)
	}
}

// --- ErrorData ---

func TestErrorDataJSON(t *testing.T) {
	d := ErrorData{Message: "something went wrong", Code: "E001"}
	b, _ := json.Marshal(d)
	var got ErrorData
	json.Unmarshal(b, &got)
	if got.Message != "something went wrong" || got.Code != "E001" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestErrorDataOmitEmptyCode(t *testing.T) {
	d := ErrorData{Message: "fail"}
	b, _ := json.Marshal(d)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	if _, ok := m["code"]; ok {
		t.Error("code should be omitted when empty")
	}
}

// --- AskUser types ---

func TestAskUserRequestDataJSON(t *testing.T) {
	d := AskUserRequestData{
		ID:    "ask-1",
		Title: "Confirm",
		Questions: []AskUserQuestion{
			{
				ID:     "q1",
				Prompt: "Continue?",
				Kind:   "single",
				Choices: []AskUserChoice{
					{ID: "y", Label: "Yes"},
					{ID: "n", Label: "No"},
				},
				AllowFreeform: true,
				Placeholder:   "or type...",
			},
		},
	}
	b, _ := json.Marshal(d)
	var got AskUserRequestData
	json.Unmarshal(b, &got)
	if got.ID != "ask-1" || len(got.Questions) != 1 {
		t.Fatalf("mismatch: %+v", got)
	}
	q := got.Questions[0]
	if q.Kind != "single" || len(q.Choices) != 2 || !q.AllowFreeform {
		t.Errorf("question mismatch: %+v", q)
	}
}

func TestAskUserResponseDataJSON(t *testing.T) {
	d := AskUserResponseData{
		ID:     "ask-1",
		Status: "submitted",
		Answers: []AskUserAnswer{
			{
				QuestionID:   "q1",
				ChoiceIDs:    []string{"y"},
				FreeformText: "yes please",
			},
		},
	}
	b, _ := json.Marshal(d)
	var got AskUserResponseData
	json.Unmarshal(b, &got)
	if got.Status != "submitted" || len(got.Answers) != 1 {
		t.Errorf("mismatch: %+v", got)
	}
	if got.Answers[0].FreeformText != "yes please" {
		t.Errorf("freeform mismatch: %s", got.Answers[0].FreeformText)
	}
}

// --- Sub-agent types ---

func TestSubagentSpawnDataJSON(t *testing.T) {
	d := SubagentSpawnData{
		AgentID:  "agent-1",
		Name:     "Researcher",
		Task:     "find bugs",
		Color:    "#4CAF50",
		ParentID: "parent-1",
	}
	b, _ := json.Marshal(d)
	var got SubagentSpawnData
	json.Unmarshal(b, &got)
	if got.AgentID != "agent-1" || got.Name != "Researcher" || got.Task != "find bugs" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestSubagentTextDataJSON(t *testing.T) {
	d := SubagentTextData{
		AgentID: "agent-1",
		ID:      "msg-1",
		Chunk:   "hello",
		Done:    false,
	}
	b, _ := json.Marshal(d)
	var got SubagentTextData
	json.Unmarshal(b, &got)
	if got.AgentID != "agent-1" || got.Chunk != "hello" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestSubagentStatusDataJSON(t *testing.T) {
	d := SubagentStatusData{
		AgentID: "agent-1",
		Status:  "running",
		Message: "working on it",
	}
	b, _ := json.Marshal(d)
	var got SubagentStatusData
	json.Unmarshal(b, &got)
	if got.Status != "running" || got.Message != "working on it" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestSubagentCompleteDataJSON(t *testing.T) {
	d := SubagentCompleteData{
		AgentID: "agent-1",
		Name:    "Researcher",
		Summary: "found 3 bugs",
		Success: true,
	}
	b, _ := json.Marshal(d)
	var got SubagentCompleteData
	json.Unmarshal(b, &got)
	if !got.Success || got.Summary != "found 3 bugs" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestSubagentToolCallDataJSON(t *testing.T) {
	d := SubagentToolCallData{
		AgentID:     "agent-1",
		ToolID:      "t1",
		ToolName:    "search",
		DisplayName: "Find TODOs",
		Args:        `{"pattern":"TODO"}`,
		Detail:      "searching",
	}
	b, _ := json.Marshal(d)
	var got SubagentToolCallData
	json.Unmarshal(b, &got)
	if got.AgentID != "agent-1" || got.ToolName != "search" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestSubagentToolResultDataJSON(t *testing.T) {
	d := SubagentToolResultData{
		AgentID:  "agent-1",
		ToolID:   "t1",
		ToolName: "search",
		Result:   "found 5",
		IsError:  false,
	}
	b, _ := json.Marshal(d)
	var got SubagentToolResultData
	json.Unmarshal(b, &got)
	if got.Result != "found 5" || got.IsError {
		t.Errorf("mismatch: %+v", got)
	}
}

// --- Constants validation ---

func TestEventConstants(t *testing.T) {
	events := []string{
		EventConnected, EventSessionInfo, EventUserMessage, EventText,
		EventTextDone, EventStatus, EventToolCall, EventToolResult,
		EventApprovalRequest, EventApprovalResult, EventAskUserRequest,
		EventAskUserResponse, EventSubagentSpawn, EventSubagentText,
		EventSubagentStatus, EventSubagentToolCall, EventSubagentToolResult,
		EventSubagentComplete, EventError, EventPing, EventDisconnected,
	}
	for _, e := range events {
		if e == "" {
			t.Error("event constant should not be empty")
		}
	}
}

func TestCommandConstants(t *testing.T) {
	cmds := []string{
		CmdResumeHello, CmdResumeFrom, CmdMessage, CmdApprovalResponse, CmdInterrupt,
		CmdModeChange, CmdAskUserResponse, CmdPong,
	}
	for _, c := range cmds {
		if c == "" {
			t.Error("command constant should not be empty")
		}
	}
}

func TestStatusConstants(t *testing.T) {
	statuses := map[string]string{
		"idle":     StatusIdle,
		"thinking": StatusThinking,
		"running":  StatusRunning,
		"waiting":  StatusWaiting,
		"error":    StatusError,
	}
	for want, got := range statuses {
		if got != want {
			t.Errorf("Status %s = %q, want %q", want, got, want)
		}
	}
}

func TestModeConstants(t *testing.T) {
	modes := map[string]string{
		"supervised": ModeSupervised,
		"auto":       ModeAuto,
		"bypass":     ModeBypass,
		"autopilot":  ModeAutopilot,
	}
	for want, got := range modes {
		if got != want {
			t.Errorf("Mode %s = %q, want %q", want, got, want)
		}
	}
}

func TestDecisionConstants(t *testing.T) {
	decisions := map[string]string{
		"allow":        DecisionAllow,
		"deny":         DecisionDeny,
		"always_allow": DecisionAlwaysAllow,
	}
	for want, got := range decisions {
		if got != want {
			t.Errorf("Decision %s = %q, want %q", want, got, want)
		}
	}
}

// --- HistoryEntry (from broker.go) ---

func TestHistoryEntryJSON(t *testing.T) {
	h := HistoryEntry{
		Role:            "tool_call",
		Content:         "calling tool",
		ToolID:          "t1",
		ToolName:        "read_file",
		ToolDisplayName: "Read Config",
		ToolArgs:        `{"path":"/tmp"}`,
		ToolDetail:      "/tmp",
		Result:          "",
		IsError:         false,
	}
	b, _ := json.Marshal(h)
	var got HistoryEntry
	json.Unmarshal(b, &got)
	if got.Role != "tool_call" || got.ToolID != "t1" {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestHistoryEntryOmitEmpty(t *testing.T) {
	h := HistoryEntry{Role: "user", Content: "hi"}
	b, _ := json.Marshal(h)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	for _, key := range []string{"tool_id", "tool_name", "tool_args", "tool_detail", "result", "is_error"} {
		if _, ok := m[key]; ok {
			t.Errorf("%q should be omitted when zero/empty", key)
		}
	}
}
