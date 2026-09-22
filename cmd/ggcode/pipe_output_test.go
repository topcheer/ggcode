package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestNormalizePipeOutputFormat(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", pipeFormatText, false},
		{"text", pipeFormatText, false},
		{"  JSON ", pipeFormatJSON, false},
		{"stream-json", pipeFormatStreamJSON, false},
		{"STREAM-JSON", pipeFormatStreamJSON, false},
		{"yaml", "", true},
		{"json;", "", true},
	}
	for _, tc := range cases {
		got, err := normalizePipeOutputFormat(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizePipeOutputFormat(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizePipeOutputFormat(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizePipeOutputFormat(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func newTestEmitterMeta() pipeEmitterMeta {
	return pipeEmitterMeta{
		SessionID:      "pipe-42",
		Model:          "glm-5",
		Cwd:            "/work",
		Tools:          []string{"read_file", "write_file"},
		PermissionMode: "auto",
	}
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line is not valid JSON: %q: %v", line, err)
		}
		out = append(out, obj)
	}
	return out
}

func TestPipeEmitterStreamJSONSequence(t *testing.T) {
	var buf bytes.Buffer
	em := newPipeEmitter(&buf, pipeFormatStreamJSON, newTestEmitterMeta())

	if err := em.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	em.TextDelta("Let me ")
	em.TextDelta("check that.")
	em.ToolCallDone("read_file", json.RawMessage(`{"path":"/work/a.go"}`))
	em.ToolResult("/work/a.go contents", false)
	em.TurnDone(&provider.TokenUsage{InputTokens: 100, OutputTokens: 20}, false)
	em.TextDelta("Done.")
	if err := em.Finish(nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	lines := decodeLines(t, &buf)
	if len(lines) != 6 {
		t.Fatalf("expected 6 NDJSON lines (init, assistant, tool_use, tool_result, assistant, result), got %d", len(lines))
	}
	if lines[0]["type"] != "system" || lines[0]["subtype"] != "init" {
		t.Errorf("line 0: expected system/init, got %v %v", lines[0]["type"], lines[0]["subtype"])
	}
	if tools, ok := lines[0]["tools"].([]any); !ok || len(tools) != 2 {
		t.Errorf("init tools: expected 2 entries, got %v", lines[0]["tools"])
	}
	if lines[1]["type"] != "assistant" {
		t.Errorf("line 1: expected assistant, got %v", lines[1]["type"])
	}
	if got := firstAssistantText(t, lines[1]); got != "Let me check that." {
		t.Errorf("assistant text = %q, want buffered deltas joined", got)
	}
	if lines[2]["type"] != "tool_use" || lines[2]["name"] != "read_file" {
		t.Errorf("line 2: expected tool_use/read_file, got %v/%v", lines[2]["type"], lines[2]["name"])
	}
	if input, ok := lines[2]["input"].(map[string]any); !ok || input["path"] != "/work/a.go" {
		t.Errorf("tool_use input: got %v", lines[2]["input"])
	}
	if lines[3]["type"] != "tool_result" || lines[3]["is_error"] != false {
		t.Errorf("line 3: expected tool_result ok, got %v is_error=%v", lines[3]["type"], lines[3]["is_error"])
	}
	if lines[4]["type"] != "assistant" {
		t.Errorf("line 4: expected final assistant flush, got %v", lines[4]["type"])
	}
	if lines[5]["type"] != "result" || lines[5]["subtype"] != pipeResultSuccess {
		t.Errorf("line 5: expected result/success, got %v/%v", lines[5]["type"], lines[5]["subtype"])
	}
	result := lines[5]
	if result["result"] != "Let me check that.Done." {
		t.Errorf("result text = %q", result["result"])
	}
	if result["num_turns"] != float64(1) || result["tool_uses"] != float64(1) {
		t.Errorf("num_turns/tool_uses = %v/%v, want 1/1", result["num_turns"], result["tool_uses"])
	}
	usage, ok := result["usage"].(map[string]any)
	if !ok || usage["input_tokens"] != float64(100) || usage["output_tokens"] != float64(20) {
		t.Errorf("usage = %v, want input=100 output=20", result["usage"])
	}
	if result["session_id"] != "pipe-42" {
		t.Errorf("session_id = %v", result["session_id"])
	}
}

func firstAssistantText(t *testing.T, line map[string]any) string {
	t.Helper()
	msg, ok := line["message"].(map[string]any)
	if !ok {
		t.Fatalf("assistant line missing message: %v", line)
	}
	content, ok := msg["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("assistant message missing content: %v", msg)
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content block not an object: %v", content[0])
	}
	text, _ := block["text"].(string)
	return text
}

func TestPipeEmitterJSONSingleObject(t *testing.T) {
	var buf bytes.Buffer
	em := newPipeEmitter(&buf, pipeFormatJSON, newTestEmitterMeta())

	if err := em.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("json mode Init must not emit anything, got %q", buf.String())
	}
	em.TextDelta("answer")
	em.TurnDone(&provider.TokenUsage{InputTokens: 10, OutputTokens: 5}, false)
	em.TurnDone(&provider.TokenUsage{InputTokens: 30, OutputTokens: 7}, false)
	em.ToolCallDone("grep", json.RawMessage(`{"pattern":"x"}`))
	if err := em.Finish(nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("json format must emit exactly one object, got %d", len(lines))
	}
	obj := lines[0]
	if obj["type"] != "result" || obj["subtype"] != pipeResultSuccess || obj["is_error"] != false {
		t.Errorf("payload = %v", obj)
	}
	if obj["result"] != "answer" {
		t.Errorf("result = %v", obj["result"])
	}
	if obj["num_turns"] != float64(2) || obj["tool_uses"] != float64(1) {
		t.Errorf("num_turns/tool_uses = %v/%v, want 2/1", obj["num_turns"], obj["tool_uses"])
	}
	usage, ok := obj["usage"].(map[string]any)
	if !ok || usage["input_tokens"] != float64(40) || usage["output_tokens"] != float64(12) {
		t.Errorf("usage = %v, want accumulated input=40 output=12", obj["usage"])
	}
}

func TestPipeEmitterFinishError(t *testing.T) {
	var buf bytes.Buffer
	em := newPipeEmitter(&buf, pipeFormatJSON, newTestEmitterMeta())
	em.TextDelta("partial")
	if err := em.Finish(errors.New("boom")); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	obj := decodeLines(t, &buf)[0]
	if obj["subtype"] != pipeResultErrorExecution || obj["is_error"] != true {
		t.Errorf("subtype/is_error = %v/%v, want error_during_execution/true", obj["subtype"], obj["is_error"])
	}
	if obj["error"] != "boom" {
		t.Errorf("error = %v, want boom", obj["error"])
	}
}

func TestPipeEmitterFinishTruncated(t *testing.T) {
	var buf bytes.Buffer
	em := newPipeEmitter(&buf, pipeFormatJSON, newTestEmitterMeta())
	em.TurnDone(&provider.TokenUsage{InputTokens: 1, OutputTokens: 1}, true)
	if err := em.Finish(nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	obj := decodeLines(t, &buf)[0]
	if obj["subtype"] != pipeResultErrorMaxOutTokens || obj["is_error"] != true {
		t.Errorf("subtype/is_error = %v/%v, want error_max_output_tokens/true", obj["subtype"], obj["is_error"])
	}
}

func TestPipeEmitterToolResultSummaryBounded(t *testing.T) {
	var buf bytes.Buffer
	em := newPipeEmitter(&buf, pipeFormatStreamJSON, newTestEmitterMeta())
	em.ToolCallDone("run_command", json.RawMessage(`{}`))
	big := strings.Repeat("x", 500) + "\nsecond line"
	em.ToolResult(big, true)
	_ = em.Finish(nil)

	for _, line := range decodeLines(t, &buf) {
		if line["type"] == "tool_result" {
			summary, _ := line["summary"].(string)
			if len(summary) > 120 {
				t.Errorf("summary length %d exceeds bound 120", len(summary))
			}
			if strings.Contains(summary, "second line") {
				t.Errorf("summary must be first line only, got %q", summary)
			}
			if isErr, ok := line["is_error"].(bool); !ok || !isErr {
				t.Errorf("tool_result is_error = %v, want true", line["is_error"])
			}
			return
		}
	}
	t.Fatalf("no tool_result line in output: %s", buf.String())
}

func TestPipeEmitterEmptyToolArgsDefault(t *testing.T) {
	var buf bytes.Buffer
	em := newPipeEmitter(&buf, pipeFormatStreamJSON, newTestEmitterMeta())
	em.ToolCallDone("todo_write", nil)
	_ = em.Finish(nil)
	for _, line := range decodeLines(t, &buf) {
		if line["type"] == "tool_use" {
			if _, ok := line["input"].(map[string]any); !ok {
				t.Errorf("nil args must serialize as {}, got %v", line["input"])
			}
			return
		}
	}
	t.Fatalf("no tool_use line: %s", buf.String())
}
