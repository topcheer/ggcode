package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func (a *Agent) maybeInjectCorrectionFeedback() {
	a.mu.RLock()
	cpMgr := a.checkpoints
	a.mu.RUnlock()
	if cpMgr == nil {
		return
	}

	corrections := cpMgr.RecentCorrections()
	if len(corrections) == 0 {
		return
	}

	filtered := make([]checkpoint.Correction, 0, len(corrections))
	for _, c := range corrections {
		if c.Source == "" || c.Source == "user" {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		return
	}
	corrections = filtered

	fileSet := make(map[string]bool)
	for _, c := range corrections {
		for _, f := range c.Files {
			fileSet[f] = true
		}
	}

	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}

	var b strings.Builder
	b.WriteString("Note: The user reverted your previous file changes (via /undo or /undo-run).\n")
	b.WriteString("The following files were reverted to their pre-edit state:\n")

	for i, f := range files {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(files)-10))
			break
		}
		b.WriteString(fmt.Sprintf("  - %s\n", f))
	}
	b.WriteString("\n")
	b.WriteString("This means your previous approach was likely wrong or unwanted. ")
	b.WriteString("Do NOT simply repeat the same changes. Instead:\n")
	b.WriteString("1. Re-read the files to understand their current (reverted) state\n")
	b.WriteString("2. Consider a fundamentally different approach\n")
	b.WriteString("3. If unsure why your changes were rejected, ask the user for clarification\n")

	msg := b.String()

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: msg},
		},
	})

	debug.Log("agent", "injected correction feedback: %d corrections, %d unique files", len(corrections), len(files))

	cpMgr.ClearCorrections()
}

func (a *Agent) maybeInjectSentimentFeedback(userPrompt string) {
	if a.userSentiment == nil {
		return
	}

	fb := a.userSentiment.analyzeAndUpdate(userPrompt)
	if fb.Level == 0 {
		return
	}

	guidance := buildSentimentGuidance(fb)
	if guidance == "" {
		return
	}

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: guidance},
		},
	})

	debug.Log("agent", "%s", formatSentimentLogLine(fb))

	if shouldResetMonitoringOnFeedback(fb) {

		a.resetOverseer(userPrompt)
		a.resetScopeDrift()
		a.recurringError.reset()
		a.confidence.reset()
		a.resetPlanner()
		debug.Log("agent", "reset monitoring systems due to strong negative user feedback (level=%d)", fb.Level)
	}
}
