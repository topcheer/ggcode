package swarm

// sa-90 tests: step-level failure attribution for parked swarm tasks
// (AgenTracer, arXiv 2509.03312 / ICLR 2026 - attribute failures to the
// specific agent AND step). Covers: attribution extraction from a teammate
// event trail, target extraction from JSON tool args, JSON roundtrip into
// task metadata, graceful degradation when no tool events exist, and the
// human-readable FormatAttribution rendering.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func newAttributionTestTeammate() *Teammate {
	return &Teammate{
		ID:     "tm-attr",
		Name:   "coder",
		Status: TeammateIdle,
		Inbox:  make(chan MailMessage, 16),
	}
}

func TestBuildFailureAttribution_ExtractsLastToolAndTarget(t *testing.T) {
	tm := newAttributionTestTeammate()
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolCall, ToolName: "grep", ToolArgs: `{"pattern":"foo"}`})
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolResult, ToolName: "grep", Result: "3 matches"})
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolCall, ToolName: "edit_file", ToolArgs: `{"file_path":"/src/a.go","old_text":"x"}`})
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolResult, ToolName: "edit_file", Result: "ok"})

	fa := buildFailureAttribution(tm, "task-9", errors.New("quota exhausted: 429"))
	if fa == nil {
		t.Fatal("expected attribution, got nil")
	}
	if fa.AgentID != "tm-attr" || fa.AgentName != "coder" || fa.TaskID != "task-9" {
		t.Fatalf("identity fields wrong: %+v", fa)
	}
	if fa.Step != 2 {
		t.Fatalf("step = %d, want 2 (two tool calls)", fa.Step)
	}
	if fa.LastTool != "edit_file" {
		t.Fatalf("lastTool = %q, want edit_file", fa.LastTool)
	}
	if fa.LastTarget != "/src/a.go" {
		t.Fatalf("lastTarget = %q, want /src/a.go (file_path key)", fa.LastTarget)
	}
	if !strings.Contains(fa.ErrorDigest, "429") {
		t.Fatalf("errorDigest missing message: %q", fa.ErrorDigest)
	}
	if fa.ParkedAt.IsZero() {
		t.Fatal("ParkedAt not set")
	}
}

func TestBuildFailureAttribution_NoToolEventsReturnsNil(t *testing.T) {
	tm := newAttributionTestTeammate()
	tm.appendEvent(TeammateEvent{Type: TeammateEventText, Text: "thinking..."})

	if fa := buildFailureAttribution(tm, "task-1", errors.New("boom")); fa != nil {
		t.Fatalf("expected nil for tool-less trail, got %+v", fa)
	}
	// nil teammate / nil error also degrade to nil.
	if fa := buildFailureAttribution(nil, "task-1", errors.New("boom")); fa != nil {
		t.Fatalf("expected nil for nil teammate, got %+v", fa)
	}
	if fa := buildFailureAttribution(tm, "task-1", nil); fa != nil {
		t.Fatalf("expected nil for nil error, got %+v", fa)
	}
}

func TestAttributionTarget_PreferentialKeys(t *testing.T) {
	cases := []struct {
		args string
		want string
	}{
		{`{"file_path":"/x.go","command":"ls"}`, "/x.go"}, // file_path wins
		{`{"path":"/tmp"}`, "/tmp"},                       // path fallback
		{`{"command":"go test ./..."}`, "go test ./..."},  // command fallback
		{`{"pattern":"TODO.*"}`, "TODO.*"},                // pattern fallback
		{`{"query":"auth logic"}`, "auth logic"},          // query fallback
		{`{"other":"v"}`, ""},                             // unrecognized -> empty
		{`not json`, ""},                                  // unparseable -> empty
		{``, ""},                                          // empty -> empty
		{`{"file_path":"  "}`, ""},                        // blank value -> empty
	}
	for _, c := range cases {
		if got := attributionTarget(c.args); got != c.want {
			t.Errorf("attributionTarget(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestAttachAttribution_MetadataRoundtrip(t *testing.T) {
	tm := newAttributionTestTeammate()
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolCall, ToolName: "run_command", ToolArgs: `{"command":"make test"}`})
	fa := buildFailureAttribution(tm, "task-7", errors.New("exit status 2"))

	meta := attachAttribution(nil, fa)
	blob, ok := meta[AttributionMetaKey]
	if !ok || blob == "" {
		t.Fatalf("attribution key missing: %v", meta)
	}
	var back FailureAttribution
	if err := json.Unmarshal([]byte(blob), &back); err != nil {
		t.Fatalf("blob not valid JSON: %v", err)
	}
	if back.LastTool != "run_command" || back.LastTarget != "make test" || back.Step != 1 {
		t.Fatalf("roundtrip lost fields: %+v", back)
	}

	// nil attribution must not create a key, and must not panic on nil map.
	if m := attachAttribution(nil, nil); m != nil {
		t.Fatalf("nil attribution should return nil map, got %v", m)
	}
	// existing keys preserved.
	meta2 := map[string]string{"permanent_error": "quota"}
	meta2 = attachAttribution(meta2, fa)
	if meta2["permanent_error"] != "quota" || meta2[AttributionMetaKey] == "" {
		t.Fatalf("existing metadata clobbered: %v", meta2)
	}
}

func TestFormatAttribution_RendersOneLine(t *testing.T) {
	tm := newAttributionTestTeammate()
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolCall, ToolName: "edit_file", ToolArgs: `{"file_path":"/src/b.go"}`})
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolCall, ToolName: "run_command", ToolArgs: `{"command":"go build"}`})
	fa := buildFailureAttribution(tm, "task-3", errors.New("429 quota"))
	meta := attachAttribution(nil, fa)

	line := FormatAttribution(meta)
	for _, want := range []string{"coder(tm-attr)", "step#2", "run_command", "go build", "429 quota"} {
		if !strings.Contains(line, want) {
			t.Errorf("FormatAttribution missing %q: %q", want, line)
		}
	}

	if got := FormatAttribution(map[string]string{}); got != "" {
		t.Errorf("empty metadata should render empty, got %q", got)
	}
	if got := FormatAttribution(map[string]string{AttributionMetaKey: "{bad json"}); got != "" {
		t.Errorf("invalid blob should render empty, got %q", got)
	}
}

func TestBuildFailureAttribution_LongValuesTruncated(t *testing.T) {
	tm := newAttributionTestTeammate()
	longCmd := strings.Repeat("x", 500)
	tm.appendEvent(TeammateEvent{Type: TeammateEventToolCall, ToolName: "run_command", ToolArgs: `{"command":"` + longCmd + `"}`})
	longErr := errors.New(strings.Repeat("e", 500))
	fa := buildFailureAttribution(tm, "task-5", longErr)
	if fa == nil {
		t.Fatal("expected attribution")
	}
	if len(fa.LastTarget) > 120 {
		t.Errorf("lastTarget not truncated: %d chars", len(fa.LastTarget))
	}
	if len(fa.ErrorDigest) > 200 {
		t.Errorf("errorDigest not truncated: %d chars", len(fa.ErrorDigest))
	}
	_ = time.Now() // keep time import if fields change
}
