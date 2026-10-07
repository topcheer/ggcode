package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
)

// decision_explain.go (r103): structured decision provenance for the user.
//
// 2026 agent-explainability work (Loadout's four practical primitives:
// chain-of-thought capture, tool-call attribution, citation anchors,
// counterfactual probes) converges on one user-facing need: "why did the
// agent do X" must be answerable from recorded structure, not by having
// the model re-read and paraphrase its own transcript. ggcode already
// persists interleaved thinking blocks (#228) and full tool results in
// the message stream; this file assembles the association that was never
// surfaced: for each recent tool call, the nearest preceding thinking
// block in the same assistant message plus an error-output file:line
// anchor (reusing causalErrorFileRe's shape).
//
// Read-only, zero LLM cost, deterministic. /why [n] in the TUI renders it.

const (
	whyDefaultCount = 5
	whyMaxCount     = 20
	whyReasonRunes  = 200
	whyTargetMax    = 60
	whyAnchorMax    = 3
)

// whyInputKeys are tool-call Input keys probed (in order) to name the call's
// target — "edit_file internal/agent/agent.go" beats a bare "edit_file".
var whyInputKeys = []string{"file_path", "path", "command", "pattern", "url", "target", "name"}

// WhyCountArg parses the /why count argument with the 1..20 clamp. Extracted
// from the handler so the clamp has its own probe.
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

// ExplainDecisions renders the decision provenance of the last n assistant
// messages that contain tool calls. Empty string when the conversation has
// no such message yet (caller shows a localized hint).
//
// Pairing rule: a tool_use block is attributed to the most recent thinking
// block that precedes it within the same assistant message — exactly the
// order thinking_accumulator preserves. Compacted reasoning blocks
// ("[compacted: ...]" placeholders, context/manager.go) are shown as their
// marker rather than expanded. Result anchors come from the matching
// tool_result block (ToolID pair) in a following message; failed calls get
// file:line anchors extracted with causalErrorFileRe.
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
		_ = hasTool // idx only holds messages with >=1 tool_use
	}
	return strings.TrimRight(sb.String(), "\n")
}

// whyInputTarget extracts a short human-readable target from a tool call's
// Input JSON (first matching well-known key), truncated to whyTargetMax.
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

// whyReasoning renders the reasoning excerpt for a tool call: the compacted
// marker verbatim when compacted, else the first whyReasonRunes runes with
// newlines flattened. Explicit "<none>" line when the provider emitted no
// reasoning (command must still work there — pure tool-chain replay).
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

// whyFindResult locates the tool_result block paired with toolID (searching
// forward a bounded window) and renders a one-line outcome: ok, or the error
// text plus up to whyAnchorMax file:line anchors from the output.
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

// whyAnchors extracts file(:line) references from a failed tool output using
// causalErrorFileRe — the same shape causal attribution blames failures on.
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
