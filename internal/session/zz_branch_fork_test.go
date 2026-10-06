package session

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// zz_branch_fork_test.go - session-tree fork point computation (#346-style
// arbitrary-point branching, /branch N drops the last N user turns).
func TestComputeBranchCutoff(t *testing.T) {
	msg := func(role, text string) provider.Message {
		return provider.Message{Role: role, Content: []provider.ContentBlock{{Type: "text", Text: text}}}
	}
	msgs := []provider.Message{
		msg("user", "u1"),
		msg("assistant", "a1"),
		msg("user", "u2"),
		msg("assistant", "a2"),
		msg("user", "u3"),
		msg("assistant", "a3"),
	}

	t.Run("tail fork keeps everything", func(t *testing.T) {
		got, ok := ComputeBranchCutoff(msgs, 0)
		if !ok || got != len(msgs) {
			t.Fatalf("drop=0: got (%d, %v), want (%d, true)", got, ok, len(msgs))
		}
	})

	t.Run("negative treated as tail fork", func(t *testing.T) {
		got, ok := ComputeBranchCutoff(msgs, -3)
		if !ok || got != len(msgs) {
			t.Fatalf("drop<0: got (%d, %v), want (%d, true)", got, ok, len(msgs))
		}
	})

	t.Run("drop 1 round cuts at last user message", func(t *testing.T) {
		// userIdx = [0,2,4]; drop 1 -> cutoff = 4 (drop u3 round, keep u1..a2)
		got, ok := ComputeBranchCutoff(msgs, 1)
		if !ok || got != 4 {
			t.Fatalf("drop=1: got (%d, %v), want (4, true)", got, ok)
		}
	})

	t.Run("drop 2 rounds cuts at second user message", func(t *testing.T) {
		got, ok := ComputeBranchCutoff(msgs, 2)
		if !ok || got != 2 {
			t.Fatalf("drop=2: got (%d, %v), want (2, true)", got, ok)
		}
	})

	t.Run("dropping every user turn is rejected", func(t *testing.T) {
		if _, ok := ComputeBranchCutoff(msgs, 3); ok {
			t.Fatal("drop=3 (== user turns): want ok=false")
		}
		if _, ok := ComputeBranchCutoff(msgs, 99); ok {
			t.Fatal("drop=99: want ok=false")
		}
	})

	t.Run("no user turns cannot fork back", func(t *testing.T) {
		sysOnly := []provider.Message{msg("system", "s"), msg("assistant", "a")}
		if _, ok := ComputeBranchCutoff(sysOnly, 1); ok {
			t.Fatal("no user turns + drop=1: want ok=false")
		}
		// But a tail fork of a user-less list still keeps everything.
		if got, ok := ComputeBranchCutoff(sysOnly, 0); !ok || got != len(sysOnly) {
			t.Fatalf("no user turns + drop=0: got (%d, %v), want (%d, true)", got, ok, len(sysOnly))
		}
	})

	t.Run("empty list tail fork", func(t *testing.T) {
		if got, ok := ComputeBranchCutoff(nil, 0); !ok || got != 0 {
			t.Fatalf("nil + drop=0: got (%d, %v), want (0, true)", got, ok)
		}
	})
}
