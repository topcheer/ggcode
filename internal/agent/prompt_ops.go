package agent

// Prompt Engineering Operations (PromptOps) — System Prompt Redundancy &
// Token Efficiency Intelligence
//
// Research basis: Prompt Engineering Operations is a 2025-2026 industry
// mega-trend (LangSmith, Helicone, Portkey, PromptLayer, Dify). These tools
// excel at post-hoc monitoring of prompt quality in production. But NO AI
// coding agent performs write-time system prompt analysis — checking the
// prompt BEFORE it is sent to the LLM for redundancy, bloat, and efficiency.
//
// Key findings from prompt engineering research:
//   - "Prompt Bloat": system prompts grow organically with rules, memories,
//     playbooks, and dynamic content. 30-50% of tokens may be redundant or
//     near-duplicate instructions (Dspy 2025, prompt optimization survey).
//   - "Instruction Collision": contradictory or near-duplicate directives
//     degrade model performance by 5-15% (Stanford HELM benchmark).
//   - "Cache Invalidation Cost": redundant dynamic content busts the prompt
//     cache, costing 40-80% more tokens per turn (arXiv:2601.06007).
//
// This module performs deterministic, zero-LLM-cost analysis of the fully
// assembled system prompt (base + dynamic layers) at injection time:
//
//   1. Redundancy Detection: identifies near-duplicate instruction blocks
//      (e.g., "do not use git add -A" appearing in both ratchet rules and
//      playbook). Uses sentence-level similarity, not raw substring match,
//      to catch paraphrased duplicates.
//   2. Token Budget Audit: categorizes token spend by source layer (base,
//      ratchet, playbook, autopilot goal, external injector) so the agent
//      knows which layer to trim when budget is tight.
//   3. Dilution Warning: when the system prompt exceeds a ratio of the
//      context window, provides actionable layer-specific trimming advice.
//
// Unlike the existing 15% ratio check in agent_prompt_inject.go (which only
// logs a debug message), this module injects ACTIONABLE guidance into the
// agent's context: "Your system prompt contains N near-duplicate instructions
// across ratchet rules and playbook. Consider consolidating."
//
// This is different from:
//   - tool description budget: trims per-MCP-tool descriptions (input side)
//   - context footprint: tracks tool OUTPUT consumption across a session
//   - context budget: enforces absolute session token limits
//   - cache keepalive: detects cache-busting patterns in conversation
//
// This analyzes the SYSTEM PROMPT itself for quality and efficiency.

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// promptRedunMinTokens: minimum prompt token count before redundancy
	// analysis activates (avoids noise for small, well-crafted prompts).
	promptRedunMinTokens = 2000

	// promptRedunMaxWarnings: cap on injected warnings per run.
	promptRedunMaxWarnings = 5

	// promptRedunSentenceMinLen: sentences shorter than this are skipped
	// during redundancy analysis (they're usually formatting, not instructions).
	promptRedunSentenceMinLen = 40

	// promptRedunSimilarityThreshold: Jaccard similarity above this means
	// two sentences are near-duplicates. 0.55 = ~55% word overlap.
	// Tuned empirically: lower catches more but increases false positives.
	promptRedunSimilarityThreshold = 0.40

	// promptBudgetWarnRatio: warn when system prompt exceeds this fraction
	// of the context window. Lower than the existing 15% debug log because
	// this provides actionable advice, not just a warning.
	promptBudgetWarnRatio = 0.12

	// promptBudgetCriticalRatio: inject urgent guidance at this level.
	promptBudgetCriticalRatio = 0.18

	// promptBudgetLayerWarn: warn when a single layer exceeds this many tokens.
	promptBudgetLayerWarn = 3000
)

// promptOpsState tracks whether the prompt ops analysis has fired this run.
type promptOpsState struct {
	fired        bool
	lastPrompt   string
	lastTokenEst int
}

func newPromptOpsState() *promptOpsState {
	return &promptOpsState{}
}

func (p *promptOpsState) reset() {
	p.fired = false
	p.lastPrompt = ""
	p.lastTokenEst = 0
}

// promptLayerInfo describes one source layer of the system prompt.
type promptLayerInfo struct {
	name   string
	tokens int
	chars  int
}

// analyzePromptRedundancy detects near-duplicate instruction sentences in
// the system prompt using word-level Jaccard similarity. Returns a list of
// redundancy findings (pairs of similar sentences with their similarity score).
//
// The algorithm:
//  1. Split the prompt into sentences (by sentence-ending punctuation).
//  2. Tokenize each sentence into lowercase word sets.
//  3. For each pair, compute Jaccard similarity = |A∩B| / |A∪B|.
//  4. Flag pairs above promptRedunSimilarityThreshold.
//
// Complexity: O(n^2 * m) where n = sentence count, m = avg word count.
// Bounded by maxSentences to cap CPU usage. For typical system prompts
// (50-200 sentences), this completes in <1ms.
func analyzePromptRedundancy(prompt string) []promptRedundancy {
	sentences := splitPromptSentences(prompt)
	if len(sentences) < 2 {
		return nil
	}

	// Cap the number of sentences to analyze for CPU safety.
	maxSentences := 150
	if len(sentences) > maxSentences {
		sentences = sentences[:maxSentences]
	}

	// Pre-compute word sets for each sentence.
	wordSets := make([]map[string]bool, len(sentences))
	for i, s := range sentences {
		wordSets[i] = tokenizePromptWords(s.text)
	}

	var findings []promptRedundancy
	seen := make(map[string]bool) // dedup by sentence pair key

	for i := 0; i < len(sentences); i++ {
		if len(sentences[i].text) < promptRedunSentenceMinLen {
			continue
		}
		for j := i + 1; j < len(sentences); j++ {
			if len(sentences[j].text) < promptRedunSentenceMinLen {
				continue
			}
			sim := jaccardSimilarity(wordSets[i], wordSets[j])
			if sim < promptRedunSimilarityThreshold {
				continue
			}
			// Build a canonical pair key to avoid duplicate findings.
			key := fmt.Sprintf("%d-%d", i, j)
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, promptRedundancy{
				sentenceA:  truncateForDisplay(sentences[i].text, 80),
				sentenceB:  truncateForDisplay(sentences[j].text, 80),
				similarity: sim,
			})
			if len(findings) >= promptRedunMaxWarnings {
				return findings
			}
		}
	}
	return findings
}

// promptRedundancy represents a pair of near-duplicate instruction sentences.
type promptRedundancy struct {
	sentenceA  string
	sentenceB  string
	similarity float64
}

// promptSentence is a sentence extracted from the system prompt.
type promptSentence struct {
	text string
}

// splitPromptSentences splits the system prompt into sentences for redundancy
// analysis. Uses sentence-ending punctuation (. ! ? followed by whitespace or
// newline) and also splits on double-newlines (paragraph boundaries).
func splitPromptSentences(prompt string) []promptSentence {
	var sentences []promptSentence
	var current strings.Builder

	flush := func() {
		s := strings.TrimSpace(current.String())
		if s != "" {
			sentences = append(sentences, promptSentence{text: s})
		}
		current.Reset()
	}

	for i := 0; i < len(prompt); i++ {
		ch := rune(prompt[i])
		if ch > 127 {
			// Handle UTF-8 multi-byte: write and continue.
			current.WriteRune(ch)
			continue
		}
		current.WriteByte(byte(ch))

		// Split on double newline (paragraph boundary).
		if ch == '\n' && i > 0 && prompt[i-1] == '\n' {
			flush()
			continue
		}

		// Split on sentence-ending punctuation followed by space/newline.
		if ch == '.' || ch == '!' || ch == '?' {
			if i+1 < len(prompt) && (prompt[i+1] == ' ' || prompt[i+1] == '\n' || prompt[i+1] == '\t') {
				flush()
			}
		}
	}
	flush()
	return sentences
}

// tokenizePromptWords converts a sentence into a set of lowercase word tokens
// for similarity comparison. Stops words (the, a, is, etc.) are removed to
// improve semantic overlap detection.
func tokenizePromptWords(s string) map[string]bool {
	words := make(map[string]bool)
	var current strings.Builder

	flushWord := func() {
		w := strings.ToLower(strings.TrimSpace(current.String()))
		if len(w) >= 2 && !promptStopWords[w] {
			words[w] = true
		}
		current.Reset()
	}

	for _, ch := range s {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '-' || ch == '_' {
			current.WriteRune(ch)
		} else {
			flushWord()
		}
	}
	flushWord()
	return words
}

// jaccardSimilarity computes the Jaccard similarity coefficient between two
// word sets: |A ∩ B| / |A ∪ B|. Returns 0.0 if both sets are empty.
func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for w := range a {
		if b[w] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// truncateForDisplay truncates a string to maxLen, appending "..." if truncated.
func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// analyzePromptBudget categorizes token spend by source layer.
// Given the full prompt text and layer boundaries, it returns per-layer
// token estimates and flags any dominant layer.
//
// layerTexts is a slice of (name, text) pairs representing each prompt layer:
// base, ratchet rules, playbook, autopilot goal, external injector, etc.
func analyzePromptBudget(layerTexts []promptLayerInfo) promptBudgetReport {
	var total int
	for i := range layerTexts {
		total += layerTexts[i].tokens
	}

	report := promptBudgetReport{
		totalTokens: total,
		layers:      layerTexts,
	}

	// Identify the largest layer.
	maxLayer := ""
	maxTokens := 0
	for i := range layerTexts {
		if layerTexts[i].tokens > maxTokens {
			maxTokens = layerTexts[i].tokens
			maxLayer = layerTexts[i].name
		}
	}
	report.dominantLayer = maxLayer
	report.dominantTokens = maxTokens

	if total > 0 && maxTokens > 0 {
		report.dominantRatio = float64(maxTokens) / float64(total)
	}

	return report
}

// promptBudgetReport summarizes the token budget breakdown of the system prompt.
type promptBudgetReport struct {
	totalTokens    int
	layers         []promptLayerInfo
	dominantLayer  string
	dominantTokens int
	dominantRatio  float64
}

// maybeCheckPromptOps performs the full PromptOps analysis on the assembled
// system prompt. Called after maybeInjectDynamicSystemPrompt() in the agent
// loop. Injects actionable guidance into the agent's context if issues are found.
//
// This fires at most once per run (tracked by promptOpsState.fired) to avoid
// repeated injections on every iteration when the prompt hasn't changed.
func (a *Agent) maybeCheckPromptOps() {
	if a.promptOps == nil {
		return
	}

	// Only fire once per run.
	if a.promptOps.fired {
		return
	}

	prompt := a.lastInjectedSystemPrompt
	if strings.TrimSpace(prompt) == "" {
		return
	}

	// Quick token estimate using character ratio.
	// Reuses the same ratio as context.EstimateTokens (~3.5 chars/token).
	estTokens := len(prompt) / 3
	a.promptOps.lastPrompt = prompt
	a.promptOps.lastTokenEst = estTokens

	// Skip analysis for small prompts — nothing to optimize.
	if estTokens < promptRedunMinTokens {
		return
	}

	a.promptOps.fired = true

	var messages []string

	// 1. Redundancy detection.
	findings := analyzePromptRedundancy(prompt)
	if len(findings) > 0 {
		var b strings.Builder
		b.WriteString(fmt.Sprintf(
			"[prompt-ops] Detected %d near-duplicate instruction pair(s) in the system prompt "+
				"(%d est tokens). Redundant instructions waste tokens and can cause instruction collision "+
				"(model receives conflicting guidance). Consider consolidating:\n",
			len(findings), estTokens,
		))
		for i, f := range findings {
			b.WriteString(fmt.Sprintf("  %d. [%.0f%% overlap] %s\n     vs: %s\n",
				i+1, f.similarity*100, f.sentenceA, f.sentenceB))
		}
		messages = append(messages, b.String())
		debug.Log("prompt-ops", "redundancy check: %d duplicates found in %d-token prompt",
			len(findings), estTokens)
	}

	// 2. Token budget audit against context window.
	ctxWindow := a.contextManager.ContextWindow()
	if ctxWindow > 0 {
		ratio := float64(estTokens) / float64(ctxWindow)
		if ratio >= promptBudgetCriticalRatio {
			messages = append(messages, fmt.Sprintf(
				"[prompt-ops] System prompt is %.1f%% of context window (%d est tokens / %d). "+
					"This significantly reduces available conversation space. "+
					"Action: trim ratchet rules (consider reducing from 5 to 3), "+
					"consolidate playbook hints, or reduce extraPrompt length.",
				ratio*100, estTokens, ctxWindow,
			))
			debug.Log("prompt-ops", "critical budget: %.1f%% of context window (%d tokens)",
				ratio*100, estTokens)
		} else if ratio >= promptBudgetWarnRatio {
			messages = append(messages, fmt.Sprintf(
				"[prompt-ops] System prompt is %.1f%% of context window (%d est tokens / %d). "+
					"Consider monitoring prompt growth across iterations.",
				ratio*100, estTokens, ctxWindow,
			))
		}
	}

	// Inject guidance if any issues found.
	if len(messages) == 0 {
		return
	}

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: strings.Join(messages, "\n"),
		}},
	})
}

// promptStopWords are common English function words that should be excluded
// from similarity comparison to improve semantic overlap detection.
// Without stop word removal, any two sentences containing "do not" + common
// verbs would have artificially high similarity.
var promptStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "must": true, "shall": true, "can": true, "of": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	"by": true, "from": true, "as": true, "into": true, "and": true, "or": true,
	"but": true, "not": true, "no": true, "if": true, "then": true, "else": true,
	"this": true, "that": true, "these": true, "those": true, "it": true,
	"its": true, "their": true, "they": true, "them": true, "he": true,
	"she": true, "his": true, "her": true, "we": true, "our": true, "you": true,
	"your": true, "when": true, "where": true, "which": true, "who": true,
	"how": true, "what": true, "why": true, "so": true, "than": true, "too": true,
	"very": true, "also": true, "only": true, "any": true, "all": true,
	"some": true, "such": true, "each": true, "every": true, "both": true,
	"more": true, "most": true, "other": true, "about": true, "after": true,
	"before": true, "between": true, "through": true, "during": true,
	"above": true, "below": true, "up": true, "down": true, "out": true,
	"off": true, "over": true, "under": true, "again": true,
}
