package runeval

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestEvaluateCoordinationGate verifies the section stays hidden for
// single-agent streams with no swarm/subagent activity.
func TestEvaluateCoordinationGate(t *testing.T) {
	if got := EvaluateCoordination(nil); got != nil {
		t.Fatalf("empty stream: want nil report, got %+v", got)
	}
	single := []CoordEvent{
		{Seq: 1, Kind: CoordTool, AgentID: "main", Tool: "read_file", InputKey: "{\"path\":\"a.go\"}", Tokens: 10},
		{Seq: 2, Kind: CoordTool, AgentID: "main", Tool: "edit_file", InputKey: "{\"path\":\"a.go\"}", Tokens: 10},
	}
	if got := EvaluateCoordination(single); got != nil {
		t.Fatalf("single-agent no-delegation stream: want nil report, got %+v", got)
	}
}

// TestEvaluateCoordinationMultiAgent exercises all five dimensions with a
// deterministic two-agent stream.
func TestEvaluateCoordinationMultiAgent(t *testing.T) {
	events := []CoordEvent{
		// main delegates two units of work.
		{Seq: 1, Kind: CoordDelegation, AgentID: "main", Tool: "spawn_agent", Tokens: 50},
		{Seq: 2, Kind: CoordDelegation, AgentID: "main", Tool: "spawn_agent", Tokens: 50},
		// both agents grep the same file concurrently (overlap 1000-2000ms).
		{Seq: 3, Kind: CoordTool, AgentID: "main", Tool: "grep", InputKey: "k", ResultHash: "h1", StartMS: 1000, EndMS: 2000, Tokens: 100},
		{Seq: 4, Kind: CoordTool, AgentID: "sub1", Tool: "grep", InputKey: "k", ResultHash: "h1", StartMS: 1500, EndMS: 2500, Tokens: 100},
		// only one delegation returns, successfully, and is read back once.
		{Seq: 5, Kind: CoordDelegationResult, AgentID: "main", Success: true, Tokens: 200},
		{Seq: 6, Kind: CoordReadBack, AgentID: "main", Tokens: 30},
		{Seq: 7, Kind: CoordMessage, AgentID: "sub1", Tokens: 20},
	}
	q := EvaluateCoordination(events)
	if q == nil {
		t.Fatal("multi-agent stream: want report, got nil")
	}
	if q.AgentCount != 2 {
		t.Errorf("AgentCount = %d, want 2", q.AgentCount)
	}
	if len(q.CrossAgentDuplicateGroups) != 1 {
		t.Fatalf("CrossAgentDuplicateGroups = %d groups, want 1", len(q.CrossAgentDuplicateGroups))
	}
	dg := q.CrossAgentDuplicateGroups[0]
	if dg.Tool != "grep" || dg.Count != 2 || dg.Repeats != 1 || !dg.Identical || !dg.ReadOnly {
		t.Errorf("duplicate group = %+v, want grep x2 identical read-only", dg)
	}
	if q.MessageRoundTrips != 1 || q.UnansweredDelegations != 1 {
		t.Errorf("round trips = %d, unanswered = %d; want 1, 1", q.MessageRoundTrips, q.UnansweredDelegations)
	}
	if q.DelegationRecoveryRate != 0.5 || q.ReadBackCoverage != 0.5 {
		t.Errorf("recovery = %.2f, coverage = %.2f; want 0.5, 0.5", q.DelegationRecoveryRate, q.ReadBackCoverage)
	}
	if q.DelegationChainIntegrity != 0.25 {
		t.Errorf("integrity = %.2f, want 0.25", q.DelegationChainIntegrity)
	}
	if q.ParallelLayerWasteMS != 500 {
		t.Errorf("ParallelLayerWasteMS = %d, want 500 (overlap 1500-2000)", q.ParallelLayerWasteMS)
	}
	// tokens: coord = 50+50+200+30+20 = 350; total = 550.
	if q.CoordinationTokens != 350 || q.TotalTokens != 550 {
		t.Errorf("coord tokens = %d/%d, want 350/550", q.CoordinationTokens, q.TotalTokens)
	}
	if q.CoordinationTokenShare < 0.636 || q.CoordinationTokenShare > 0.637 {
		t.Errorf("token share = %.3f, want ~0.636", q.CoordinationTokenShare)
	}
	if len(q.Findings) == 0 {
		t.Error("findings empty; want duplicate/unanswered/integrity hints")
	}
}

// TestEvaluateCoordinationSeqOrder verifies Seq reorders an out-of-order
// stream before counting round trips.
func TestEvaluateCoordinationSeqOrder(t *testing.T) {
	q := EvaluateCoordination([]CoordEvent{
		{Seq: 3, Kind: CoordDelegationResult, AgentID: "main", Success: true},
		{Seq: 1, Kind: CoordDelegation, AgentID: "main"},
	})
	if q == nil {
		t.Fatal("want report, got nil")
	}
	if q.MessageRoundTrips != 1 || q.UnansweredDelegations != 0 {
		t.Errorf("round trips = %d, unanswered = %d; want 1, 0", q.MessageRoundTrips, q.UnansweredDelegations)
	}
}

// TestCoordEventsFromMessages verifies log-derived delegation sequences feed
// the same pipeline, and that plain sessions yield no coordination section.
func TestCoordEventsFromMessages(t *testing.T) {
	msgsWithDelegation := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "spawn_agent", Input: []byte(`{"task":"x"}`)},
			{Type: "tool_use", ToolID: "t2", ToolName: "read_file", Input: []byte(`{"path":"a"}`)},
		}},
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "tool_result", ToolID: "t1", Output: "done"},
			{Type: "tool_result", ToolID: "t2", Output: "data"},
		}},
	}
	events := CoordEventsFromMessages(msgsWithDelegation)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (delegation + result)", len(events))
	}
	if events[0].Kind != CoordDelegation || events[1].Kind != CoordDelegationResult {
		t.Errorf("kinds = %s/%s, want delegation/delegation_result", events[0].Kind, events[1].Kind)
	}
	if !events[1].Success {
		t.Error("delegation result Success = false, want true")
	}
	q := EvaluateCoordination(events)
	if q == nil {
		t.Fatal("delegation-only stream: want report, got nil")
	}
	if q.MessageRoundTrips != 1 || q.UnansweredDelegations != 0 {
		t.Errorf("round trips = %d, unanswered = %d; want 1, 0", q.MessageRoundTrips, q.UnansweredDelegations)
	}
	if q.DelegationChainIntegrity != 0 {
		t.Errorf("integrity = %.2f, want 0 (no read-back)", q.DelegationChainIntegrity)
	}

	plain := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t9", ToolName: "read_file", Input: []byte(`{"path":"a"}`)},
		}},
	}
	if got := EvaluateCoordination(CoordEventsFromMessages(plain)); got != nil {
		t.Errorf("plain session: want nil coordination report, got %+v", got)
	}
}

// TestRenderCoordinationSection verifies the /runreport output gates on the
// coordination report being present.
func TestRenderCoordinationSection(t *testing.T) {
	base := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "go"}}},
		{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "tool_use", ToolID: "t1", ToolName: "read_file", Input: []byte(`{"path":"a"}`)},
		}},
	}
	plain := Render(EvaluateWithCoordination(base, nil, nil))
	if stringsContains(plain, "Coordination:") {
		t.Error("plain render contains Coordination section; want absent")
	}
	withCoord := Render(EvaluateWithCoordination(base, nil, []CoordEvent{
		{Seq: 1, Kind: CoordDelegation, AgentID: "main", Tokens: 10},
		{Seq: 2, Kind: CoordDelegationResult, AgentID: "main", Success: true, Tokens: 10},
	}))
	if !stringsContains(withCoord, "Coordination: 1 agents · 0 cross-agent dup groups · 1 round trip (0 unanswered)") {
		t.Errorf("coordination line missing or malformed:\n%s", withCoord)
	}
}

func stringsContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
