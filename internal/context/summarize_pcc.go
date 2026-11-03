package context

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	pccMaxBlocks        = 4
	pccMinPayloadTokens = 12000
)

func splitPayloadBlocks(payload string, targetTokens int) []string {
	if targetTokens <= 0 || EstimateTokens(payload) <= targetTokens {
		return nil
	}
	lines := strings.Split(payload, "\n")
	units := [][]string{}
	cur := []string{}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "[") && strings.HasSuffix(ln, "]") && len(cur) > 0 {
			units = append(units, cur)
			cur = []string{}
		}
		cur = append(cur, ln)
	}
	if len(cur) > 0 {
		units = append(units, cur)
	}
	if len(units) <= 1 {
		return nil
	}
	blocks := []string{}
	var sb strings.Builder
	for _, u := range units {
		uText := strings.Join(u, "\n")
		if sb.Len() > 0 && EstimateTokens(sb.String())+EstimateTokens(uText) > targetTokens && len(blocks) < pccMaxBlocks-1 {
			blocks = append(blocks, sb.String())
			sb.Reset()
		}
		sb.WriteString(uText)
		sb.WriteByte('\n')
	}
	if sb.Len() > 0 {
		blocks = append(blocks, sb.String())
	}
	if len(blocks) <= 1 {
		return nil
	}
	return blocks
}

func pccBlockSystemPrompt(blockBudget int) string {
	return fmt.Sprintf(`You are summarizing ONE BLOCK of a longer conversation between a user and an AI coding assistant. Other blocks are being summarized in parallel; your block summary will be concatenated with the others in order, so do NOT reference content outside your block.

Your block summary must be under %d tokens. Be extremely concise.

Structure your output as bullet points (skip categories that are empty):
- Task/goal statements
- Completed & verified work (mark ✅)
- In-flight changes (file, function, what was being edited)
- Key files touched (path + 1-3 word note)
- Decisions & USER CONSTRAINTS (must preserve)
- Dead ends / failed approaches (with reason)
- Background tasks / sub-agents (IDs and purpose)
- Verbatim user requests in this block

Omit: full source code, verbose command output, tool-call mechanics.`, blockBudget)
}

func summarizeParallel(ctx context.Context, prov provider.Provider, blocks []string, summaryTokenLimit int, onUsage func(provider.TokenUsage)) (string, bool) {
	numBlocks := len(blocks)
	blockBudget := summaryTokenLimit / numBlocks
	if blockBudget < 1 {
		blockBudget = 1
	}
	type blockResult struct {
		idx   int
		text  string
		usage provider.TokenUsage
		err   error
	}
	results := make([]blockResult, numBlocks)
	var wg sync.WaitGroup
	for i := range blocks {
		wg.Add(1)
		idx := i
		safego.Go("ctx.pcc.block", func() {
			defer wg.Done()
			summaryMsgs := []provider.Message{
				{
					Role: "system",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: pccBlockSystemPrompt(blockBudget),
					}},
				},
				{
					Role: "user",
					Content: []provider.ContentBlock{{
						Type: "text",
						Text: fmt.Sprintf("Summarize this block (block %d of %d):\n\n%s", idx+1, numBlocks, blocks[idx]),
					}},
				},
			}
			resp, err := prov.Chat(ctx, summaryMsgs, nil)
			if err != nil {
				results[idx] = blockResult{idx: idx, err: err}
				return
			}
			text := ""
			for _, block := range resp.Message.Content {
				if block.Type == "text" && block.Text != "" {
					text = block.Text
					break
				}
			}
			results[idx] = blockResult{idx: idx, text: text, usage: resp.Usage}
		})
	}
	wg.Wait()
	var parts []string
	for _, r := range results {
		if r.err != nil {
			debug.Log("ctx", "summarizeParallel: block %d failed (%v) — falling back to sequential path", r.idx, r.err)
			return "", false
		}
		if r.text == "" {
			debug.Log("ctx", "summarizeParallel: block %d returned empty text — falling back to sequential path", r.idx)
			return "", false
		}
		parts = append(parts, r.text)
	}
	for _, r := range results {
		if (r.usage.InputTokens > 0 || r.usage.OutputTokens > 0) && onUsage != nil {
			onUsage(r.usage)
		}
	}
	joined := strings.Join(parts, "\n\n---\n\n")
	debug.Log("ctx", "summarizeParallel: %d blocks, joined summary len=%d chars est=%d tokens budget=%d",
		numBlocks, len(joined), EstimateTokens(joined), summaryTokenLimit)
	return joined, true
}
