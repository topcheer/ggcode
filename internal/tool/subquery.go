package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

// subquery.go implements Recursive Language Model (RLM) sub-queries for the
// code_execution sandbox (Zhang & Khattab, "Recursive Language Models",
// arXiv:2512.24601, Dec 2025). RLMs treat a long prompt as part of an
// external environment that the model can programmatically examine,
// decompose, and *recursively call itself over snippets of*. ggcode already
// had the "context as environment" half (the sandbox keeps big tool results
// in JS and only summaries reach the window); tools.subquery() adds the
// recursion half: the model can route a focused LLM call over ONE snippet it
// selected, so multi-part analysis of an oversized output (200KB build log,
// 5MB JSON) never enters the main context window.
//
// Budgets (RLM recursion guard — a runaway JS loop must not become an
// unbounded LLM spend loop):
//   - at most maxSubQueriesPerRun sub-LLM calls per code_execution run
//   - context snippet capped at maxSubQueryContextBytes (rune-safe)
//   - task prompt capped at maxSubQueryPromptBytes (rune-safe)
// The per-call deadline is the sandbox's own execCtx (codeExecTimeout), so a
// hanging provider chat cannot outlive the tool call that requested it.

const (
	// maxSubQueryContextBytes caps the context snippet forwarded to the
	// sub-LLM call. 64KB is far above any main-window-safe snippet yet
	// bounded; larger inputs must be decomposed into multiple calls —
	// which is exactly the RLM pattern.
	maxSubQueryContextBytes = 64 * 1024

	// maxSubQueryPromptBytes caps the task prompt portion.
	maxSubQueryPromptBytes = 8 * 1024

	// maxSubQueriesPerRun caps recursive sub-LLM calls per sandbox run.
	maxSubQueriesPerRun = 8
)

// SubQueryFn performs ONE focused LLM call over a pre-composed prompt and
// returns the model's text reply. Implemented by adapters over
// provider.Provider (NewProviderSubQueryFn); tests inject fakes.
type SubQueryFn func(ctx context.Context, prompt string) (string, error)

// buildSubQueryPrompt composes the single user message sent to the sub-LLM.
// The isolation framing matters: without it, models tend to answer from
// prior knowledge instead of the provided snippet and hallucinate content
// "found" in the context.
func buildSubQueryPrompt(task, contextSnippet string) string {
	var b strings.Builder
	b.WriteString("You are a focused data-extraction assistant inside a code sandbox. ")
	b.WriteString("Answer the task below using ONLY the <context> block. ")
	b.WriteString("Do not invent content that is not in the context. Be concise; reply with plain text only.\n\n")
	b.WriteString("<context>\n")
	b.WriteString(contextSnippet)
	b.WriteString("\n</context>\n\nTask: ")
	b.WriteString(task)
	return b.String()
}

// NewProviderSubQueryFn adapts a provider getter (the same late-bound
// pattern as the MCP sampling handler, #1592-B) into a SubQueryFn. The
// getter is read at call time so runtimes that bind their provider after
// tool registration (interactive core) still resolve correctly.
func NewProviderSubQueryFn(providerFn func() provider.Provider) SubQueryFn {
	return func(ctx context.Context, prompt string) (string, error) {
		p := providerFn()
		if p == nil {
			return "", fmt.Errorf("no LLM provider available for subquery")
		}
		resp, err := p.Chat(ctx, []provider.Message{{
			Role:    "user",
			Content: []provider.ContentBlock{provider.TextBlock(prompt)},
		}}, nil)
		if err != nil {
			return "", fmt.Errorf("subquery provider chat: %w", err)
		}
		var sb strings.Builder
		for _, block := range resp.Message.Content {
			if block.Type == "text" {
				sb.WriteString(block.Text)
			}
		}
		out := strings.TrimSpace(sb.String())
		if out == "" {
			return "", fmt.Errorf("subquery returned an empty response")
		}
		return out, nil
	}
}

// subQueryTruncateContext rune-safely caps a context snippet and appends an
// explicit truncation marker so the sub-LLM knows the snippet is partial
// (models otherwise treat a cut-off snippet as complete evidence).
func subQueryTruncateContext(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return truncateSandboxText(s, max) +
		fmt.Sprintf("\n[context truncated: %d bytes total, showing first %d]", len(s), max)
}
