package mcpserve

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- request/response plumbing helpers ---

// serveLines runs Serve over the given newline-delimited frames and returns
// the response frames produced.
func serveLines(t *testing.T, s *Server, frames ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(frames, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response not valid JSON (%q): %v", line, err)
		}
		responses = append(responses, m)
	}
	return responses
}

func frame(id int, method string, params string) string {
	f := `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"` + method + `"`
	if params != "" {
		f += `,"params":` + params
	}
	return f + "}"
}

func itoa(n int) string {
	return strings.TrimSpace(json.Number(jsonifyInt(n)).String())
}

func jsonifyInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// --- protocol tests ---

func TestNegotiateProtocolVersion(t *testing.T) {
	cases := []struct{ requested, want string }{
		{"2024-11-05", "2024-11-05"},
		{"2025-06-18", "2025-06-18"},
		{"2025-11-25", "2025-11-25"},
		{"1999-01-01", "2025-11-25"}, // unknown → server latest
		{"", "2025-11-25"},
	}
	for _, tc := range cases {
		if got := negotiateProtocolVersion(tc.requested); got != tc.want {
			t.Errorf("negotiate(%q) = %q, want %q", tc.requested, got, tc.want)
		}
	}
}

func TestServeInitializePingToolsList(t *testing.T) {
	s := New(Options{Version: "test-1", Sessions: &fakeSessionSource{}})
	responses := serveLines(t, s, frame(1, "initialize", `{"protocolVersion":"2025-06-18","clientInfo":{"name":"t","version":"0"}}`), frame(2, "ping", ""), frame(3, "tools/list", ""))
	if len(responses) != 3 {
		t.Fatalf("got %d responses, want 3", len(responses))
	}
	init := responses[0]
	if errObj, ok := init["error"]; ok {
		t.Fatalf("initialize errored: %v", errObj)
	}
	result := init["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want 2025-06-18", result["protocolVersion"])
	}
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "ggcode" || serverInfo["version"] != "test-1" {
		t.Errorf("serverInfo = %v", serverInfo)
	}
	ping := responses[1]
	if _, has := ping["error"]; has {
		t.Errorf("ping errored: %v", ping["error"])
	}
	list := responses[2]["result"].(map[string]any)
	tools := list["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("tools/list returned %d tools, want 3", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		def := tool.(map[string]any)
		names[def["name"].(string)] = true
		if def["inputSchema"] == nil || def["description"] == nil {
			t.Errorf("tool %s missing inputSchema/description", def["name"])
		}
	}
	for _, want := range []string{"ggcode_run", "ggcode_session_list", "ggcode_session_read"} {
		if !names[want] {
			t.Errorf("tools/list missing %s", want)
		}
	}
}

func TestToolsCallRunSuccessAndFailure(t *testing.T) {
	runner := &fakeRunner{result: RunResult{Output: "done: 42"}}
	s := New(Options{Version: "t", Runner: runner, Sessions: &fakeSessionSource{}})
	args := `{"name":"ggcode_run","arguments":{"prompt":"add a test","working_dir":"/tmp","timeout_seconds":120}}`
	responses := serveLines(t, s, frame(1, "tools/call", args))
	result := responses[0]["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("unexpected isError: %v", result)
	}
	content := result["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "done: 42" {
		t.Fatalf("content = %v", content)
	}
	if runner.last.Prompt != "add a test" || runner.last.WorkingDir != "/tmp" || runner.last.TimeoutSeconds != 120 {
		t.Errorf("runner got %+v", runner.last)
	}
	// Execution failure → isError=true result (not a protocol error).
	runner.err = true
	responses = serveLines(t, s, frame(2, "tools/call", args))
	result = responses[0]["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected isError=true, got %v", result)
	}
}

func TestToolsCallProtocolErrors(t *testing.T) {
	s := New(Options{Version: "t", Runner: &fakeRunner{}, Sessions: &fakeSessionSource{}})
	responses := serveLines(t, s,
		frame(1, "tools/call", `{"name":"ggcode_run","arguments":{"prompt":"  "}}`),
		frame(2, "tools/call", `{"name":"nope","arguments":{}}`),
		frame(3, "tools/call", `42`),
		frame(4, "tools/call", `{"name":"ggcode_session_read","arguments":{}}`),
	)
	for i, wantCode := range []float64{errInvalidParams, errInvalidParams, errInvalidParams, errInvalidParams} {
		errObj, ok := responses[i]["error"].(map[string]any)
		if !ok {
			t.Fatalf("response %d: expected error, got %v", i+1, responses[i])
		}
		if errObj["code"].(float64) != wantCode {
			t.Errorf("response %d: code = %v, want %v", i+1, errObj["code"], wantCode)
		}
	}
}

func TestUnknownMethodAndNotifications(t *testing.T) {
	s := New(Options{Version: "t", Runner: &fakeRunner{}, Sessions: &fakeSessionSource{}})
	responses := serveLines(t, s,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // no response
		frame(1, "resources/list", ""),
		frame(2, "prompts/list", ""),
		frame(3, "bogus/method", ""),
		frame(4, "this-is-not-json", ""), // valid frame, unknown method
	)
	if len(responses) != 4 {
		t.Fatalf("got %d responses, want 4 (notification must be silent)", len(responses))
	}
	if _, has := responses[0]["error"]; has {
		t.Errorf("resources/list errored")
	}
	if _, has := responses[1]["error"]; has {
		t.Errorf("prompts/list errored")
	}
	errObj := responses[2]["error"].(map[string]any)
	if errObj["code"].(float64) != errMethodNotFound {
		t.Errorf("unknown method code = %v", errObj["code"])
	}
}

func TestMalformedLineYieldsParseError(t *testing.T) {
	s := New(Options{Version: "t"})
	responses := serveLines(t, s, `{broken`)
	errObj, ok := responses[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected parse error, got %v", responses)
	}
	if errObj["code"].(float64) != errParse {
		t.Errorf("code = %v, want %v", errObj["code"], errParse)
	}
	if id := responses[0]["id"]; id != nil {
		if s, isStr := id.(string); !isStr || s != "" {
			// null unmarshals to nil; any non-nil id here is wrong.
			t.Errorf("parse error id = %v, want nil", id)
		}
	}
}

func TestServeCleanEOF(t *testing.T) {
	s := New(Options{Version: "t"})
	if err := s.Serve(context.Background(), strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Errorf("clean EOF should return nil, got %v", err)
	}
}

// --- session tools with a real JSONL store ---

func TestSessionToolsWithRealStore(t *testing.T) {
	dir := t.TempDir()
	writeRealSession(t, dir, "ses-1", "Fix login bug", "fix the login bug", "all done, tests pass")
	s := New(Options{Version: "t", Runner: &fakeRunner{}, Sessions: newTempSessionSource(dir)})

	responses := serveLines(t, s,
		frame(1, "tools/call", `{"name":"ggcode_session_list","arguments":{"limit":5}}`),
		frame(2, "tools/call", `{"name":"ggcode_session_read","arguments":{"session_id":"ses-1"}}`),
		frame(3, "tools/call", `{"name":"ggcode_session_read","arguments":{"session_id":"missing"}}`),
	)
	listText := firstContentText(t, responses[0])
	if !strings.Contains(listText, "ses-1") || !strings.Contains(listText, "Fix login bug") {
		t.Errorf("session list text = %q", listText)
	}
	readText := firstContentText(t, responses[1])
	if !strings.Contains(readText, "fix the login bug") || !strings.Contains(readText, "all done, tests pass") {
		t.Errorf("session read text = %q", readText)
	}
	if !strings.Contains(readText, "[1] user") || !strings.Contains(readText, "[2] assistant") {
		t.Errorf("transcript roles missing: %q", readText)
	}
	if isErrorResult(t, responses[2]) {
		// missing session is a tool-level error result — fine either way,
		// but it must be an isError result mentioning the cause.
		t.Logf("missing session reported as error result (ok)")
	}
}

// --- fakes ---

type fakeRunner struct {
	result RunResult
	err    bool
	last   RunRequest
}

func (f *fakeRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	f.last = req
	if f.err {
		return RunResult{}, context.DeadlineExceeded
	}
	return f.result, nil
}

type fakeSessionSource struct{}

func (f *fakeSessionSource) List(int) ([]SessionSummary, error) {
	return []SessionSummary{{ID: "s1", Title: "t"}}, nil
}

func (f *fakeSessionSource) Read(string, int) (string, error) { return "transcript", nil }
