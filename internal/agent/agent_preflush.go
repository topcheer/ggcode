package agent

import (
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

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
