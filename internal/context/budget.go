package context

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
)

type BudgetCategory string

const (
	CategorySystem     BudgetCategory = "system"
	CategoryUser       BudgetCategory = "user"
	CategoryAssistant  BudgetCategory = "assistant"
	CategoryToolCall   BudgetCategory = "tool_call"
	CategoryToolResult BudgetCategory = "tool_result"
)

type CategoryTokens struct {
	Category BudgetCategory
	Tokens   int
	Count    int
}

type MessageTokenInfo struct {
	Index    int
	Role     string
	Category BudgetCategory
	Tokens   int
	Preview  string
}

type BudgetBreakdown struct {
	TotalTokens        int
	Categories         []CategoryTokens
	TopMessages        []MessageTokenInfo
	LargestToolResults []MessageTokenInfo
}

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

const defaultTopN = 5

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

			blockCat := categorizeBlock(block, msg.Role)
			if _, ok := catMap[blockCat]; !ok {
				catMap[blockCat] = &CategoryTokens{Category: blockCat}
			}
			catMap[blockCat].Tokens += blockTokens
			catMap[blockCat].Count++
		}

		bd.TotalTokens += msgTokens

		effectiveCat := primaryCat
		if isToolResultMessage(msg) {
			effectiveCat = CategoryToolResult
		}

		preview := messagePreview(msg)
		info := MessageTokenInfo{
			Index:    i,
			Role:     msg.Role,
			Category: effectiveCat,
			Tokens:   msgTokens,
			Preview:  preview,
		}
		allMsgs = append(allMsgs, info)

		if effectiveCat == CategoryToolResult && msgTokens > 100 {
			largestToolResults = append(largestToolResults, info)
		}
	}

	for _, ct := range catMap {
		bd.Categories = append(bd.Categories, *ct)
	}
	sort.Slice(bd.Categories, func(i, j int) bool {
		return bd.Categories[i].Tokens > bd.Categories[j].Tokens
	})

	sort.Slice(allMsgs, func(i, j int) bool {
		return allMsgs[i].Tokens > allMsgs[j].Tokens
	})
	if len(allMsgs) > defaultTopN {
		allMsgs = allMsgs[:defaultTopN]
	}
	bd.TopMessages = allMsgs

	sort.Slice(largestToolResults, func(i, j int) bool {
		return largestToolResults[i].Tokens > largestToolResults[j].Tokens
	})
	if len(largestToolResults) > 3 {
		largestToolResults = largestToolResults[:3]
	}
	bd.LargestToolResults = largestToolResults

	return bd
}

func isToolResultMessage(msg provider.Message) bool {
	hasResult := false
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			hasResult = true
		} else if block.Type == "text" && block.Text != "" {
			return false
		}
	}
	return hasResult
}

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

func estimateBlockTokens(block provider.ContentBlock) int {
	if block.Text != "" {
		return EstimateTokens(block.Text)
	}

	if block.Type == "tool_use" && len(block.Input) > 0 {
		return EstimateTokens(string(block.Input)) + 10
	}

	if block.Type == "tool_result" {
		n := EstimateTokens(block.Output) + 5
		n += len(block.Images) * 300
		return n
	}

	if block.ReasoningContent != "" {
		return EstimateTokens(block.ReasoningContent)
	}

	if block.Type == "image" && block.ImageData != "" {
		return 300
	}

	return 0
}

func estimateTokensChars(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}

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

func truncatePreview(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		cut := s[:77]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		return cut + "..."
	}
	return s
}
