package context

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #5194: all three mechanical compaction paths (reasoning, superseded reads,
// superseded commands) must commit through the shared commitMechanicalCompaction
// helper so version/nonTailMutSeq/token recalc AND benignRemoval bookkeeping stay
// in lockstep. The reasoning path previously hand-rolled the commit triplet and
// silently skipped markBenignRemoval.
func TestIssue5194ReasoningCompactionUsesSharedCommit(t *testing.T) {
	m := NewManager(100000)

	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q1"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
		Type:             "text",
		Text:             "a1",
		ReasoningContent: strings.Repeat("r", 500),
	}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q2"}}})
	// Last assistant message keeps its reasoning (only pre-last-assistant
	// blocks are compaction targets).
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
		Type:             "text",
		Text:             "a2",
		ReasoningContent: strings.Repeat("k", 500),
	}}})

	beforeTokens := m.tokens
	beforeVersion := m.version
	beforeSeq := m.nonTailMutSeq
	m.benignRemoval = false

	freed := m.CompactOldReasoningBlocks()
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
	}

	// Same assertions the other two mechanical paths satisfy via the shared
	// helper: token recalc matches the reported freed amount...
	if got := beforeTokens - m.tokens; got != freed {
		t.Fatalf("token accounting drift: before=%d after=%d freed=%d", beforeTokens, m.tokens, freed)
	}
	// ...version and nonTailMutSeq both advance...
	if m.version != beforeVersion+1 {
		t.Fatalf("version: want %d, got %d", beforeVersion+1, m.version)
	}
	if m.nonTailMutSeq != beforeSeq+1 {
		t.Fatalf("nonTailMutSeq: want %d, got %d", beforeSeq+1, m.nonTailMutSeq)
	}
	// ...and the benign-removal marker is set, exactly like the reads/commands
	// paths (the drift the issue filed: reasoning skipped markBenignRemoval).
	if !m.benignRemoval {
		t.Fatal("CompactOldReasoningBlocks did not set benignRemoval: reasoning path bypasses commitMechanicalCompaction")
	}
}

// The three mechanical paths must leave identical commit-shaped state: for the
// same message-set mutation class, each path bumps version by exactly 1,
// nonTailMutSeq by exactly 1, and reports freed == actual token delta.
func TestIssue5194MechanicalCompactionStateConsistency(t *testing.T) {
	buildReads := func() *Manager {
		m := NewManager(100000)
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-1",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/foo.go"}`),
		}}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-1", strings.Repeat("a", 500), false),
		}})
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "read-2",
			ToolName: "read_file",
			Input:    json.RawMessage(`{"path":"/foo.go"}`),
		}}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			provider.ToolResultBlock("read-2", strings.Repeat("b", 500), false),
		}})
		return m
	}

	buildCommands := func() *Manager {
		m := NewManager(100000)
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "cmd-1",
			ToolName: "run_command",
			Input:    json.RawMessage(`{"command":"go test ./..."}`),
		}}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			provider.ToolResultBlock("cmd-1", strings.Repeat("c", 500), false),
		}})
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
			Type:     "tool_use",
			ToolID:   "cmd-2",
			ToolName: "run_command",
			Input:    json.RawMessage(`{"command":"go test ./..."}`),
		}}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			provider.ToolResultBlock("cmd-2", strings.Repeat("d", 500), false),
		}})
		return m
	}

	buildReasoning := func() *Manager {
		m := NewManager(100000)
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{
			Type:             "text",
			Text:             "a1",
			ReasoningContent: strings.Repeat("r", 500),
		}}})
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "q"}}})
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "a2"}}})
		return m
	}

	cases := []struct {
		name string
		m    *Manager
		run  func(*Manager) int
	}{
		{"superseded reads", buildReads(), func(m *Manager) int { return m.CompactSupersededReads() }},
		{"superseded commands", buildCommands(), func(m *Manager) int { return m.CompactSupersededCommands() }},
		{"old reasoning", buildReasoning(), func(m *Manager) int { return m.CompactOldReasoningBlocks() }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeTokens := tc.m.tokens
			beforeVersion := tc.m.version
			beforeSeq := tc.m.nonTailMutSeq
			tc.m.benignRemoval = false

			freed := tc.run(tc.m)
			if freed <= 0 {
				t.Fatalf("expected freed > 0, got %d", freed)
			}
			if got := beforeTokens - tc.m.tokens; got != freed {
				t.Fatalf("freed mismatch: reported %d, actual delta %d", freed, got)
			}
			if tc.m.version != beforeVersion+1 {
				t.Fatalf("version: want %d, got %d", beforeVersion+1, tc.m.version)
			}
			if tc.m.nonTailMutSeq != beforeSeq+1 {
				t.Fatalf("nonTailMutSeq: want %d, got %d", beforeSeq+1, tc.m.nonTailMutSeq)
			}
			if !tc.m.benignRemoval {
				t.Fatalf("%s path did not set benignRemoval", tc.name)
			}
		})
	}
}
