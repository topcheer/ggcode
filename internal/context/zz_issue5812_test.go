package context

import (
	"reflect"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func issue5812Sys(text string) provider.Message {
	return provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

func issue5812User(text string) provider.Message {
	return provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

// TestIssue5812MutationSourcesAtCallSites pins the call-site
// instrumentation: every public mutation path records its source string
// for the ctxstats sampler, and the byte-identical rewrite path (#5807)
// records nothing.
func TestIssue5812MutationSourcesAtCallSites(t *testing.T) {
	cases := []struct {
		name  string
		drive func(m *Manager)
		want  []string
	}{{
		name: "system-rewrite",
		drive: func(m *Manager) {
			m.Add(issue5812Sys("HEAD ONE"))
			m.UpdateFirstSystemMessage(issue5812Sys("HEAD TWO"))
		},
		want: []string{MutSourceSystemRewrite},
	}, {
		name: "system-insert",
		drive: func(m *Manager) {
			m.UpdateFirstSystemMessage(issue5812Sys("HEAD"))
		},
		want: []string{MutSourceSystemInsert},
	}, {
		name: "identical rewrite records nothing",
		drive: func(m *Manager) {
			m.Add(issue5812Sys("HEAD"))
			m.UpdateFirstSystemMessage(issue5812Sys("HEAD"))
		},
		want: nil,
	}, {
		name: "ptl-truncate",
		drive: func(m *Manager) {
			m.Add(issue5812Sys("HEAD"))
			m.Add(issue5812User("first question"))
			m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "first answer"}}})
			m.Add(issue5812User("second question"))
			if !m.TruncateOldestGroupForRetry() {
				t.Fatal("fixture setup: PTL truncate refused")
			}
		},
		want: []string{MutSourcePTLTruncate},
	}, {
		name: "user-reset",
		drive: func(m *Manager) {
			m.Add(issue5812Sys("HEAD"))
			m.Add(issue5812User("question"))
			m.Clear()
		},
		want: []string{MutSourceUserReset},
	}, {
		name: "compaction",
		drive: func(m *Manager) {
			seedWindowMessages(m)
			snapshot := m.CompactSnapshot()
			if applied, _ := m.ApplyCompactResult(snapshot, summaryResult()); !applied {
				t.Fatal("fixture setup: ApplyCompactResult refused")
			}
		},
		want: []string{MutSourceCompaction},
	}, {
		name: "remove-last-group",
		drive: func(m *Manager) {
			m.Add(issue5812User("question"))
			m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "answer"}}})
			if m.RemoveLastAssistantGroup() == "" {
				t.Fatal("fixture setup: RemoveLastAssistantGroup removed nothing")
			}
		},
		want: []string{MutSourceRemoveLastGroup},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(10000)
			tc.drive(m)
			got := m.DrainMutationSources()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("mutation sources = %v, want %v", got, tc.want)
			}
			if again := m.DrainMutationSources(); again != nil {
				t.Fatalf("drain must clear the pending set, got %v", again)
			}
		})
	}
}

// TestIssue5812NonTailMutSeqAndAppend pins the seq contract the sampler
// relies on: plain appends never bump nonTailMutSeq (#5808), head
// mutations do.
func TestIssue5812NonTailMutSeqAndAppend(t *testing.T) {
	m := NewManager(10000)
	before := m.NonTailMutSeq()
	m.Add(issue5812User("plain append"))
	if m.NonTailMutSeq() != before {
		t.Fatal("plain append must not bump nonTailMutSeq")
	}
	m.UpdateFirstSystemMessage(issue5812Sys("HEAD"))
	if m.NonTailMutSeq() != before+1 {
		t.Fatalf("nonTailMutSeq = %d, want %d", m.NonTailMutSeq(), before+1)
	}
}
