package metrics

import (
	"strings"
	"testing"
	"time"
)

// sa-218: sub-agent events carry agent_id, which must surface as
// gen_ai.agent.name on llm/tool spans in the OTLP export.
func TestOTLPExportSubAgentAttribution(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Microsecond)
	evs := []MetricEvent{
		{Timestamp: base, TurnIndex: 1, Type: "session"},
		{Timestamp: base, TurnIndex: 1, Type: "llm", Model: "glm-5", Duration: 100 * time.Millisecond, AgentID: "agent-abc"},
		{Timestamp: base.Add(time.Millisecond), TurnIndex: 1, Type: "tool", ToolName: "read_file", ToolSuccess: true, ToolDuration: 5 * time.Millisecond, AgentID: "agent-abc"},
	}
	export, err := ExportTraceOTLP("sess-1", "zai", "ep", "glm-5", base, evs)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(export)

	if !strings.Contains(doc, "gen_ai.agent.name") {
		t.Fatal("sub-agent events must produce gen_ai.agent.name attributes")
	}
	if !strings.Contains(doc, "ggcode:subagent:agent-abc") {
		t.Fatalf("agent.name value wrong, got: %s", doc)
	}
	// Parent and sub-agent spans share the session-derived traceId.
	if !strings.Contains(doc, "traceId") {
		t.Fatal("traceId missing")
	}
}

// Parent (empty AgentID) events must NOT gain an agent.name attribute -
// the session span keeps its plain ggcode identity.
func TestOTLPExportParentUnattributed(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Microsecond)
	evs := []MetricEvent{
		{Timestamp: base, TurnIndex: 1, Type: "session"},
		{Timestamp: base, TurnIndex: 1, Type: "tool", ToolName: "read_file", ToolSuccess: true},
	}
	export, err := ExportTraceOTLP("sess-2", "zai", "ep", "glm-5", base, evs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(export), "subagent") {
		t.Fatalf("parent events must not be attributed as sub-agent: %s", export)
	}
}
