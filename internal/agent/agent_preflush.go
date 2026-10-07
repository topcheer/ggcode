package agent

// r491 pre-compaction memory flush wiring (see internal/memory/preflush.go
// for the concept and online evidence). This file adapts provider.Message to
// the memory package's provider-free PreflushMsg shape and exposes the two
// call sites' shared entry point.

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

// preflushPayloads converts live-context messages to the flush input:
// user/assistant roles only, text blocks only (system prompts are full of
// instruction-shaped noise that is not session facts; tool payloads are not
// line-structured constraint text).
func preflushPayloads(msgs []provider.Message) []memory.PreflushMsg {
	var out []memory.PreflushMsg
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		var b strings.Builder
		for _, blk := range m.Content {
			if blk.Type == "text" && blk.Text != "" {
				b.WriteString(blk.Text)
				b.WriteByte('\n')
			}
		}
		if b.Len() > 0 {
			out = append(out, memory.PreflushMsg{Role: m.Role, Text: b.String()})
		}
	}
	return out
}

// preflushFacts persists constraint-line facts from the about-to-be-folded
// slice into project memory. Called on BOTH compaction paths (auto
// pre-compact and reactive) BEFORE summarization; the reactive path may run
// after an auto flush already covered the same slice - the merge-dedupe makes
// that second call a no-op. Failures are debug-logged inside; the flush must
// never block or abort compaction.
func (a *Agent) preflushFacts(trigger string, msgs []provider.Message) int {
	a.mu.Lock()
	wd := a.workingDir
	a.mu.Unlock()
	n := memory.PreflushFacts(wd, preflushPayloads(msgs))
	if n > 0 {
		debug.Log("preflush", "[%s] persisted %d fact(s) to project memory before compaction", trigger, n)
	}
	return n
}
