package hooks

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- Dispatch tests ---

func TestDispatch_PreToolUse_Block(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{
			{Match: "run_command(rm *)", Command: "exit 2"},
		},
	}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "run_command",
		RawInput: `{"command":"rm -rf /tmp"}`,
	}
	result := Dispatch(cfg, env)
	if result.Allowed {
		t.Error("expected pre_tool_use to block")
	}
}

func TestDispatch_PreToolUse_Allow(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{
			{Match: "write_file", Command: "true"},
		},
	}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "write_file",
		RawInput: `{}`,
	}
	result := Dispatch(cfg, env)
	if !result.Allowed {
		t.Error("expected pre_tool_use to allow")
	}
}

func TestDispatch_PostToolUse_InjectOutput(t *testing.T) {
	cfg := HookConfig{
		PostToolUse: []Hook{
			{Match: "write_file", Command: "echo formatted", InjectOutput: true},
		},
	}
	env := HookEnv{
		Event:    EventPostToolUse,
		ToolName: "write_file",
		RawInput: `{}`,
	}
	result := Dispatch(cfg, env)
	if !result.Allowed {
		t.Error("expected post_tool_use to allow")
	}
	if !strings.Contains(result.Output, "formatted") {
		t.Errorf("expected injected output, got %q", result.Output)
	}
}

func TestDispatch_OnAgentStop_Async(t *testing.T) {
	var called int32
	cfg := HookConfig{
		OnAgentStop: []Hook{
			{Match: "*", Command: "true"},
		},
	}
	env := HookEnv{
		Event:      EventOnAgentStop,
		StopReason: "completed",
	}
	result := Dispatch(cfg, env)
	if !result.Allowed {
		t.Error("on_agent_stop should always allow")
	}
	// Async — give it a moment
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&called) > 0 {
		// Best effort check — the hook ran async
	}
}

func TestDispatch_OnStreamStop_Async(t *testing.T) {
	cfg := HookConfig{
		OnStreamStop: []Hook{
			{Match: "*", Command: "true"},
		},
	}
	env := HookEnv{
		Event:      EventOnStreamStop,
		StopReason: "completed",
	}
	result := Dispatch(cfg, env)
	if !result.Allowed {
		t.Error("on_stream_stop should always allow")
	}
}

func TestDispatch_UnknownEvent_NoOp(t *testing.T) {
	cfg := HookConfig{
		PreToolUse: []Hook{{Match: "*", Command: "exit 2"}},
	}
	env := HookEnv{Event: "unknown_event"}
	result := Dispatch(cfg, env)
	if !result.Allowed {
		t.Error("unknown event should be noop (allow)")
	}
}

// --- HTTP hook tests ---

func TestHTTPHook_Success(t *testing.T) {
	var receivedPayload HookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	h := Hook{
		Match: "write_file",
		Type:  HookTypeHTTP,
		URL:   server.URL,
	}
	env := HookEnv{
		Event:     EventPreToolUse,
		ToolName:  "write_file",
		RawInput:  `{"file_path":"/tmp/test.go"}`,
		Workspace: "/tmp",
	}
	payload := BuildPayload(env)
	result := executeHTTPHook(h, env, payload)
	if !result.Allowed {
		t.Error("expected HTTP 200 to allow")
	}
	if receivedPayload.Event != EventPreToolUse {
		t.Errorf("expected event %s, got %s", EventPreToolUse, receivedPayload.Event)
	}
	if receivedPayload.Tool.Name != "write_file" {
		t.Errorf("expected tool name write_file, got %s", receivedPayload.Tool.Name)
	}
}

func TestHTTPHook_Block403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "Security policy violation")
	}))
	defer server.Close()

	h := Hook{
		Match: "write_file",
		Type:  HookTypeHTTP,
		URL:   server.URL,
	}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "write_file",
		RawInput: `{}`,
	}
	payload := BuildPayload(env)
	result := executeHTTPHook(h, env, payload)
	if result.Allowed {
		t.Error("expected HTTP 403 to block")
	}
	if !strings.Contains(result.Output, "Security policy violation") {
		t.Errorf("expected block reason in output, got %q", result.Output)
	}
}

func TestHTTPHook_NonBlockingError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	h := Hook{
		Match: "write_file",
		Type:  HookTypeHTTP,
		URL:   server.URL,
	}
	env := HookEnv{
		Event:    EventPreToolUse,
		ToolName: "write_file",
		RawInput: `{}`,
	}
	payload := BuildPayload(env)
	result := executeHTTPHook(h, env, payload)
	if !result.Allowed {
		t.Error("HTTP 500 should not block")
	}
}

func TestHTTPHook_HMACSignature(t *testing.T) {
	var receivedSig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-GGCode-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := Hook{
		Match:  "*",
		Type:   HookTypeHTTP,
		URL:    server.URL,
		Secret: "test-secret-key",
	}
	env := HookEnv{
		Event:    EventOnUserMessage,
		RawInput: `{}`,
	}
	payload := BuildPayload(env)
	executeHTTPHook(h, env, payload)
	if !strings.HasPrefix(receivedSig, "sha256=") {
		t.Errorf("expected HMAC signature, got %q", receivedSig)
	}
}

func TestHTTPHook_CustomHeaders(t *testing.T) {
	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := Hook{
		Match: "*",
		Type:  HookTypeHTTP,
		URL:   server.URL,
		Headers: map[string]string{
			"Authorization": "Bearer my-token",
		},
	}
	env := HookEnv{Event: EventOnUserMessage}
	payload := BuildPayload(env)
	executeHTTPHook(h, env, payload)
	if receivedAuth != "Bearer my-token" {
		t.Errorf("expected custom header, got %q", receivedAuth)
	}
}

func TestHTTPHook_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := Hook{
		Match:   "*",
		Type:    HookTypeHTTP,
		URL:     server.URL,
		Timeout: "100ms",
	}
	env := HookEnv{Event: EventPreToolUse}
	payload := BuildPayload(env)
	start := time.Now()
	result := executeHTTPHook(h, env, payload)
	elapsed := time.Since(start)
	if !result.Allowed {
		t.Error("timeout should not block")
	}
	if result.Err == nil {
		t.Error("expected error on timeout")
	}
	if elapsed > 1*time.Second {
		t.Errorf("expected timeout near 100ms, took %v", elapsed)
	}
}

func TestHTTPHook_EventHeader(t *testing.T) {
	var receivedEvent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEvent = r.Header.Get("X-GGCode-Event")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	h := Hook{Match: "*", Type: HookTypeHTTP, URL: server.URL}
	env := HookEnv{Event: EventPostToolUse, ToolName: "write_file"}
	payload := BuildPayload(env)
	executeHTTPHook(h, env, payload)
	if receivedEvent != EventPostToolUse {
		t.Errorf("expected X-GGCode-Event header %s, got %s", EventPostToolUse, receivedEvent)
	}
}

// --- Payload tests ---

func TestBuildPayload_PreToolUse(t *testing.T) {
	env := HookEnv{
		Event:      EventPreToolUse,
		ToolName:   "write_file",
		RawInput:   `{"file_path":"/tmp/test.go","content":"hello"}`,
		FilePath:   "/tmp/test.go",
		Workspace:  "/home/user/project",
		WorkingDir: "/home/user/project",
	}
	payload := BuildPayload(env)
	if payload.Event != EventPreToolUse {
		t.Errorf("expected event %s", EventPreToolUse)
	}
	if payload.Tool.Name != "write_file" {
		t.Errorf("expected tool name write_file")
	}
	if payload.Tool.FilePath != "/tmp/test.go" {
		t.Errorf("expected file path /tmp/test.go, got %s", payload.Tool.FilePath)
	}
	if payload.Workspace != "/home/user/project" {
		t.Errorf("expected workspace, got %s", payload.Workspace)
	}
}

func TestBuildPayload_PostToolUse(t *testing.T) {
	env := HookEnv{
		Event:        EventPostToolUse,
		ToolName:     "run_command",
		RawInput:     `{"command":"ls"}`,
		ToolSuccess:  false,
		ToolError:    "command failed",
		ToolResult:   "exit code 1",
		ToolDuration: "5ms",
	}
	payload := BuildPayload(env)
	if payload.Result.Success {
		t.Error("expected success=false")
	}
	if payload.Result.Error != "command failed" {
		t.Errorf("expected error, got %s", payload.Result.Error)
	}
	if payload.Result.DurationMs != 5 {
		t.Errorf("expected 5ms, got %d", payload.Result.DurationMs)
	}
}

func TestBuildPayload_OnAgentStop(t *testing.T) {
	env := HookEnv{
		Event:      EventOnAgentStop,
		StopReason: "cancelled",
		StopError:  "",
	}
	payload := BuildPayload(env)
	if payload.Stop == nil {
		t.Fatal("expected stop to be non-nil")
	}
	if payload.Stop.Reason != "cancelled" {
		t.Errorf("expected cancelled, got %s", payload.Stop.Reason)
	}
}

func TestBuildPayload_OnUserMessage(t *testing.T) {
	env := HookEnv{
		Event:       EventOnUserMessage,
		UserMessage: "fix the bug in auth.go",
	}
	payload := BuildPayload(env)
	if payload.Msg == nil {
		t.Fatal("expected message to be non-nil")
	}
	if payload.Msg.Content != "fix the bug in auth.go" {
		t.Errorf("expected user message content, got %s", payload.Msg.Content)
	}
}

// --- matchAny tests ---

func TestMatchAny_Wildcard(t *testing.T) {
	if !matchAny("*", "", "") {
		t.Error("* should match everything")
	}
	if !matchAny("", "", "") {
		t.Error("empty pattern should match everything")
	}
}

func TestMatchAny_ToolSpecific(t *testing.T) {
	if !matchAny("write_file", "write_file", "") {
		t.Error("should match exact tool name")
	}
	if matchAny("write_file", "read_file", "") {
		t.Error("should not match different tool name")
	}
}

// --- Hook.HasType tests ---

func TestHook_HasType_Command(t *testing.T) {
	h := Hook{Command: "echo hello"}
	if h.HasType() != HookTypeCommand {
		t.Error("hook with only command should be command type")
	}
}

func TestHook_HasType_HTTP(t *testing.T) {
	h := Hook{Type: HookTypeHTTP, URL: "https://example.com"}
	if h.HasType() != HookTypeHTTP {
		t.Error("hook with explicit http type should be http")
	}
}

func TestHook_HasType_HTTPRequiresExplicitType(t *testing.T) {
	h := Hook{URL: "https://example.com"}
	// URL alone does NOT infer http type — user must set type: http explicitly
	if h.HasType() != HookTypeCommand {
		t.Error("hook with URL but no type should default to command")
	}
}
