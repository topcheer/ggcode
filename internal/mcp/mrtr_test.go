package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMRTRCompletePassthrough(t *testing.T) {
	params := &CallToolParams{Name: "t"}
	send := func() (json.RawMessage, error) {
		return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
	}
	var out CallToolResult
	if err := NewClient("test", "echo", nil).mrtrLoop(context.Background(), "tools/call", params, send, &out); err != nil {
		t.Fatalf("mrtrLoop: %v", err)
	}
	if len(out.Content) != 1 || out.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", out)
	}
	if params.InputResponses != nil || params.RequestState != "" {
		t.Fatalf("retry fields must stay empty on a complete result: %+v", params)
	}
}

func TestMRTRRoundTripRetry(t *testing.T) {
	c := NewClient("test", "echo", nil)
	c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
		return &SamplingResult{Model: "m", Role: "assistant", StopReason: "end_turn", Content: SamplingContent{Type: "text", Text: "sampled"}}, nil
	})
	params := &CallToolParams{Name: "t"}
	var sent []string
	send := func() (json.RawMessage, error) {
		b, _ := json.Marshal(params)
		sent = append(sent, string(b))
		if len(sent) == 1 {
			return json.RawMessage(`{"resultType":"input_required","inputRequests":{"ask1":{"method":"sampling/createMessage","params":{"messages":[]}}},"requestState":"state-1"}`), nil
		}
		return json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"done"}]}`), nil
	}
	var out CallToolResult
	if err := c.mrtrLoop(context.Background(), "tools/call", params, send, &out); err != nil {
		t.Fatalf("mrtrLoop: %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("expected 2 sends, got %d", len(sent))
	}
	if !strings.Contains(sent[1], `"inputResponses"`) || !strings.Contains(sent[1], `"requestState":"state-1"`) {
		t.Fatalf("retry params missing inputResponses/requestState: %s", sent[1])
	}
	if !strings.Contains(sent[1], `"sampled"`) {
		t.Fatalf("retry must carry the resolved sampling response: %s", sent[1])
	}
	if out.Content[0].Text != "done" {
		t.Fatalf("unexpected final result: %+v", out)
	}
}

func TestMRTRRequestStateOnlyImmediateRetry(t *testing.T) {
	params := &CallToolParams{Name: "t"}
	var sent []string
	send := func() (json.RawMessage, error) {
		b, _ := json.Marshal(params)
		sent = append(sent, string(b))
		if len(sent) == 1 {
			return json.RawMessage(`{"resultType":"input_required","requestState":"wait-1"}`), nil
		}
		return json.RawMessage(`{"resultType":"complete","content":[{"type":"text","text":"ok"}]}`), nil
	}
	var out CallToolResult
	if err := NewClient("test", "echo", nil).mrtrLoop(context.Background(), "tools/call", params, send, &out); err != nil {
		t.Fatalf("mrtrLoop: %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("expected immediate retry, got %d sends", len(sent))
	}
	if !strings.Contains(sent[1], `"requestState":"wait-1"`) {
		t.Fatalf("retry must echo requestState: %s", sent[1])
	}
	if strings.Contains(sent[1], `"inputResponses"`) {
		t.Fatalf("retry must omit inputResponses when there were no input requests: %s", sent[1])
	}
}

func TestMRTRLoopGuard(t *testing.T) {
	// Serve the same input_required forever; the guard must stop the loop.
	infinite := func() (json.RawMessage, error) {
		return json.RawMessage(`{"resultType":"input_required","requestState":"s"}`), nil
	}
	params := &CallToolParams{Name: "t"}
	err := NewClient("test", "echo", nil).mrtrLoop(context.Background(), "tools/call", params, infinite, &CallToolResult{})
	if err == nil || !strings.Contains(err.Error(), "loop guard") {
		t.Fatalf("expected loop guard error, got: %v", err)
	}
}

func TestMRTRProtocolViolations(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"no inputRequests nor requestState", `{"resultType":"input_required"}`, "neither inputRequests nor requestState"},
		{"unknown resultType", `{"resultType":"mind_control"}`, "unrecognized resultType"},
		{"malformed result", `{not json}`, "malformed result"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			send := func() (json.RawMessage, error) { return json.RawMessage(tc.raw), nil }
			err := NewClient("test", "echo", nil).mrtrLoop(context.Background(), "tools/call", &CallToolParams{Name: "t"}, send, &CallToolResult{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got: %v", tc.want, err)
			}
		})
	}
}

func TestMRTRResolveSamplingInputRequest(t *testing.T) {
	c := NewClient("test", "echo", nil)
	c.SetSamplingHandler(func(ctx context.Context, p SamplingParams) (*SamplingResult, error) {
		return &SamplingResult{
			Model:      "m",
			Role:       "assistant",
			StopReason: "end_turn",
			Content:    SamplingContent{Type: "text", Text: "sampled"},
		}, nil
	})
	in := map[string]json.RawMessage{
		"r1": json.RawMessage(`{"method":"sampling/createMessage","params":{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}]}}`),
	}
	resp, err := c.resolveInputRequests(context.Background(), in)
	if err != nil {
		t.Fatalf("resolveInputRequests: %v", err)
	}
	var sr SamplingResult
	if err := json.Unmarshal(resp["r1"], &sr); err != nil {
		t.Fatalf("unmarshal sampling response: %v", err)
	}
	if sr.Content.Text != "sampled" {
		t.Fatalf("unexpected sampling response: %+v", sr)
	}
}

func TestMRTRResolveRootsInputRequest(t *testing.T) {
	c := NewClient("test", "echo", nil)
	in := map[string]json.RawMessage{"roots1": json.RawMessage(`{"method":"roots/list","params":{}}`)}
	resp, err := c.resolveInputRequests(context.Background(), in)
	if err != nil {
		t.Fatalf("resolveInputRequests: %v", err)
	}
	var got struct {
		Roots []struct {
			URI string `json:"uri"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(resp["roots1"], &got); err != nil {
		t.Fatalf("unmarshal roots response: %v", err)
	}
	if len(got.Roots) != 1 || !strings.HasPrefix(got.Roots[0].URI, "file://") {
		t.Fatalf("unexpected roots response: %s", resp["roots1"])
	}
}

func TestMRTRResolveRejectsUnknownMethod(t *testing.T) {
	c := NewClient("test", "echo", nil)
	in := map[string]json.RawMessage{
		"evil": json.RawMessage(`{"method":"resources/write","params":{"uri":"file:///etc/passwd"}}`),
	}
	if _, err := c.resolveInputRequests(context.Background(), in); err == nil {
		t.Fatal("expected rejection of non-whitelisted input request method")
	}
}

func TestMRTRResolveElicitationWithoutHandlerFails(t *testing.T) {
	c := NewClient("test", "echo", nil)
	in := map[string]json.RawMessage{
		"e1": json.RawMessage(`{"method":"elicitation/create","params":{"message":"need key","requestedSchema":{"type":"object","properties":{}}}}`),
	}
	if _, err := c.resolveInputRequests(context.Background(), in); err == nil || !strings.Contains(err.Error(), "elicitation not supported") {
		t.Fatalf("expected elicitation-not-supported error, got: %v", err)
	}
}

func TestMRTRResolveMalformedInputRequest(t *testing.T) {
	c := NewClient("test", "echo", nil)
	in := map[string]json.RawMessage{"bad": json.RawMessage(`["not","an","object"]`)}
	if _, err := c.resolveInputRequests(context.Background(), in); err == nil {
		t.Fatal("expected error for malformed input request")
	}
}
