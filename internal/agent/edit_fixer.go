package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// edit-fixer wiring: when edit_file exhausts its deterministic match
// fallbacks, repair old_text with one cheap provider call instead of
// bouncing a full re-read-and-retry round trip back through the main loop
// (arXiv 2609.00006 "Harness Engineering", LLM edit-fixer pattern).
//
// Safety containment lives in internal/tool (budgets, memo, timeout,
// re-matching through the normal uniqueness gates); this file only owns the
// provider call and prompt.

const editFixSystemPrompt = `You repair failed search-and-replace edits for a coding agent. You receive a file excerpt, the failed old_text, and the intended new_text. Find where the edit was intended and output the corrected old_text: the exact text CURRENTLY in the file that should be replaced, copied verbatim with exact indentation. If the old_text refers to a renamed symbol, use the original symbol name as it appears in the file. Output ONLY that text wrapped in <fixed_old_text> and </fixed_old_text> tags. If the intent is unclear or the text genuinely does not exist in the excerpt, output <fixed_old_text></fixed_old_text>.`

const editFixTagOpen = "<fixed_old_text>"
const editFixTagClose = "</fixed_old_text>"

// wireEditFixer connects the tool-layer repair hook to the session
// provider. Called from NewAgent; the last wire wins across sub-agents
// (each agent inherits and reinstalls its provider).
func wireEditFixer(p provider.Provider) {
	if p == nil {
		tool.SetEditFixer(nil)
		return
	}
	tool.SetEditFixer(func(ctx context.Context, req tool.EditFixRequest) (string, bool) {
		return fixOldTextWithModel(ctx, p, req)
	})
}

func fixOldTextWithModel(ctx context.Context, p provider.Provider, req tool.EditFixRequest) (string, bool) {
	var user strings.Builder
	fmt.Fprintf(&user, "File: %s\n\n", req.FilePath)
	user.WriteString("File excerpt (numbered):\n")
	user.WriteString(req.FileExcerpt)
	user.WriteString("\n\nFailed old_text (did not match the file):\n")
	user.WriteString(req.OldText)
	if req.NewText != "" {
		user.WriteString("\n\nIntended replacement new_text (for context):\n")
		user.WriteString(req.NewText)
	}

	resp, err := p.Chat(ctx, []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: editFixSystemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: user.String()}}},
	}, nil)
	if err != nil {
		debug.Log("agent", "edit-fixer: model call failed: %v", err)
		return "", false
	}
	if resp == nil {
		return "", false
	}

	text := ""
	for _, b := range resp.Message.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	corrected := extractEditFixPayload(text)
	if corrected == "" {
		debug.Log("agent", "edit-fixer: no correction produced (len=%d)", len(text))
		return "", false
	}
	return corrected, true
}

// extractEditFixPayload pulls the corrected old_text out of the tagged
// response. Models that drop the tags fall back to their whole reply; the
// tool layer re-matches the result through the normal gates, so noisy
// fallbacks fail closed rather than editing the wrong bytes.
func extractEditFixPayload(text string) string {
	if open := strings.Index(text, editFixTagOpen); open >= 0 {
		rest := text[open+len(editFixTagOpen):]
		if close := strings.Index(rest, editFixTagClose); close >= 0 {
			return strings.TrimSpace(rest[:close])
		}
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(text)
}
