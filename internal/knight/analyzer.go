package knight

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"unicode"
)

type SessionAnalyzer struct {
	store             session.Store
	budget            *Budget
	knight            *Knight
	projDir           string
	normalizedProj    string
	filterByWorkspace bool
}

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

func isTempDir(path string) bool {
	base := strings.ToLower(filepath.Base(path))

	normalized := filepath.ToSlash(path)
	if tempDir := os.TempDir(); tempDir != "" {

		normalizedTemp := strings.TrimSuffix(filepath.ToSlash(tempDir), "/")
		if normalizedTemp != "" && (strings.HasPrefix(normalized, normalizedTemp+"/") || normalized == normalizedTemp) {
			return true
		}
	}

	return strings.HasPrefix(base, "knight-") || strings.HasPrefix(base, "test-")
}

type AnalysisResult struct {
	SessionsAnalyzed int
	SkillCandidates  []SkillCandidate
}

type SkillCandidate struct {
	Name           string
	Description    string
	Scope          string
	Score          float64
	Reason         string
	EvidenceCount  int
	SourceSessions []string

	Evidence []string

	Category string

	GenFailCount int `json:",omitempty"`

	FirstQueuedAt       time.Time `json:"first_queued_at,omitempty"`
	LastQueuedAt        time.Time `json:"last_queued_at,omitempty"`
	QueueTouchCount     int       `json:"queue_touch_count,omitempty"`
	QueuePriority       float64   `json:"queue_priority,omitempty"`
	QueuePriorityReason string    `json:"queue_priority_reason,omitempty"`
}

func (sa *SessionAnalyzer) AnalyzeRecent(ctx context.Context) (*AnalysisResult, error) {
	if sa.store == nil {
		return nil, fmt.Errorf("session store not available")
	}

	sessions, err := sa.store.List()
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var eligible []*session.Session
	for _, s := range sessions {
		sa.knight.mu.Lock()
		lastAnalyzed, alreadyAnalyzed := sa.knight.analyzedSessions[s.ID]
		sa.knight.mu.Unlock()
		if alreadyAnalyzed {

			if !s.UpdatedAt.After(lastAnalyzed) {
				continue
			}
		}

		if sa.filterByWorkspace && s.Workspace != "" && s.Workspace != sa.normalizedProj {
			continue
		}

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

	scores := make(map[string]int, len(eligible))
	for _, ses := range eligible {
		scores[ses.ID] = sa.sessionSignalScore(ses)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		si, sj := scores[eligible[i].ID], scores[eligible[j].ID]
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

func (sa *SessionAnalyzer) sessionSignalScore(ses *session.Session) int {
	score := 0
	textLower := ""
	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "text" {
				textLower += strings.ToLower(block.Text) + " "
			}
			if block.Type == "tool_result" && block.IsError {
				score += 3
			}
		}
	}

	for _, signal := range correctionSignals {
		if strings.Contains(textLower, strings.ToLower(signal)) {
			score += 5
			break
		}
	}
	return score
}

func (sa *SessionAnalyzer) analyzeSession(ses *session.Session) []SkillCandidate {
	if len(ses.Messages) < 4 {
		return nil
	}

	var all []SkillCandidate

	all = append(all, sa.detectCorrections(ses)...)

	all = append(all, sa.detectFailureFixes(ses)...)

	return all
}

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

		prev := findPrevAssistant(ses.Messages, i)
		if prev == nil {
			continue
		}

		prevText := extractText(prev.Content)

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
			Scope:       "",
			Score:       2.0,
			Reason:      fmt.Sprintf("user correction in session %s", ses.ID),
			Evidence:    evidence,
			Category:    "correction",
		})
	}

	return candidates
}

func (sa *SessionAnalyzer) detectFailureFixes(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

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

	var failures []failure
	matched := make(map[string]bool)

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
						Scope:       "",
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

func extractSkillDocument(output string) string {

	if idx := strings.LastIndex(output, "---\nname:"); idx >= 0 {
		return strings.TrimSpace(output[idx:])
	}
	if idx := strings.LastIndex(output, "---\r\nname:"); idx >= 0 {
		return strings.TrimSpace(output[idx:])
	}

	if idx := strings.Index(output, "---"); idx > 0 {
		return strings.TrimSpace(output[idx:])
	}

	if strings.HasPrefix(output, "---") {
		return output
	}
	return ""
}

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

	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

func isValidCandidateName(name string) bool {
	if len(name) < 8 {
		return false
	}

	if strings.HasPrefix(name, "correction-") {
		rest := strings.TrimPrefix(name, "correction-")
		if _, err := strconv.Atoi(rest); err == nil {
			return false
		}
	}

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

	words := strings.Fields(textLower)
	var parts []string
	cjkOnly := true

	hasCJK := false
	for _, w := range words {
		w = strings.Trim(w, "，。,.!?！？、")

		rw := []rune(w)
		if len(rw) > 2 && len(rw) < 15 {

			if san := sanitizeName(w); san != "" {
				parts = append(parts, san)
			}
		}
		if w != "" && isASCIIIdentifierWord(w) {
			cjkOnly = false
		}
		for _, r := range w {
			if unicode.Is(unicode.Han, r) {
				hasCJK = true
				break
			}
		}
		if len(parts) >= 3 {
			break
		}
	}
	if join := strings.Join(parts, "-"); join != "" {

		if len(join) >= 8 || !hasCJK {
			return join
		}
	}
	if cjkOnly || hasCJK {

		h := fnv.New32a()
		_, _ = h.Write([]byte(textLower))
		return fmt.Sprintf("cjk-correction-%08x", h.Sum32())
	}
	return "correction-" + fmt.Sprintf("%d", len(text))
}

func isASCIIIdentifierWord(w string) bool {
	for _, r := range w {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

type failure struct {
	toolName string
	toolInp  string
	errMsg   string
	index    int
}

func failureFingerprint(f failure) string {
	first := ""
	for _, ln := range strings.Split(f.errMsg, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed != "" {
			first = trimmed
			break
		}
	}
	if first == "" {
		first = f.toolInp
	}
	first = strings.ToLower(first)

	fields := strings.Fields(first)
	for i, w := range fields {
		if strings.ContainsAny(w, "/\\") || strings.Contains(w, ":") && strings.Count(w, "") > 2 && isMostlyNonAlpha(w) {
			fields[i] = "<path>"
			continue
		}
		fields[i] = stripDigits(w)
	}
	normalized := strings.Join(fields, " ")
	if len(normalized) > 120 {
		normalized = normalized[:120]
	}
	h := fnv.New32a()
	h.Write([]byte(normalized))
	return fmt.Sprintf("%08x", h.Sum32())
}

func isMostlyNonAlpha(w string) bool {
	alpha := 0
	for _, r := range w {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			alpha++
		}
	}
	return alpha*2 < len(w)
}

func stripDigits(w string) string {
	var b strings.Builder
	for _, r := range w {
		if r < '0' || r > '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func buildFailureFixName(f failure) string {

	inputLower := strings.ToLower(f.toolInp)
	fp := failureFingerprint(f)

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
			return m.name + "-" + fp
		}
	}

	return "fix-" + sanitizeName(f.toolName) + "-" + fp
}

type candidateAggregate struct {
	candidate SkillCandidate
	hits      int
	scoreSum  float64
	sessions  map[string]struct{}

	sig string
}

func failureClass(errMsg string) string {
	first := errMsg
	if i := strings.IndexAny(errMsg, "\n\r"); i >= 0 {
		first = errMsg[:i]
	}
	first = strings.ToLower(strings.TrimSpace(first))

	classes := []struct{ marker, class string }{
		{"timed out", "timeout"}, {"timeout", "timeout"},
		{"deadline exceeded", "timeout"},
		{"undefined", "undefined-symbol"},
		{"cannot find", "not-found"}, {"not found", "not-found"}, {"no such file", "not-found"}, {"enoent", "not-found"},
		{"permission denied", "permission"}, {"eacces", "permission"},
		{"already exists", "already-exists"},
		{"conflict", "conflict"},
		{"compile", "compile-error"}, {"syntax error", "compile-error"},
		{"connection refused", "network"}, {"eof", "network"}, {"reset by peer", "network"},
	}
	for _, c := range classes {
		if strings.Contains(first, c.marker) {
			return c.class
		}
	}

	return strings.Join(strings.Fields(first), " ")
}

func aggregateCandidate(aggregated map[string]*candidateAggregate, candidate SkillCandidate, sessionID string) {
	key := strings.ToLower(strings.TrimSpace(candidate.Name))
	if key == "" {
		return
	}

	sig := ""
	if candidate.Category == "failure-fix" && len(candidate.Evidence) > 0 {
		sig = failureClass(candidate.Evidence[0])
		key += "|" + sig
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
			sig: sig,
		}
		return
	}
	agg.hits++
	agg.scoreSum += candidate.Score

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

	baseNameCount := map[string]int{}
	for _, agg := range aggregated {
		baseNameCount[strings.ToLower(strings.TrimSpace(agg.candidate.Name))]++
	}
	for _, agg := range aggregated {
		candidate := agg.candidate
		if agg.sig != "" && baseNameCount[strings.ToLower(strings.TrimSpace(candidate.Name))] > 1 {
			candidate.Name = candidate.Name + "-" + agg.sig
		}
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

func (sa *SessionAnalyzer) RecordUsage(usage provider.TokenUsage) {

}
