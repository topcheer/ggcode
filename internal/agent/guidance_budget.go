package agent

import (
	"time"

	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	guidanceBudgetPerTurn = 2

	guidanceBudgetBytesPerTurn = 2048

	guidanceBudgetCriticalBytesPerTurn = 1024
)

type guidanceBudget struct {
	injected      int
	suppressed    int
	appendedBytes int
	criticalBytes int
	delivered     []string
	seenHintTags  map[string]bool
}

func (g *guidanceBudget) reset() {
	if g.suppressed > 0 {
		debug.Log("guidance-budget", "previous turn: %d guidance messages suppressed (budget=%d)",
			g.suppressed, guidanceBudgetPerTurn)
	}
	g.injected = 0
	g.suppressed = 0
	g.seenHintTags = nil
	g.appendedBytes = 0
	g.criticalBytes = 0
	g.delivered = nil
}

func (g *guidanceBudget) allow(text string) bool {

	if isCriticalGuidance(text) {
		if g.criticalBytes+len(text) > guidanceBudgetCriticalBytesPerTurn {
			g.suppressed++
			return false
		}
		return true
	}

	if g.appendedBytes+len(text) > guidanceBudgetBytesPerTurn {
		g.suppressed++
		return false
	}

	if strings.ToLower(extractHintTag(text)) == "guidance-conflict" {
		return true
	}
	if g.injected < guidanceBudgetPerTurn {
		g.injected++
		return true
	}
	g.suppressed++
	return false
}

func (g *guidanceBudget) allowDeduped(text string) bool {

	tag := strings.ToLower(extractHintTag(text))
	if tag != "" {
		if g.seenHintTags == nil {
			g.seenHintTags = make(map[string]bool)
		}
		if g.seenHintTags[tag] {
			g.suppressed++
			return false
		}

	}
	ok := g.allow(text)
	if ok {
		if tag != "" {
			if g.seenHintTags == nil {
				g.seenHintTags = make(map[string]bool)
			}
			g.seenHintTags[tag] = true
		}
		g.chargeBytes(len(text), isCriticalGuidance(text))
		g.delivered = append(g.delivered, text)
	}
	return ok
}

func (g *guidanceBudget) chargeBytes(n int, critical bool) {
	if critical {
		g.criticalBytes += n
		return
	}
	g.appendedBytes += n
}

func isCriticalGuidance(text string) bool {
	tag := extractHintTag(text)
	return isCriticalTag(tag)
}

func (a *Agent) guidanceEmit(msg string, msgs []provider.Message) []provider.Message {
	if msg == "" {
		return msgs
	}
	a.injectGuidance(msg)
	return a.contextManager.Messages()
}

func (a *Agent) injectGuidance(text string) bool {

	allowed := a.guidanceBudget.allow(text)

	if allowed && harnessOverrideSuppresses(guidanceTag(text)) {
		allowed = false
	}
	if a.guidanceStats != nil {
		a.guidanceStats.record(guidanceTag(text), allowed)
	}

	if allowed {
		a.recordGuidanceHint(guidanceTag(text), text, time.Now().UTC().Format(time.RFC3339))
	}
	if !allowed {
		debug.Log("guidance-budget", "suppressing guidance message (budget exceeded, %d suppressed this turn)",
			a.guidanceBudget.suppressed)
		return false
	}

	if ch := detectGuidanceConflict(stripCoalescedSummaries(append(append([]string{}, a.guidanceBudget.delivered...), text))); ch != "" && a.guidanceBudget.allowDeduped(ch) {
		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: ch,
			}},
		})
	}
	a.guidanceBudget.chargeBytes(len(text), isCriticalGuidance(text))
	a.guidanceBudget.delivered = append(a.guidanceBudget.delivered, text)
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: text,
		}},
	})
	return true
}
