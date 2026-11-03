package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	whyDefaultCount = 5
	whyMaxCount     = 20
	whyReasonRunes  = 200
	whyTargetMax    = 60
	whyAnchorMax    = 3
)

var whyInputKeys = []string{"file_path", "path", "command", "pattern", "url", "target", "name"}

func WhyCountArg(parts []string) int {
	n := whyDefaultCount
	if len(parts) >= 2 {
		if v, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
			n = v
		}
	}
	if n < 1 {
		n = 1
	}
	if n > whyMaxCount {
		n = whyMaxCount
	}
	return n
}

func ExplainDecisions(msgs []provider.Message, n int) string {
	if n < 1 {
		n = 1
	}
	if n > whyMaxCount {
		n = whyMaxCount
	}
	var idx []int
	for i := range msgs {
		if msgs[i].Role != "assistant" {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == "tool_use" {
				idx = append(idx, i)
				break
			}
		}
	}
	if len(idx) == 0 {
		return ""
	}
	if len(idx) > n {
		idx = idx[len(idx)-n:]
	}

	var sb strings.Builder
	shown := 0
	for _, mi := range idx {
		m := msgs[mi]
		lastThinking := ""
		hasTool := false
		for bi := range m.Content {
			b := &m.Content[bi]
			switch b.Type {
			case "thinking":
				if b.ReasoningContent != "" {
					lastThinking = b.ReasoningContent
				}
			case "tool_use":
				hasTool = true
				shown++
				sb.WriteString(fmt.Sprintf("[%d] tool: %s %s\n", shown, b.ToolName, whyInputTarget(b.Input)))
				sb.WriteString(fmt.Sprintf("     why: %s\n", whyReasoning(lastThinking)))
				if res, ok := whyFindResult(msgs, mi, b.ToolID); ok {
					sb.WriteString(fmt.Sprintf("     result: %s\n", res))
				}
			}
		}
		_ = hasTool
	}
	return strings.TrimRight(sb.String(), "\n")
}

func whyInputTarget(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, k := range whyInputKeys {
		if v, ok := m[k]; ok {
			s := strings.TrimSpace(fmt.Sprintf("%v", v))
			if s == "" {
				continue
			}
			s = strings.ReplaceAll(s, "\n", " ")
			if utf8.RuneCountInString(s) > whyTargetMax {
				s = string([]rune(s)[:whyTargetMax]) + "…"
			}
			return s
		}
	}
	return ""
}

func whyReasoning(thinking string) string {
	if thinking == "" {
		return "<no reasoning block recorded>"
	}
	if strings.HasPrefix(thinking, "[compacted:") {
		return "<" + thinking + ">"
	}
	s := strings.ReplaceAll(strings.TrimSpace(thinking), "\n", " ⏎ ")
	if utf8.RuneCountInString(s) > whyReasonRunes {
		s = string([]rune(s)[:whyReasonRunes]) + "…"
	}
	return s
}

func whyFindResult(msgs []provider.Message, from int, toolID string) (string, bool) {
	if toolID == "" {
		return "", false
	}
	for j := from + 1; j < len(msgs) && j <= from+3; j++ {
		for _, rb := range msgs[j].Content {
			if rb.Type != "tool_result" || rb.ToolID != toolID {
				continue
			}
			if !rb.IsError {
				return "ok", true
			}
			out := strings.TrimSpace(rb.Output)
			if len(out) > 160 {
				out = out[:160] + "…"
			}
			anchors := whyAnchors(rb.Output)
			if len(anchors) > 0 {
				out += " [" + strings.Join(anchors, ", ") + "]"
			}
			return "error: " + strings.ReplaceAll(out, "\n", " "), true
		}
	}
	return "", false
}

func whyAnchors(output string) []string {
	matches := causalErrorFileRe.FindAllStringSubmatch(output, whyAnchorMax)
	out := make([]string, 0, len(matches))
	for _, mm := range matches {
		if len(mm) > 1 && mm[1] != "" {
			out = append(out, mm[1])
		}
	}
	return out
}
