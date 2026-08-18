package context

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
)

// BudgetCategory classifies a context consumer by type.
type BudgetCategory string

const (
	CategorySystem     BudgetCategory = "system"
	CategoryUser       BudgetCategory = "user"
	CategoryAssistant  BudgetCategory = "assistant"
	CategoryToolCall   BudgetCategory = "tool_call"
	CategoryToolResult BudgetCategory = "tool_result"
)

// CategoryTokens tracks token usage for a single category.
type CategoryTokens struct {
	Category BudgetCategory
	Tokens   int
	Count    int // number of items in this category
}

// MessageTokenInfo records the estimated token count for a single message
// and its primary category. Used for the top-N largest consumer analysis.
type MessageTokenInfo struct {
	Index    int            // message index in the conversation
	Role     string         // original message role
	Category BudgetCategory // primary category
	Tokens   int            // estimated tokens
	Preview  string         // short preview of content (first 80 chars)
}

// BudgetBreakdown is the result of analyzing context consumption.
type BudgetBreakdown struct {
	TotalTokens        int
	Categories         []CategoryTokens
	TopMessages        []MessageTokenInfo // top-N largest messages by token count
	LargestToolResults []MessageTokenInfo // largest tool_result messages
}

// Percentage returns the fraction of total tokens for a category (0-100).
func (b *BudgetBreakdown) Percentage(cat BudgetCategory) float64 {
	if b.TotalTokens == 0 {
		return 0
	}
	for _, c := range b.Categories {
		if c.Category == cat {
			return float64(c.Tokens) / float64(b.TotalTokens) * 100
		}
	}
	return 0
}

// FormatHumanReadable returns a multi-line summary suitable for logging or
// display in a status panel.
func (b *BudgetBreakdown) FormatHumanReadable() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Context Budget: %d tokens total\n", b.TotalTokens))
	for _, c := range b.Categories {
		pct := 0.0
		if b.TotalTokens > 0 {
			pct = float64(c.Tokens) / float64(b.TotalTokens) * 100
		}
		sb.WriteString(fmt.Sprintf("  %-14s %6d tokens (%5.1f%%)  %d items\n",
			c.Category, c.Tokens, pct, c.Count))
	}
	if len(b.TopMessages) > 0 {
		sb.WriteString("\nTop consumers:\n")
		for _, m := range b.TopMessages {
			sb.WriteString(fmt.Sprintf("  [%d] %-14s %6d tok  %s\n",
				m.Index, m.Category, m.Tokens, m.Preview))
		}
	}
	return strings.TrimSpace(sb.String())
}

// topN is the maximum number of largest messages to report.
const defaultTopN = 5

// AnalyzeBudget categorizes context consumption by message type and identifies
// the largest individual consumers. This gives both users and the system itself
// visibility into what's consuming context tokens — enabling smarter compaction
// decisions and context optimization.
//
// Token estimation delegates to the same script-aware estimator as
// EstimateTokens (#702c: the old len/4 fork systematically underestimated
// pure-CJK text by ~25%-2x versus the tokenizer's ~1.0 chars/token CJK tier).
// This is intentionally lightweight (no LLM calls, no network) so it can be
// called on every turn if needed.
func AnalyzeBudget(msgs []provider.Message) *BudgetBreakdown {
	bd := &BudgetBreakdown{}
	catMap := make(map[BudgetCategory]*CategoryTokens)

	allMsgs := make([]MessageTokenInfo, 0, len(msgs))
	var largestToolResults []MessageTokenInfo

	for i, msg := range msgs {
		msgTokens := 0
		primaryCat := categorizeRole(msg.Role)

		for _, block := range msg.Content {
			blockTokens := estimateBlockTokens(block)
			msgTokens += blockTokens

			// Track per-block-type category
			blockCat := categorizeBlock(block, msg.Role)
			if _, ok := catMap[blockCat]; !ok {
				catMap[blockCat] = &CategoryTokens{Category: blockCat}
			}
			catMap[blockCat].Tokens += blockTokens
			catMap[blockCat].Count++

			// Track tool results for largest-consumer analysis
			if blockCat == CategoryToolResult {
				// Merge into message-level tracking
			}
		}

		bd.TotalTokens += msgTokens

		// Determine the effective category: if the message is primarily
		// tool_result blocks, classify it as CategoryToolResult rather than
		// by the raw role (which is "user" for tool results in Anthropic format).
		effectiveCat := primaryCat
		if isToolResultMessage(msg) {
			effectiveCat = CategoryToolResult
		}

		// Build message info for top-N analysis
		preview := messagePreview(msg)
		info := MessageTokenInfo{
			Index:    i,
			Role:     msg.Role,
			Category: effectiveCat,
			Tokens:   msgTokens,
			Preview:  preview,
		}
		allMsgs = append(allMsgs, info)

		// Track largest tool_result messages separately
		if effectiveCat == CategoryToolResult && msgTokens > 100 {
			largestToolResults = append(largestToolResults, info)
		}
	}

	// Convert catMap to sorted slice
	for _, ct := range catMap {
		bd.Categories = append(bd.Categories, *ct)
	}
	sort.Slice(bd.Categories, func(i, j int) bool {
		return bd.Categories[i].Tokens > bd.Categories[j].Tokens
	})

	// Sort all messages by token count descending, take top N
	sort.Slice(allMsgs, func(i, j int) bool {
		return allMsgs[i].Tokens > allMsgs[j].Tokens
	})
	if len(allMsgs) > defaultTopN {
		allMsgs = allMsgs[:defaultTopN]
	}
	bd.TopMessages = allMsgs

	// Sort tool results by size descending, take top 3
	sort.Slice(largestToolResults, func(i, j int) bool {
		return largestToolResults[i].Tokens > largestToolResults[j].Tokens
	})
	if len(largestToolResults) > 3 {
		largestToolResults = largestToolResults[:3]
	}
	bd.LargestToolResults = largestToolResults

	return bd
}

// isToolResultMessage returns true if the message contains at least one
// tool_result block (and no text blocks). In Anthropic format, tool results
// are sent as "user" role messages containing tool_result content blocks.
func isToolResultMessage(msg provider.Message) bool {
	hasResult := false
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			hasResult = true
		} else if block.Type == "text" && block.Text != "" {
			// Mixed message with text — classify by role, not as pure tool result
			return false
		}
	}
	return hasResult
}

// categorizeRole maps a message role to a budget category.
func categorizeRole(role string) BudgetCategory {
	switch role {
	case "system":
		return CategorySystem
	case "user":
		return CategoryUser
	case "assistant":
		return CategoryAssistant
	default:
		return CategoryUser
	}
}

// categorizeBlock determines the budget category for a content block.
func categorizeBlock(block provider.ContentBlock, msgRole string) BudgetCategory {
	switch block.Type {
	case "tool_use":
		return CategoryToolCall
	case "tool_result":
		return CategoryToolResult
	default:
		return categorizeRole(msgRole)
	}
}

// estimateBlockTokens estimates the token count for a single content block
// using the ~4 chars/token heuristic. Image blocks are estimated at a fixed
// overhead since base64 image data is much larger than its token equivalent.
func estimateBlockTokens(block provider.ContentBlock) int {
	// Text blocks: script-aware estimator (same as EstimateTokens).
	if block.Text != "" {
		return EstimateTokens(block.Text)
	}

	// Tool use: input JSON
	if block.Type == "tool_use" && len(block.Input) > 0 {
		return EstimateTokens(string(block.Input)) + 10 // overhead for tool name + structure
	}

	// Tool result: output text
	if block.Type == "tool_result" && block.Output != "" {
		return EstimateTokens(block.Output) + 5 // small overhead
	}

	// Reasoning content
	if block.ReasoningContent != "" {
		return EstimateTokens(block.ReasoningContent)
	}

	// Image blocks: base64 data is inflated; estimate actual token overhead
	if block.Type == "image" && block.ImageData != "" {
		return 300 // typical image token cost for standard resolution
	}

	return 0
}

// estimateTokensChars converts a character count to an estimated token count
// using the ~4 chars/token heuristic.
func estimateTokensChars(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}

// messagePreview extracts a short preview string from a message.
func messagePreview(msg provider.Message) string {
	for _, block := range msg.Content {
		switch {
		case block.Text != "":
			return truncatePreview(block.Text)
		case block.Type == "tool_use":
			return fmt.Sprintf("[tool_call: %s]", block.ToolName)
		case block.Type == "tool_result":
			if block.IsError {
				return fmt.Sprintf("[tool_error: %s]", truncatePreview(block.Output))
			}
			return fmt.Sprintf("[tool_result: %s]", truncatePreview(block.Output))
		}
	}
	return "(empty)"
}

// truncatePreview shortens a string to fit in a preview line.
// #520: the cut must land on a rune boundary — 77 bytes splits a 3-byte CJK
// character in two, leaving an invalid UTF-8 prefix that renders as U+FFFD.
func truncatePreview(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		cut := s[:77]
		// #520: back off until the cut is valid UTF-8 — a byte cut at 77 can
		// land inside a multi-byte CJK rune; dropping bytes until the string
		// is valid removes both the partial tail and its lead byte (at most
		// 2 iterations for a 3-byte rune).
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		return cut + "..."
	}
	return s
}
