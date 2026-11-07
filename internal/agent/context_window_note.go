package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/provider"
)

// #5206 follow-up (user report): when the context window is ENLARGED
// mid-session, earlier context-pressure advisories stay in the transcript
// - the model has no way to know the limit moved, so it keeps acting under
// pressure that no longer exists. NoteContextWindowChanged injects a
// correction into the live context after a growth step and re-arms the
// continuation advisory (one-shot latch) so it can re-evaluate on the new
// scale. Shrinks are NOT corrected here: lower limits make real pressure
// more likely, and auto-compact handles that path.

func contextWindowCorrectionMsg(oldCW, newCW, tokens, threshold int) string {
	pct := 0
	if threshold > 0 {
		pct = int(float64(tokens) / float64(threshold) * 100)
	}
	return fmt.Sprintf(
		"[context-note] The context window limit changed from %d to %d tokens. "+
			"Earlier context-pressure warnings were computed against the old (smaller) limit and no longer apply. "+
			"Current usage is %d tokens (%d%% of the compaction threshold); continue the task normally.",
		oldCW, newCW, tokens, pct)
}

// NoteContextWindowChanged is called by the runtime after enlarging the
// context window on a LIVE session. No-op for shrinks and no-op when the
// session is still genuinely near the (new) compaction threshold.
func (a *Agent) NoteContextWindowChanged(oldCW, newCW int) {
	if a == nil || a.contextManager == nil || newCW <= oldCW || oldCW <= 0 {
		return
	}
	if a.termGuard != nil {
		a.termGuard.fired = false // re-arm: re-evaluate under the new scale
	}
	threshold := a.contextManager.AutoCompactThreshold()
	if threshold <= 0 {
		return
	}
	tokens := a.contextManager.TokenCount()
	if float64(tokens)/float64(threshold) >= ptGuardOccupancy {
		return // still near the real compaction point - pressure is real
	}
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: contextWindowCorrectionMsg(oldCW, newCW, tokens, threshold),
		}},
	})
}
