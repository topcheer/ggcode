package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/toolreplay"
)

func TestRenderVerifyReport(t *testing.T) {
	report := toolreplay.VerifyReport{
		TapePath: "session.tape.json",
		Results: []toolreplay.VerifyResult{
			{Index: 0, ToolName: "read_file", Input: json.RawMessage(`{"path":"a.txt"}`), Status: toolreplay.VerifyMatch},
			{Index: 1, ToolName: "read_file", Input: json.RawMessage(`{"path":"b.txt"}`),
				Status: toolreplay.VerifyDiverged, Detail: "content differs (recorded 12 B, live 8 B)"},
			{Index: 2, ToolName: "grep", Status: toolreplay.VerifyCut, Detail: "served from record"},
			{Index: 3, ToolName: "write_file", Status: toolreplay.VerifySkippedUnsafe,
				Detail: "mutating tool; use --include-unsafe to execute live"},
			{Index: 4, ToolName: "gone_tool", Status: toolreplay.VerifyNoTool},
		},
	}
	out := renderVerifyReport(report)
	for _, want := range []string{
		"#0", "#4",
		"read_file", "write_file", "gone_tool",
		"content differs",
		"FAIL",
		"diverged: 1", "no-tool: 1", "skipped-unsafe: 1", "cut: 1", "match: 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report output missing %q:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("report should end with a newline")
	}

	// Truncated long input stays on one line.
	long := toolreplay.VerifyReport{Results: []toolreplay.VerifyResult{{
		Index: 0, ToolName: "read_file",
		Input:  json.RawMessage(`{"path":"` + strings.Repeat("x", 100) + `"}`),
		Status: toolreplay.VerifyMatch,
	}}}
	for _, line := range strings.Split(renderVerifyReport(long), "\n") {
		if strings.Contains(line, strings.Repeat("x", 49)) {
			t.Error("long input not truncated")
		}
	}
}

func TestRenderVerifyReport_PassVerdict(t *testing.T) {
	report := toolreplay.VerifyReport{Results: []toolreplay.VerifyResult{
		{Index: 0, ToolName: "read_file", Status: toolreplay.VerifyMatch},
	}}
	if out := renderVerifyReport(report); !strings.Contains(out, "PASS") {
		t.Errorf("all-match report should PASS, got:\n%s", out)
	}
}

func writeTestTape(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.tape.json")
	tf := map[string]interface{}{
		"version": 1,
		"entries": []map[string]interface{}{
			{"tool_name": "read_file", "input": json.RawMessage(`{"path":"a.txt"}`),
				"input_hash": "deadbeef", "result": map[string]interface{}{"content": "hi"}},
		},
	}
	data, err := json.Marshal(tf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTapeVerifyCmd_BadTapePath(t *testing.T) {
	cmd := newTapeVerifyCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{filepath.Join(t.TempDir(), "missing.tape.json")})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for missing tape file")
	}
}

func TestTapeVerifyCmd_ArgValidation(t *testing.T) {
	cmd := newTapeVerifyCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when no tape path given")
	}
}

func TestTapeInfoCmd_ListsBoundaries(t *testing.T) {
	path := writeTestTape(t)
	cmd := newTapeInfoCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("info: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "1 boundaries") || !strings.Contains(got, "read_file") {
		t.Errorf("info output unexpected:\n%s", got)
	}
}
