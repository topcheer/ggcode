package knight

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// SessionAnalyzer analyzes session history to discover reusable patterns.
type SessionAnalyzer struct {
	store             session.Store
	budget            *Budget
	knight            *Knight
	projDir           string
	normalizedProj    string // workspace-normalized project path for session filtering
	filterByWorkspace bool   // false in test environments with tmp dirs
}

// NewSessionAnalyzer creates a session analyzer.
func NewSessionAnalyzer(k *Knight) *SessionAnalyzer {
	normalized := session.NormalizeWorkspacePath(k.projDir)
	return &SessionAnalyzer{
		store:             k.store,
		budget:            k.budget,
		knight:            k,
		projDir:           k.projDir,
		normalizedProj:    normalized,
		filterByWorkspace: normalized != "" && !isTempDir(normalized),
	}
}

// isTempDir returns true if the path looks like a temp directory (test scenario).
func isTempDir(path string) bool {
	base := strings.ToLower(filepath.Base(path))

	// Cross-platform temp directory detection
	// Normalize path separators for consistent checking
	normalized := filepath.ToSlash(path)

	// Unix/macOS temp paths
	if strings.Contains(normalized, "/tmp/") || strings.HasPrefix(normalized, "/tmp/") ||
		strings.Contains(normalized, "/var/folders/") {
		return true
	}

	// Windows temp paths
	if strings.Contains(normalized, "/temp/") || strings.Contains(normalized, "/tmp/") {
		return true
	}

	// Check against os.TempDir() (respects $TMPDIR, %TEMP%, etc.)
	if tempDir := os.TempDir(); tempDir != "" {
		// Compare normalized paths
		normalizedTemp := filepath.ToSlash(tempDir)
		if strings.HasPrefix(normalized, normalizedTemp+"/") || normalized == normalizedTemp {
			return true
		}
	}

	// Name-based heuristics
	return strings.HasPrefix(base, "knight-") || strings.HasPrefix(base, "test-") ||
		strings.Contains(base, "tmp")
}

// AnalysisResult holds the outcome of a session analysis.
type AnalysisResult struct {
	SessionsAnalyzed int
	SkillCandidates  []SkillCandidate
}

// SkillCandidate is a potential skill discovered from session analysis.
type SkillCandidate struct {
	Name           string
	Description    string
	Scope          string // "global" or "project"
	Score          float64
	Reason         string
	EvidenceCount  int
	SourceSessions []string
	// Evidence holds the key conversation excerpts that justify this skill.
	// For corrections: [0]=what AI did wrong, [1]=what user said instead.
	// For failure-fix: [0]=error message, [1]=how it was fixed.
	Evidence []string
	// Category classifies the skill source for LLM context.
	Category string // "correction", "failure-fix", "convention"
	// GenFailCount tracks consecutive LLM generation failures; abandon after knightMaxGenFailures.
	GenFailCount int `json:",omitempty"`
	// Queue metadata is persisted for deferred candidates so Knight can make
	// less myopic prioritization decisions across later analysis ticks.
	FirstQueuedAt       time.Time `json:"first_queued_at,omitempty"`
	LastQueuedAt        time.Time `json:"last_queued_at,omitempty"`
	QueueTouchCount     int       `json:"queue_touch_count,omitempty"`
	QueuePriority       float64   `json:"queue_priority,omitempty"`
	QueuePriorityReason string    `json:"queue_priority_reason,omitempty"`
}

// AnalyzeRecent scans recent sessions for high-value learning signals.
//
// Filtering and priority:
//  1. Only sessions matching the current project workspace are analyzed.
//  2. Sessions already analyzed in previous ticks are skipped (dedup).
//  3. Sessions are prioritized: corrections/failure-fixes first, then recency.
//  4. Up to `limit` sessions are analyzed per tick.
func (sa *SessionAnalyzer) AnalyzeRecent(ctx context.Context) (*AnalysisResult, error) {
	if sa.store == nil {
		return nil, fmt.Errorf("session store not available")
	}

	sessions, err := sa.store.List()
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	// Filter: only current project's sessions, not yet analyzed (or updated since last analysis).
	var eligible []*session.Session
	for _, s := range sessions {
		sa.knight.mu.Lock()
		lastAnalyzed, alreadyAnalyzed := sa.knight.analyzedSessions[s.ID]
		sa.knight.mu.Unlock()
		if alreadyAnalyzed {
			// Already analyzed — skip unless the session has new activity since then
			if !s.UpdatedAt.After(lastAnalyzed) {
				continue
			}
		}
		// Filter by workspace: skip sessions from different projects.
		// Only filter when running in a real project (not tests).
		// Sessions with empty workspace are always included (legacy data).
		if sa.filterByWorkspace && s.Workspace != "" && s.Workspace != sa.normalizedProj {
			continue
		}
		// Load full session to check eligibility
		full, loadErr := sa.store.Load(s.ID)
		if loadErr != nil {
			debug.Log("knight", "cannot load session %s: %v", s.ID, loadErr)
			continue
		}
		if len(full.Messages) < 4 {
			continue
		}
		eligible = append(eligible, full)
	}

	// Priority sort: sessions with correction/failure signals first.
	sort.SliceStable(eligible, func(i, j int) bool {
		si := sa.sessionSignalScore(eligible[i])
		sj := sa.sessionSignalScore(eligible[j])
		if si != sj {
			return si > sj
		}
		return eligible[i].UpdatedAt.After(eligible[j].UpdatedAt)
	})

	result := &AnalysisResult{}
	aggregated := make(map[string]*candidateAggregate)

	limit := 10
	if len(eligible) < limit {
		limit = len(eligible)
	}

	for i := 0; i < limit; i++ {
		candidates := sa.analyzeSession(eligible[i])
		for _, candidate := range candidates {
			aggregateCandidate(aggregated, candidate, eligible[i].ID)
		}
		sa.knight.mu.Lock()
		sa.knight.analyzedSessions[eligible[i].ID] = time.Now()
		sa.knight.mu.Unlock()
		result.SessionsAnalyzed++
	}
	result.SkillCandidates = finalizeCandidates(aggregated)

	debug.Log("knight", "session analysis: %d eligible (of %d total), %d analyzed, %d candidates",
		len(eligible), len(sessions), result.SessionsAnalyzed, len(result.SkillCandidates))

	// Trim dedup set to prevent unbounded growth.
	// Keep only IDs that appear in the current session list.
	sa.knight.mu.Lock()
	if len(sa.knight.analyzedSessions) > 1000 {
		current := make(map[string]time.Time, len(sessions))
		for _, s := range sessions {
			if t, ok := sa.knight.analyzedSessions[s.ID]; ok {
				current[s.ID] = t
			}
		}
		sa.knight.analyzedSessions = current
	}
	sa.knight.mu.Unlock()

	return result, nil
}

// sessionSignalScore estimates how likely a session contains learning signals.
// Higher score = higher priority for analysis.
func (sa *SessionAnalyzer) sessionSignalScore(ses *session.Session) int {
	score := 0
	textLower := ""
	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "text" {
				textLower += strings.ToLower(block.Text) + " "
			}
			if block.Type == "tool_result" && block.IsError {
				score += 3 // error tool results are strong signals
			}
		}
	}
	// Check for correction signals in user messages
	for _, signal := range correctionSignals {
		if strings.Contains(textLower, strings.ToLower(signal)) {
			score += 5
			break
		}
	}
	return score
}

// analyzeSession runs analysis heuristics on a single session.
func (sa *SessionAnalyzer) analyzeSession(ses *session.Session) []SkillCandidate {
	if len(ses.Messages) < 4 {
		return nil // too short to be interesting
	}

	var all []SkillCandidate

	// 1. User corrections — the highest-value learning signal
	all = append(all, sa.detectCorrections(ses)...)

	// 2. Failure → fix pairs (tool error followed by successful correction)
	all = append(all, sa.detectFailureFixes(ses)...)

	return all
}

// --- Heuristic 1: User corrections ---
//
// When a user says "不对/错了/wrong/不要" they are teaching us something.
// We capture what the AI did wrong and what the user said instead.
// These become behavioral guidelines (e.g. "always check code before concluding").

var correctionSignals = []string{
	"不要", "别用", "不应该", "不对", "错了", "应该是", "改用", "换成",
	"而不是", "不是什么", "你需要", "为什么你不会", "为什么你不", "不要没搞清楚",
	"先看", "看逻辑", "你看一下", "看一下逻辑",
	"wrong", "don't", "should be", "instead", "no,", "not that", "use x.",
	"incorrect", "fix it", "different", "look at the", "read the code",
	"not what i", "that's not", "the right way",
}

func (sa *SessionAnalyzer) detectCorrections(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

	for i, msg := range ses.Messages {
		if msg.Role != "user" || i == 0 {
			continue
		}

		text := extractText(msg.Content)
		if text == "" {
			continue
		}

		textLower := strings.ToLower(text)
		matched := false
		for _, signal := range correctionSignals {
			if strings.Contains(textLower, strings.ToLower(signal)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// Found a correction — extract context from the preceding assistant message
		prev := findPrevAssistant(ses.Messages, i)
		if prev == nil {
			continue
		}

		prevText := extractText(prev.Content)

		// Only create a candidate if the user message has substantive content
		// (not just "no" or "wrong" but explains WHY or WHAT to do instead)
		if len([]rune(text)) < 10 {
			continue
		}

		name := buildCorrectionSkillName(text)
		if !isValidCandidateName(name) {
			continue
		}
		evidence := []string{
			fmt.Sprintf("AI did: %s", truncate(prevText, 300)),
			fmt.Sprintf("User corrected: %s", truncate(text, 500)),
		}

		desc := truncate(text, 120)

		candidates = append(candidates, SkillCandidate{
			Name:        name,
			Description: desc,
			Scope:       "", // scope decided by LLM during generation
			Score:       2.0,
			Reason:      fmt.Sprintf("user correction in session %s", ses.ID),
			Evidence:    evidence,
			Category:    "correction",
		})
	}

	return candidates
}

// --- Heuristic 2: Failure → fix chains ---
//
// When a tool call fails (is_error=true) and then the AI or user corrects
// the approach and succeeds, the failure reason + fix method is a learnable pattern.
// We look at the ACTUAL error and the fix, not just tool names.

func (sa *SessionAnalyzer) detectFailureFixes(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

	// Build maps: toolID → toolName, toolID → input, toolID → output
	type toolInfo struct {
		name  string
		input string
	}
	toolMap := make(map[string]toolInfo)
	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" && block.ToolID != "" {
				input := ""
				if block.Input != nil {
					if b, err := json.Marshal(block.Input); err == nil {
						input = string(b)
					}
				}
				toolMap[block.ToolID] = toolInfo{name: block.ToolName, input: truncate(input, 300)}
			}
		}
	}

	// Find error tool_results, then look for the fix in subsequent messages
	var failures []failure
	matched := make(map[string]bool) // toolName → already matched

	for i, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_result" && block.IsError {
				info, ok := toolMap[block.ToolID]
				if !ok {
					continue
				}
				failures = append(failures, failure{
					toolName: info.name,
					toolInp:  info.input,
					errMsg:   truncate(block.Output, 300),
					index:    i,
				})
			}
		}
	}

	for _, f := range failures {
		if matched[f.toolName] {
			continue
		}

		// Look for a successful result of the same tool within 6 messages
		// with at least one assistant message in between (the fix attempt)
		for j := f.index + 1; j < len(ses.Messages) && j <= f.index+6; j++ {
			hasAssistantBetween := false
			for k := f.index + 1; k < j; k++ {
				if ses.Messages[k].Role == "assistant" {
					hasAssistantBetween = true
					break
				}
			}
			if !hasAssistantBetween {
				continue
			}

			for _, block := range ses.Messages[j].Content {
				if block.Type == "tool_result" && !block.IsError {
					info, ok := toolMap[block.ToolID]
					if !ok || info.name != f.toolName {
						continue
					}

					// Extract what the assistant did to fix it
					var fixDesc string
					for k := f.index + 1; k < j; k++ {
						if ses.Messages[k].Role == "assistant" {
							fixDesc = truncate(extractText(ses.Messages[k].Content), 300)
							break
						}
					}

					name := buildFailureFixName(f)
					evidence := []string{
						fmt.Sprintf("Error: %s (input: %s)", truncate(f.errMsg, 200), truncate(f.toolInp, 100)),
						fmt.Sprintf("Fix: %s", fixDesc),
					}

					candidates = append(candidates, SkillCandidate{
						Name:        name,
						Description: fmt.Sprintf("Fix for %s error: %s", f.toolName, truncate(f.errMsg, 80)),
						Scope:       "", // scope decided by LLM during generation
						Score:       1.5,
						Reason:      fmt.Sprintf("failure-fix in session %s", ses.ID),
						Evidence:    evidence,
						Category:    "failure-fix",
					})
					matched[f.toolName] = true
					break
				}
			}
			if matched[f.toolName] {
				break
			}
		}
	}

	return candidates
}

// --- Project convention scanning ---

// CollectProjectConventions reads well-known project config files for context.
func (sa *SessionAnalyzer) CollectProjectConventions() string {
	var conventions []string

	files := []string{
		"CLAUDE.md", "COPILOT.md", ".editorconfig",
		"Makefile", "go.mod", "package.json",
		"pyproject.toml", "Cargo.toml", "tsconfig.json",
		".eslintrc.json", ".eslintrc.js",
	}

	for _, f := range files {
		path := filepath.Join(sa.projDir, f)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if len([]rune(content)) > 2000 {
			content = string([]rune(content)[:2000]) + "\n... (truncated)"
		}
		conventions = append(conventions, fmt.Sprintf("=== %s ===\n%s", f, content))
	}

	return strings.Join(conventions, "\n\n")
}

// --- LLM skill generation ---

// GenerateSkillFromAnalysis uses LLM to create a skill from a high-value candidate.
// The skill is a behavioral guideline that teaches the AI what to do (or not do)
// based on real user interactions.
func (sa *SessionAnalyzer) GenerateSkillFromAnalysis(ctx context.Context, candidate SkillCandidate, factory AgentFactory) (string, error) {
	conventions := sa.CollectProjectConventions()
	conventionsSection := ""
	if conventions != "" {
		conventionsSection = fmt.Sprintf("\n\n## Project conventions\n%s", conventions)
	}

	evidenceSection := ""
	if len(candidate.Evidence) > 0 {
		evidenceSection = fmt.Sprintf("\n\n## Evidence from conversations\n%s", strings.Join(candidate.Evidence, "\n"))
	}

	memorySection := ""
	if sa.knight != nil {
		if mem := sa.knight.formatRecentSemanticMemoryForEval(8); mem != "" {
			memorySection = fmt.Sprintf("\n\n## Prior Knight lessons (avoid duplicating these)\n%s", mem)
		}
	}

	prompt := fmt.Sprintf(`CRITICAL: Your FINAL text output must be a SKILL.md document starting with the line --- (YAML frontmatter). Do NOT output analysis, summaries, or explanations as your final message. Only the skill document.

You are generating a BEHAVIORAL SKILL — a reusable guideline that teaches an AI coding assistant how to act based on real user interactions.

## What happened
Category: %s
%s

## Your task
Create a concise, actionable skill document. This is NOT a tutorial — it's a RULE that the AI should follow.
Think of it as adding a paragraph to the project's AGENTS.md based on what was learned.

Examples of good skills:
- "bool-default-trap": In Go, bool zero-value is false. When a config field should default to true, use a separate `+"`"+`enabledSet bool`+"`"+` field to distinguish "not set" from "explicitly disabled".
- "build-only-official-binary": Only use `+"`"+`make build`+"`"+` → `+"`"+`bin/ggcode`+"`"+`. The restart workflow expects this exact path. Never create debug variants.
- "read-code-before-concluding": When investigating runtime behavior, read the relevant source code first. Don't guess at file paths or log locations — find the constants and functions that define them.

## Source sessions (read these to understand context)
%s

## Instructions
1. Use read_file to examine 1-2 of the source sessions under ~/.ggcode/sessions/ — focus on the conversation around the evidence above
2. Extract the CORE LESSON — what should the AI do differently next time?
3. Write the skill document below

Create a SKILL.md document with this EXACT structure:

---
name: "%s"
description: "<one-line actionable rule>"
scope: "<DECIDE: default to 'project'. Use 'global' ONLY if the rule references nothing project-specific (no project basename, no internal/cmd/pkg paths, no project-specific make targets or scripts) AND would genuinely apply unchanged to any unrelated codebase. When in doubt, choose 'project'.>"
platforms: ["darwin", "linux", "windows"]
created_by: "knight"
---

# %s

<1-2 sentence summary of the rule>

## Rule

<The core behavioral rule — what to do or not do, and WHY>

## Steps

1. **Step one**: <specific action>
2. **Step two**: <specific action>

## When to Apply

- <specific situations where this rule applies>

## Examples

<Concrete example from the real interaction showing wrong vs right approach>

## Anti-Patterns

- <what NOT to do and why>

RULES:
- Output ONLY the skill document content, starting with ---
- Do NOT output any analysis, reasoning, or summary before or after the skill document
- Your ENTIRE final text output must be the skill document from --- to the end
- The "## Steps" heading is REQUIRED (exact text)
- Each step must be specific and actionable — not "analyze the situation" but "read the function at line N of file X"
- The Examples section must include REAL details from the session, not generic placeholders
- All YAML string values containing colons or quotes MUST be quoted%s%s%s`,
		candidate.Category, candidate.Reason,
		strings.Join(candidate.SourceSessions, ", "),
		candidate.Name, candidate.Name,
		conventionsSection, evidenceSection, memorySection)

	result := sa.knight.RunTaskWithTurns(ctx, "skill-generation", prompt, factory, 50)
	if result.Error != nil {
		return "", result.Error
	}

	output := strings.TrimSpace(result.Output)
	output = extractSkillDocument(output)
	if output == "" {
		return "", fmt.Errorf("LLM output does not contain valid YAML frontmatter (expected ---): %s", truncate(result.Output, 200))
	}

	return output, nil
}

// extractSkillDocument extracts a YAML frontmatter skill document from LLM output.
// Handles cases where the LLM prepends analysis or appends commentary.
// Returns empty string if no valid skill document is found.
func extractSkillDocument(output string) string {
	// Try the most specific pattern first: "---\nname:" signature
	if idx := strings.LastIndex(output, "---\nname:"); idx >= 0 {
		return strings.TrimSpace(output[idx:])
	}
	if idx := strings.LastIndex(output, "---\r\nname:"); idx >= 0 {
		return strings.TrimSpace(output[idx:])
	}
	// Fallback: any "---" not at position 0
	if idx := strings.Index(output, "---"); idx > 0 {
		return strings.TrimSpace(output[idx:])
	}
	// Already starts with ---
	if strings.HasPrefix(output, "---") {
		return output
	}
	return ""
}

// --- Helpers ---

func extractText(blocks []provider.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

func findPrevAssistant(messages []provider.Message, beforeIdx int) *provider.Message {
	for i := beforeIdx - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return &messages[i]
		}
	}
	return nil
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func sanitizeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, s)
	// Collapse consecutive dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// isValidCandidateName rejects garbage names that carry no semantic meaning.
func isValidCandidateName(name string) bool {
	if len(name) < 8 {
		return false
	}
	// Pure fallback patterns like "correction-123"
	if strings.HasPrefix(name, "correction-") {
		rest := strings.TrimPrefix(name, "correction-")
		if _, err := strconv.Atoi(rest); err == nil {
			return false
		}
	}
	// Check that at least half the characters are alphanumeric (not dashes)
	alnum := 0
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			alnum++
		}
	}
	return alnum >= len(name)/2
}

func buildCorrectionSkillName(text string) string {
	textLower := strings.ToLower(text)

	// Try to infer a meaningful name from the correction content
	keywords := []struct {
		keyword string
		name    string
	}{
		{"看逻辑", "read-code-before-concluding"},
		{"先看", "read-code-first"},
		{"搞清楚", "understand-before-acting"},
		{"不要自作主张", "confirm-before-acting"},
		{"编译", "build-convention"},
		{"二进制", "binary-convention"},
		{"debug", "no-debug-binary"},
		{"默认值", "default-value-check"},
		{"bool", "bool-zero-value"},
		{"为什么你不会", "read-code-before-concluding"},
		{"read the code", "read-code-first"},
		{"look at", "look-before-acting"},
		{"don't guess", "dont-guess-read-code"},
	}

	for _, kw := range keywords {
		if strings.Contains(textLower, kw.keyword) {
			return kw.name
		}
	}

	// Fallback: derive from first meaningful words
	words := strings.Fields(textLower)
	var parts []string
	for _, w := range words {
		w = strings.Trim(w, "，。,.!?！？、")
		if len(w) > 2 && len(w) < 15 {
			parts = append(parts, sanitizeName(w))
		}
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "-")
	}
	return "correction-" + fmt.Sprintf("%d", len(text))
}

type failure struct {
	toolName string
	toolInp  string
	errMsg   string
	index    int
}

func buildFailureFixName(f failure) string {
	// Build a descriptive name from the error context
	inputLower := strings.ToLower(f.toolInp)

	type matcher struct {
		keyword string
		name    string
	}
	matchers := []matcher{
		{"build", "build-failure-recovery"},
		{"compile", "compile-failure-recovery"},
		{"test", "test-failure-recovery"},
		{"go.mod", "go-module-failure"},
		{"go.sum", "go-module-failure"},
		{"permission", "permission-failure"},
		{"not found", "not-found-recovery"},
		{"no such file", "missing-file-recovery"},
		{"already exists", "already-exists-recovery"},
	}

	for _, m := range matchers {
		if strings.Contains(inputLower, m.keyword) || strings.Contains(strings.ToLower(f.errMsg), m.keyword) {
			return m.name
		}
	}

	return "fix-" + sanitizeName(f.toolName)
}

// --- Aggregation ---

type candidateAggregate struct {
	candidate SkillCandidate
	hits      int
	scoreSum  float64
	sessions  map[string]struct{}
}

func aggregateCandidate(aggregated map[string]*candidateAggregate, candidate SkillCandidate, sessionID string) {
	key := strings.ToLower(strings.TrimSpace(candidate.Name))
	if key == "" {
		return
	}
	agg, ok := aggregated[key]
	if !ok {
		aggregated[key] = &candidateAggregate{
			candidate: candidate,
			hits:      1,
			scoreSum:  candidate.Score,
			sessions: map[string]struct{}{
				sessionID: {},
			},
		}
		return
	}
	agg.hits++
	agg.scoreSum += candidate.Score
	// Keep the longer description and evidence
	if len(candidate.Description) > len(agg.candidate.Description) {
		agg.candidate.Description = candidate.Description
	}
	if len(candidate.Reason) > len(agg.candidate.Reason) {
		agg.candidate.Reason = candidate.Reason
	}
	if len(candidate.Evidence) > len(agg.candidate.Evidence) {
		agg.candidate.Evidence = candidate.Evidence
	}
	agg.sessions[sessionID] = struct{}{}
}

func finalizeCandidates(aggregated map[string]*candidateAggregate) []SkillCandidate {
	candidates := make([]SkillCandidate, 0, len(aggregated))
	for _, agg := range aggregated {
		candidate := agg.candidate
		candidate.EvidenceCount = len(agg.sessions)
		candidate.SourceSessions = make([]string, 0, len(agg.sessions))
		for sessionID := range agg.sessions {
			candidate.SourceSessions = append(candidate.SourceSessions, sessionID)
		}
		sort.Strings(candidate.SourceSessions)
		avgScore := agg.scoreSum / float64(agg.hits)
		candidate.Score = avgScore + 0.5*float64(candidate.EvidenceCount-1)
		if candidate.EvidenceCount > 1 {
			candidate.Reason = fmt.Sprintf("%s; converged across %d sessions", candidate.Reason, candidate.EvidenceCount)
		}
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].EvidenceCount != candidates[j].EvidenceCount {
			return candidates[i].EvidenceCount > candidates[j].EvidenceCount
		}
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Name < candidates[j].Name
	})
	return candidates
}

// RecordUsage records token usage for session analysis tasks.
func (sa *SessionAnalyzer) RecordUsage(usage provider.TokenUsage) {
	// Intentionally empty — usage is recorded by Knight.RunTask via budget
}
