package agent

// claim_citation.go (sa-114): claim-level evidence citation for the FINAL
// assistant answer.
//
// /why (decision_explain.go, r103) attributes tool calls to reasoning —
// "why did the agent run X". This file answers the complementary question
// the 2026 agent-trust UX literature puts first: "which recorded evidence
// backs each claim in the answer the user just read?". The final reply
// text is scanned deterministically for code anchors (backticked symbols,
// file.ext(:line) references); each anchor is linked to the most recent
// tool call whose input or result output contains it. No LLM, no re-ask:
// the user verifies "the root cause is agent.go:42" against the actual
// tool output that produced that statement, one /evidence away.
//
// Read-only, deterministic, bounded scan windows.

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	citeMaxAnchors    = 12
	citeClaimRunes    = 80
	citeEvidenceRunes = 200
	citeResultScanCap = 4096
	citeInputScanCap  = 2048
)

// citeFileRe matches file.ext and file.ext:line references in prose —
// deliberately looser than causalErrorFileRe (which requires the trailing
// error-output shape); an answer cites files without error colons too.
var citeFileRe = regexp.MustCompile(`(?:^|[\s"'(])([\w\-./\\]+\.(?:go|ts|tsx|js|jsx|mjs|py|rs|java|rb|kt|swift|c|cc|cpp|h|hpp|md|yaml|yml|json|toml)(?::\d+)?)`)

// citeBacktickRe extracts short code-shaped spans from backticks: must
// contain a code-ish character (dot, slash, underscore, colon) so plain
// quoted words do not become anchors.
// citeCodeShapeRe accepts code-shaped spans: a code separator character
// OR camelCase (streamChatResponse carries no separator but is a symbol).
var citeBacktickRe = regexp.MustCompile("`([^`\n]{2,120})`")
var citeCodeShapeRe = regexp.MustCompile(`(?:[./_:(]|[a-z][A-Z])`)
var citeLineNoRe = regexp.MustCompile(`:\d+$`)

// ClaimCitation links one anchor from the final answer to the tool
// evidence that supports it.
type ClaimCitation struct {
	Anchor   string `json:"anchor"`   // `HandleMsg` / internal/agent/agent.go:42
	Claim    string `json:"claim"`    // answer excerpt around the anchor
	Tool     string `json:"tool"`     // tool_use name producing the evidence
	ToolID   string `json:"toolId"`   // paired tool_use block ID
	Evidence string `json:"evidence"` // tool_result excerpt containing the anchor
}

// EvidenceCitations scans the last text-only assistant message for code
// anchors and links each to the most recent supporting tool call. Returns
// nil when the conversation has no final answer yet or no anchor finds
// recorded evidence (unsupported claims are simply not listed — absence
// of a citation is itself the signal, mirroring citation-based UIs: only
// verifiable statements carry links).
func EvidenceCitations(msgs []provider.Message) []ClaimCitation {
	ansIdx, answer := finalAnswer(msgs)
	if ansIdx < 0 || answer == "" {
		return nil
	}
	anchors := extractAnchors(answer)
	if len(anchors) == 0 {
		return nil
	}

	// Collect tool_use blocks newest-first, each with the paired result
	// output (searched forward, same bounded window /why uses).
	type call struct {
		tool, toolID, input, result string
	}
	var calls []call
	for i := ansIdx - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue // user tool_result messages interleave the tool turns
		}
		m := &msgs[i]
		for bi := len(m.Content) - 1; bi >= 0; bi-- {
			b := &m.Content[bi]
			if b.Type != "tool_use" {
				continue
			}
			c := call{tool: b.ToolName, toolID: b.ToolID, input: clamp(string(b.Input), citeInputScanCap)}
			if out, ok := findResultOutput(msgs, i, b.ToolID); ok {
				c.result = clamp(out, citeResultScanCap)
			}
			calls = append(calls, c)
		}
	}

	var out []ClaimCitation
	for _, a := range anchors {
		for _, c := range calls { // newest-first: nearest evidence wins
			if c.toolID == "" {
				continue
			}
			if !anchorHit(c.input, c.result, a) {
				continue
			}
			out = append(out, ClaimCitation{
				Anchor:   a,
				Claim:    claimExcerpt(answer, a, citeClaimRunes),
				Tool:     c.tool,
				ToolID:   c.toolID,
				Evidence: evidenceExcerpt(c.result, a, citeEvidenceRunes),
			})
			break
		}
		if len(out) >= citeMaxAnchors {
			break
		}
	}
	return out
}

// finalAnswer returns the index and concatenated text of the last
// assistant message that carries text but no tool_use — the reply the
// user actually read.
func finalAnswer(msgs []provider.Message) (int, string) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		var sb strings.Builder
		pure := true
		for _, b := range msgs[i].Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
			if b.Type == "tool_use" {
				pure = false
			}
		}
		if pure && sb.Len() > 0 {
			return i, sb.String()
		}
	}
	return -1, ""
}

// anchorHit reports whether a tool call's input or result output backs
// the anchor. A file.go:123 anchor also hits on the bare file.go form:
// read inputs carry paths without line numbers, and read outputs carry
// "123:" line prefixes rather than "file.go:123".
func anchorHit(input, result, anchor string) bool {
	if strings.Contains(input, anchor) || strings.Contains(result, anchor) {
		return true
	}
	if citeLineNoRe.MatchString(anchor) {
		bare := citeLineNoRe.ReplaceAllString(anchor, "")
		if bare != anchor && (strings.Contains(input, bare) || strings.Contains(result, bare)) {
			return true
		}
	}
	return false
}

// extractAnchors pulls backticked code spans first (highest precision),
// then bare file(:line) references, deduped in first-seen order.
func extractAnchors(answer string) []string {
	bt := citeBacktickRe.FindAllStringSubmatch(answer, -1)
	fl := citeFileRe.FindAllStringSubmatch(answer, -1)
	raw := make([]string, 0, len(bt)+len(fl))
	for _, mm := range bt {
		if citeCodeShapeRe.MatchString(mm[1]) {
			raw = append(raw, mm[1])
		}
	}
	for _, mm := range fl {
		raw = append(raw, mm[1])
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, a := range raw {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// findResultOutput returns the raw output of the tool_result paired with
// toolID (same forward-window discipline as whyFindResult).
func findResultOutput(msgs []provider.Message, from int, toolID string) (string, bool) {
	if toolID == "" {
		return "", false
	}
	for j := from + 1; j < len(msgs) && j <= from+3; j++ {
		for _, rb := range msgs[j].Content {
			if rb.Type == "tool_result" && rb.ToolID == toolID {
				return rb.Output, true
			}
		}
	}
	return "", false
}

// claimExcerpt renders the answer around the anchor's first occurrence.
func claimExcerpt(answer, anchor string, max int) string {
	pos := strings.Index(answer, anchor)
	if pos < 0 {
		return clamp(anchor, max)
	}
	start := pos - max/3
	if start < 0 {
		start = 0
	}
	seg := []rune(answer[start:])
	if len(seg) > max {
		seg = seg[:max]
	}
	s := strings.TrimSpace(string(seg))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

// evidenceExcerpt centers the anchor occurrence inside the tool output.
func evidenceExcerpt(result, anchor string, max int) string {
	if result == "" {
		return "<no recorded output>"
	}
	pos := strings.Index(result, anchor)
	if pos < 0 {
		return clamp(result, max)
	}
	start := pos - max/3
	if start < 0 {
		start = 0
	}
	seg := []rune(result[start:])
	if len(seg) > max {
		seg = seg[:max]
	}
	s := strings.ReplaceAll(string(seg), "\n", " ⏎ ")
	return s + "…"
}

func clamp(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "…"
}

// FormatCitations renders the /evidence output block.
func FormatCitations(cites []ClaimCitation) string {
	if len(cites) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d citation(s) — claims in the last answer linked to recorded tool evidence:\n", len(cites)))
	for i, c := range cites {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, c.Claim))
		sb.WriteString(fmt.Sprintf("     ← %s: %s\n", c.Tool, c.Evidence))
	}
	return strings.TrimRight(sb.String(), "\n")
}
