package a2a

// Feature tests for issue #565 (ver-53 probe findings A, C, D, E, F, G).
// Finding B was rejected in triage and is intentionally not tested.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// ---------------------------------------------------------------------------
// Shared stubs
// ---------------------------------------------------------------------------

// scriptProvider serves a scripted ChatStream per call (agent path).
type scriptProvider struct {
	mu     sync.Mutex
	script [][]provider.StreamEvent
	calls  int
}

func (p *scriptProvider) Name() string { return "script" }

func (p *scriptProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{}, nil
}

func (p *scriptProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	idx := p.calls
	if idx >= len(p.script) {
		idx = len(p.script) - 1
	}
	p.calls++
	events := append([]provider.StreamEvent(nil), p.script[idx]...)
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, len(events)+1)
	for _, e := range events {
		ch <- e
	}
	ch <- provider.StreamEvent{Type: provider.StreamEventDone}
	close(ch)
	return ch, nil
}

func (p *scriptProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 1, nil
}

// stubTool records executions.
type stubTool struct {
	name string
	ran  int
}

func (t *stubTool) Name() string        { return t.name }
func (t *stubTool) Description() string { return "stub for tests" }
func (t *stubTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *stubTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	t.ran++
	return tool.Result{Content: "ok"}, nil
}

func newStubRegistry(tools ...tool.Tool) *tool.Registry {
	reg := tool.NewRegistry()
	for _, t := range tools {
		if err := reg.Register(t); err != nil {
			panic(err)
		}
	}
	return reg
}

var handlerWorkspaceOnce sync.Once
var handlerWorkspace string

func stubWorkspace() string {
	handlerWorkspaceOnce.Do(func() {
		handlerWorkspace = tTempDir()
	})
	return handlerWorkspace
}

func newHandler(a *agent.Agent, reg *tool.Registry) *TaskHandler {
	return NewTaskHandler(stubWorkspace(), a, reg,
		WithMaxTasks(5), WithTimeout(10*time.Second))
}

// tTempDir returns a fresh temp dir for shared handler construction.
func tTempDir() string {
	d, err := os.MkdirTemp("", "issue565-*")
	if err != nil {
		panic(err)
	}
	return d
}

// ---------------------------------------------------------------------------
// A: skill allowlist enforced on the agent path (security)
// ---------------------------------------------------------------------------

// TestIssue565A_ReadOnlySkillWhitelistsAreActuallyReadOnly verifies the
// built-in read-only skills do not whitelist any side-effecting tool.
func TestIssue565A_ReadOnlySkillWhitelistsAreActuallyReadOnly(t *testing.T) {
	for skill, allowed := range skillPermissions {
		if skill == SkillFullTask || skill == SkillCodeEdit || skill == SkillCommandExec {
			continue // deliberately side-effecting skills
		}
		for _, name := range allowed.AllowedTools {
			for _, dangerous := range []string{"write_file", "edit_file", "multi_edit_file", "file_ops", "git_commit", "git_reset", "run_command", "batch_replace"} {
				if name == dangerous {
					t.Errorf("skill %q whitelist includes side-effecting tool %q", skill, name)
				}
			}
		}
	}
}

// TestIssue565A_RestrictRegistry_Unit verifies restrictRegistry exposes only
// allowlisted tools and tolerates unknown names / nil source.
func TestIssue565A_RestrictRegistry_Unit(t *testing.T) {
	read := &stubTool{name: "read_file"}
	write := &stubTool{name: "write_file"}
	src := newStubRegistry(read, write)

	got := restrictRegistry(src, []string{"read_file", "nonexistent"})
	if _, ok := got.Get("read_file"); !ok {
		t.Error("allowlisted tool missing from restricted registry")
	}
	if _, ok := got.Get("write_file"); ok {
		t.Error("non-allowlisted tool leaked into restricted registry")
	}
	if n := len(restrictRegistry(nil, []string{"read_file"}).List()); n != 0 {
		t.Errorf("nil source should yield empty registry, got %d tools", n)
	}
}

// TestIssue565A_AllowlistExcludesWriteToolsOnAgentPath runs the REAL
// executeAgent path with a file-search skill whose whitelist excludes
// write_file, and proves the write tool is unreachable (regression test for
// the privilege escalation found by the ver-53 probe).
func TestIssue565A_AllowlistExcludesWriteToolsOnAgentPath(t *testing.T) {
	read := &stubTool{name: "read_file"}
	write := &stubTool{name: "write_file"}

	inputArgs, _ := json.Marshal(map[string]string{"path": "/tmp/x", "content": "pwned"})
	// Turn 1: model requests write_file; turn 2: model finishes with text.
	script := [][]provider.StreamEvent{{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{
			ID: "tc1", Name: "write_file", Arguments: inputArgs,
		}},
	}, {
		{Type: provider.StreamEventText, Text: "done"},
	}}

	reg := newStubRegistry(read, write)
	a := agent.NewAgent(&scriptProvider{script: script}, reg, "test", 10)
	h := newHandler(a, reg)

	perm := skillPermissions[SkillFileSearch]
	if perm == nil {
		t.Fatalf("no permissions declared for %s", SkillFileSearch)
	}

	// #565 A: executeAgent must honor perm; previously it built the agent
	// from the FULL registry, letting file-search invoke write_file.
	_, err := h.executeAgent(context.Background(), perm, SkillFileSearch,
		Message{Parts: []Part{{Kind: "text", Text: "do it"}}})
	if err != nil {
		t.Fatalf("executeAgent: %v", err)
	}
	if write.ran > 0 {
		t.Fatalf("BUG A PRIVILEGE ESCALATION: write_file executed %d time(s) via %s skill allowlist bypass", write.ran, SkillFileSearch)
	}
}

// TestIssue565A_PositiveControl_UnrestrictedSkillCanExecuteTool proves the
// harness actually drives tool execution when nothing is restricted —
// guarding against a vacuously-passing A test.
func TestIssue565A_PositiveControl_UnrestrictedSkillCanExecuteTool(t *testing.T) {
	write := &stubTool{name: "write_file"}
	inputArgs, _ := json.Marshal(map[string]string{"path": "/tmp/x", "content": "data"})
	script := [][]provider.StreamEvent{{
		{Type: provider.StreamEventToolCallDone, Tool: provider.ToolCallDelta{
			ID: "tc1", Name: "write_file", Arguments: inputArgs,
		}},
	}, {
		{Type: provider.StreamEventText, Text: "done"},
	}}

	reg := newStubRegistry(write)
	a := agent.NewAgent(&scriptProvider{script: script}, reg, "test", 10)
	h := newHandler(a, reg)

	perm := &SkillPermission{AllowedTools: nil} // unrestricted
	_, err := h.executeAgent(context.Background(), perm, SkillFullTask,
		Message{Parts: []Part{{Kind: "text", Text: "write"}}})
	if err != nil {
		t.Fatalf("executeAgent: %v", err)
	}
	if write.ran == 0 {
		t.Fatal("positive control failed: unrestricted skill did not execute write_file — harness is not driving tool calls")
	}
}

// ---------------------------------------------------------------------------
// F: SSE scanner robustness
// ---------------------------------------------------------------------------

// TestIssue565F_ScannerBufferSizePresent guards the buffer setup so a future
// refactor cannot silently drop it.
func TestIssue565F_ScannerBufferSizePresent(t *testing.T) {
	b, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("read client.go: %v", err)
	}
	if !strings.Contains(string(b), "scanner.Buffer(") {
		t.Fatal("BUG F REGRESSION: decodeSSE lost its scanner.Buffer call — large events will be silently truncated again")
	}
}

// TestIssue565F_LargeSSEEventsNotTruncated feeds a >64KB single-line SSE
// data event followed by a terminal completed event; both must decode.
func TestIssue565F_LargeSSEEventsNotTruncated(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"kind\":\"status-update\",\"status\":{\"state\":\"working\"}}}\n\n")

	huge := strings.Repeat("x", 70*1024)
	artifact, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1,
		"result": map[string]interface{}{
			"kind": "artifact-update",
			"artifact": map[string]interface{}{
				"artifactId": "big",
				"parts":      []map[string]interface{}{{"kind": "text", "text": huge}},
			},
			"final": false,
		},
	})
	fmt.Fprintf(&sb, "data: %s\n\n", artifact)

	final, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1,
		"result": map[string]interface{}{
			"kind":   "status-update",
			"status": map[string]interface{}{"state": "completed"},
			"final":  true,
		},
	})
	fmt.Fprintf(&sb, "data: %s\n\n", final)

	ch := make(chan JSONRPCResponse, 8)
	decodeSSE(strings.NewReader(sb.String()), ch)
	close(ch)

	var got []JSONRPCResponse
	for r := range ch {
		if r.Error != nil {
			t.Fatalf("unexpected error event: %v", r.Error)
		}
		got = append(got, r)
	}
	if len(got) != 3 {
		t.Fatalf("BUG F: expected 3 SSE events, decoded %d — scanner truncated the stream", len(got))
	}
	resultJSON, err := json.Marshal(got[len(got)-1].Result)
	if err != nil {
		t.Fatalf("re-marshal final result: %v", err)
	}
	var payload struct {
		Status struct {
			State string `json:"state"`
		} `json:"status"`
		Final bool `json:"final"`
	}
	if err := json.Unmarshal(resultJSON, &payload); err != nil {
		t.Fatalf("decode final event: %v", err)
	}
	if payload.Status.State != "completed" || !payload.Final {
		t.Fatalf("terminal event lost or wrong: state=%q final=%v", payload.Status.State, payload.Final)
	}
}

// errReader delivers its payload, then a permanent read error.
type errReader struct {
	payload string
	done    bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.payload), nil
	}
	return 0, fmt.Errorf("connection reset by peer")
}

// TestIssue565F_ScannerErrorSurfaced proves a mid-stream transport failure
// reaches the consumer as a JSON-RPC error instead of a silent EOF.
func TestIssue565F_ScannerErrorSurfaced(t *testing.T) {
	r := &errReader{payload: "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"kind\":\"status-update\",\"status\":{\"state\":\"working\"}}}\n\n"}
	ch := make(chan JSONRPCResponse, 8)
	decodeSSE(r, ch)
	close(ch)

	var sawErr bool
	for resp := range ch {
		if resp.Error != nil && resp.Error.Code == -32603 {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("BUG F: scanner error was swallowed — consumer saw clean EOF on a failed stream")
	}
}

// ---------------------------------------------------------------------------
// C: card mutex
// ---------------------------------------------------------------------------

// TestIssue565C_CardConcurrentReadsAndWrites hammers card reads (HTTP card
// endpoint + accessors) against setter writes; guarded by cardMu this is
// race-detector clean.
func TestIssue565C_CardConcurrentReadsAndWrites(t *testing.T) {
	reg := newStubRegistry()
	a := agent.NewAgent(&scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "hi"}},
	}}, reg, "test", 5)
	h := newHandler(a, reg)
	s := NewServer(ServerConfig{}, h)
	ts := httptest.NewServer(s.Mux())
	defer ts.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	writersDone := make(chan struct{})

	wg.Add(1)
	go func() { // writers
		defer wg.Done()
		defer close(writersDone)
		for i := 0; i < 200; i++ {
			s.SetExtendedCard(json.RawMessage(`{"extra":true}`))
		}
	}()
	for r := 0; r < 3; r++ { // readers via HTTP + accessors
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := ts.Client().Get(ts.URL + "/.well-known/agent.json")
				if err == nil {
					io.Copy(io.Discard, resp.Body) //nolint:errcheck
					resp.Body.Close()              //nolint:errcheck
				}
				_ = s.AgentCard()
				_ = s.Endpoint()
			}
		}()
	}
	<-writersDone // stop readers once writers finish
	close(stop)
	wg.Wait()
}

// ---------------------------------------------------------------------------
// E: id null in error responses
// ---------------------------------------------------------------------------

// TestIssue565E_ParseErrorResponseHasNullID posts garbage; the parse-error
// response MUST carry "id":null per JSON-RPC 2.0 (not omit the member).
func TestIssue565E_ParseErrorResponseHasNullID(t *testing.T) {
	reg := newStubRegistry()
	a := agent.NewAgent(&scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "hi"}},
	}}, reg, "test", 5)
	h := newHandler(a, reg)
	s := NewServer(ServerConfig{}, h)
	ts := httptest.NewServer(s.Mux())
	defer ts.Close()

	resp, err := ts.Client().Post(ts.URL+"/", "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var buf strings.Builder
	io.Copy(&buf, resp.Body) //nolint:errcheck

	if !strings.Contains(buf.String(), `"id":null`) {
		t.Fatalf("BUG E: parse-error response missing explicit null id: %s", buf.String())
	}
	var decoded struct {
		ID    *json.RawMessage `json:"id"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(buf.String()), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != -32700 {
		t.Fatalf("expected parse error -32700, got: %s", buf.String())
	}
	// Note: json.Unmarshal of JSON null into *json.RawMessage sets the
	// pointer to nil, so decoded.ID == nil is EXPECTED here — the raw-body
	// Contains check above is the actual proof that "id":null was emitted
	// rather than omitted.
}

// TestIssue565E_NormalizeResponseID unit-checks the mapping.
func TestIssue565E_NormalizeResponseID(t *testing.T) {
	if got := string(normalizeResponseID(nil)); got != "null" {
		t.Errorf("nil id → %q, want null", got)
	}
	if got := string(normalizeResponseID(json.RawMessage(`5`))); got != "5" {
		t.Errorf("present id mutated: %q", got)
	}
}

// ---------------------------------------------------------------------------
// D: artifact events in stream
// ---------------------------------------------------------------------------

// TestIssue565D_ArtifactEventsEmittedInStream drives a full task through
// message/stream and requires an artifact event before the terminal status.
func TestIssue565D_ArtifactEventsEmittedInStream(t *testing.T) {
	reg := newStubRegistry()
	a := agent.NewAgent(&scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "the result text"}},
	}}, reg, "test", 5)
	h := newHandler(a, reg)
	s := NewServer(ServerConfig{}, h)
	ts := httptest.NewServer(s.Mux())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	c := NewClient(ts.URL, "")
	ch, err := c.SendMessageStream(ctx, SkillFullTask, "hello")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var sawArtifact, sawFinal bool
	deadline := time.After(12 * time.Second)
collect:
	for {
		select {
		case resp, ok := <-ch:
			if !ok {
				break collect
			}
			if resp.Error != nil {
				t.Fatalf("stream error: %v", resp.Error)
			}
			resultJSON, err := json.Marshal(resp.Result)
			if err != nil {
				continue
			}
			var payload struct {
				Status struct {
					State string `json:"state"`
				} `json:"status"`
				Final    bool `json:"final"`
				Artifact *struct {
					ArtifactID string `json:"artifactId"`
				} `json:"artifact"`
			}
			if err := json.Unmarshal(resultJSON, &payload); err != nil {
				continue
			}
			if payload.Artifact != nil && payload.Artifact.ArtifactID != "" {
				sawArtifact = true
			}
			if payload.Final {
				sawFinal = true
				if payload.Status.State != string(TaskStateCompleted) {
					t.Errorf("final state = %q, want completed", payload.Status.State)
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for stream events")
		}
	}

	if !sawFinal {
		t.Fatal("terminal status event never arrived")
	}
	if !sawArtifact {
		t.Fatal("BUG D: no artifact event in stream — card declares streaming but artifacts are missing")
	}
}

// ---------------------------------------------------------------------------
// G: messageId idempotency
// ---------------------------------------------------------------------------

// TestIssue565G_MessageIdIdempotency re-sends the same messageId and must
// get the SAME task back, not a duplicate (timeout-retry double execution).
func TestIssue565G_MessageIdIdempotency(t *testing.T) {
	reg := newStubRegistry()
	a := agent.NewAgent(&scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "edit done"}},
	}}, reg, "test", 5)
	h := newHandler(a, reg)

	msg := Message{MessageID: "retry-abc", Parts: []Part{{Kind: "text", Text: "edit file"}}}
	t1, err := h.Handle(context.Background(), SkillCodeEdit, msg, "")
	if err != nil {
		t.Fatalf("first handle: %v", err)
	}
	// The idempotency check happens at Handle entry regardless of the
	// first task's state, so no need to wait for completion here.
	t2, err := h.Handle(context.Background(), SkillCodeEdit, msg, "")
	if err != nil {
		t.Fatalf("second handle: %v", err)
	}
	if t1.ID != t2.ID {
		t.Fatalf("BUG G: duplicate task spawned on retry — t1=%s t2=%s (double execution of %s)", t1.ID, t2.ID, SkillCodeEdit)
	}

	// Different messageId → different task.
	msg2 := msg
	msg2.MessageID = "different"
	t3, err := h.Handle(context.Background(), SkillCodeEdit, msg2, "")
	if err != nil {
		t.Fatalf("third handle: %v", err)
	}
	if t3.ID == t1.ID {
		t.Fatalf("different messageId returned same task %s — index over-matches", t1.ID)
	}
}

// TestIssue565G_MessageIdIndexReapedWithTasks verifies the idempotency
// index does not outlive its tasks (memory growth guard).
func TestIssue565G_MessageIdIndexReapedWithTasks(t *testing.T) {
	reg := newStubRegistry()
	a := agent.NewAgent(&scriptProvider{script: [][]provider.StreamEvent{
		{{Type: provider.StreamEventText, Text: "x"}},
	}}, reg, "test", 5)
	h := newHandler(a, reg)

	msg := Message{MessageID: "gone-soon", Parts: []Part{{Kind: "text", Text: "x"}}}
	if _, err := h.Handle(context.Background(), SkillFullTask, msg, ""); err != nil {
		t.Fatalf("handle: %v", err)
	}

	h.mu.Lock()
	tid, ok := h.messageIndex["gone-soon"]
	if !ok {
		h.mu.Unlock()
		t.Fatal("index entry missing right after creation — setup assumption wrong")
	}
	// Simulate the task expiring (as cleanupExpiredTasksLocked would do),
	// then run the reaper and confirm the index entry goes with it.
	delete(h.tasks, tid)
	h.cleanupExpiredTasksLocked()
	if _, ok := h.messageIndex["gone-soon"]; ok {
		h.mu.Unlock()
		t.Fatal("BUG G: messageIndex entry survived task expiry — unbounded growth")
	}
	h.mu.Unlock()
}
