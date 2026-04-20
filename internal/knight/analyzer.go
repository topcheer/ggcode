package knight

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// SessionAnalyzer analyzes session history to discover reusable patterns.
type SessionAnalyzer struct {
	store   session.Store
	budget  *Budget
	knight  *Knight
	projDir string
}

// NewSessionAnalyzer creates a session analyzer.
func NewSessionAnalyzer(k *Knight) *SessionAnalyzer {
	return &SessionAnalyzer{
		store:   k.store,
		budget:  k.budget,
		knight:  k,
		projDir: k.projDir,
	}
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
}

// toolCallDetail holds extracted information from a single tool call.
type toolCallDetail struct {
	ToolName string
	Input    map[string]interface{}
	RawInput json.RawMessage
	IsError  bool
	ErrorMsg string
	Output   string
}

// AnalyzeRecent scans recent sessions for reusable patterns.
func (sa *SessionAnalyzer) AnalyzeRecent(ctx context.Context) (*AnalysisResult, error) {
	if sa.store == nil {
		return nil, fmt.Errorf("session store not available")
	}

	sessions, err := sa.store.List()
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	result := &AnalysisResult{}
	aggregated := make(map[string]*candidateAggregate)

	// Analyze up to 10 most recent sessions
	limit := 10
	if len(sessions) < limit {
		limit = len(sessions)
	}

	for i := 0; i < limit; i++ {
		full, err := sa.store.Load(sessions[i].ID)
		if err != nil {
			debug.Log("knight", "cannot load session %s: %v", sessions[i].ID, err)
			continue
		}
		candidates := sa.analyzeSession(full)
		for _, candidate := range candidates {
			aggregateCandidate(aggregated, candidate, full.ID)
		}
		result.SessionsAnalyzed++
	}
	result.SkillCandidates = finalizeCandidates(aggregated)

	debug.Log("knight", "session analysis: %d sessions, %d candidates",
		result.SessionsAnalyzed, len(result.SkillCandidates))

	return result, nil
}

// analyzeSession runs all analysis heuristics on a single session.
func (sa *SessionAnalyzer) analyzeSession(ses *session.Session) []SkillCandidate {
	if len(ses.Messages) < 4 {
		return nil // too short to be interesting
	}

	var all []SkillCandidate

	// 1. Detect repeated tool patterns (workflow extraction)
	all = append(all, sa.detectToolPatterns(ses)...)

	// 2. Detect user corrections (highest value learning source)
	all = append(all, sa.detectCorrections(ses)...)

	// 3. Detect failure → fix pairs
	all = append(all, sa.detectFailurePatterns(ses)...)

	// 4. Extract tool parameter insights (commands, files, conventions)
	all = append(all, sa.detectToolParameterInsights(ses)...)

	return all
}

// --- Heuristic 1: Repeated tool patterns ---

func (sa *SessionAnalyzer) detectToolPatterns(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

	toolCounts := make(map[string]int)
	var toolSequence []string
	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" {
				toolCounts[block.ToolName]++
				toolSequence = append(toolSequence, block.ToolName)
			}
		}
	}

	// Repeated sequences
	patterns := detectRepeatedPatterns(toolSequence)
	for _, pattern := range patterns {
		if len(pattern.Tools) >= 2 && pattern.Count >= 2 {
			candidates = append(candidates, SkillCandidate{
				Name:        pattern.SuggestedName(),
				Description: pattern.Description(),
				Scope:       inferScope(pattern.Tools),
				Score:       float64(pattern.Count) * float64(len(pattern.Tools)) * 0.3,
				Reason:      fmt.Sprintf("repeated %d times in session %s", pattern.Count, ses.ID),
			})
		}
	}

	// Heavy tool combinations
	if toolCounts["run_command"] >= 3 && toolCounts["read_file"] >= 2 {
		candidates = append(candidates, SkillCandidate{
			Name:        "build-and-verify",
			Description: "Build project, run tests, and verify output",
			Scope:       "project",
			Score:       0.5,
			Reason:      fmt.Sprintf("build-verify pattern in session %s", ses.ID),
		})
	}

	return candidates
}

// --- Heuristic 2: User corrections (highest value) ---

var correctionSignals = []string{
	"不要", "别用", "不应该", "不对", "错了", "应该是", "改用", "换成",
	"wrong", "don't", "should be", "instead", "no,", "not that", "use x.",
	"incorrect", "fix it", "different",
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
		// Also extract what tools the assistant used
		var toolsUsed []string
		for _, block := range prev.Content {
			if block.Type == "tool_use" {
				toolsUsed = append(toolsUsed, block.ToolName)
			}
		}

		desc := fmt.Sprintf("User correction: %s", truncate(text, 80))
		if prevText != "" {
			desc = fmt.Sprintf("Instead of \"%s\", user said: %s", truncate(prevText, 40), truncate(text, 60))
		}

		candidates = append(candidates, SkillCandidate{
			Name:        buildCorrectionName(text, toolsUsed),
			Description: desc,
			Scope:       inferCorrectionScope(text, prevText, toolsUsed),
			Score:       2.0, // Corrections are the highest value signal
			Reason:      fmt.Sprintf("user correction in session %s (tools: %s)", ses.ID, strings.Join(toolsUsed, ",")),
		})
	}

	return candidates
}

// --- Heuristic 3: Failure → fix pairs ---

func (sa *SessionAnalyzer) detectFailurePatterns(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

	// Build maps: toolID → toolName and toolID → input summary from tool_use blocks
	toolIDToName := make(map[string]string)
	toolIDToInput := make(map[string]string)
	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" {
				toolIDToName[block.ToolID] = block.ToolName
				if block.Input != nil {
					if b, err := json.Marshal(block.Input); err == nil {
						toolIDToInput[block.ToolID] = truncate(string(b), 200)
					}
				}
			}
		}
	}

	// Find [tool_result(is_error=true)] followed by [tool_result(success)] for the same tool.
	// To avoid false positives (e.g. two unrelated run_command calls), we track
	// which failure→success pairs have already been matched per toolName so each
	// tool only generates at most one candidate per session.
	type failure struct {
		toolName  string
		toolInput string
		errMsg    string
		index     int
	}
	var failures []failure
	matched := make(map[string]bool) // toolName → already matched

	for i, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_result" && block.IsError {
				toolName := toolIDToName[block.ToolID]
				if toolName == "" {
					continue
				}
				failures = append(failures, failure{
					toolName:  toolName,
					toolInput: toolIDToInput[block.ToolID],
					errMsg:    truncate(block.Output, 200),
					index:     i,
				})
			}
		}
	}

	// For each failure, look for a successful use of the same tool within 4 messages.
	// Only match if there is at least one assistant message (a correction attempt)
	// between the failure and the success.
	for _, f := range failures {
		if matched[f.toolName] {
			continue
		}
		for j := f.index + 1; j < len(ses.Messages) && j <= f.index+4; j++ {
			msg := ses.Messages[j]
			// Require an assistant correction message between failure and fix
			hasCorrection := false
			for k := f.index + 1; k < j; k++ {
				if ses.Messages[k].Role == "assistant" {
					hasCorrection = true
					break
				}
			}
			if !hasCorrection {
				continue
			}
			for _, block := range msg.Content {
				if block.Type == "tool_result" && !block.IsError {
					toolName := toolIDToName[block.ToolID]
					if toolName == f.toolName {
						candidates = append(candidates, SkillCandidate{
							Name:        "troubleshoot-" + sanitizeName(f.toolName),
							Description: fmt.Sprintf("%s failure recovery: %s", f.toolName, f.errMsg),
							Scope:       "project",
							Score:       1.5,
							Reason:      fmt.Sprintf("error->fix pattern for %s (input: %s) in session %s", f.toolName, truncate(f.toolInput, 60), ses.ID),
						})
						matched[f.toolName] = true
						break
					}
				}
			}
		}
	}

	return candidates
}

// --- Heuristic 4: Tool parameter insights ---

func (sa *SessionAnalyzer) detectToolParameterInsights(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

	// Extract frequently used commands from run_command calls
	commands := make(map[string]int)
	editPaths := make(map[string]int)

	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type != "tool_use" {
				continue
			}

			detail := extractToolDetailFromBlock(block)

			switch block.ToolName {
			case "run_command":
				if cmd := detail.command(); cmd != "" {
					commands[cmd]++
				}
			case "edit_file", "write_file":
				if p := detail.filePath(); p != "" {
					editPaths[p]++
				}
			}
		}
	}

	// Frequently used commands → project conventions
	for cmd, count := range commands {
		if count >= 2 {
			candidates = append(candidates, SkillCandidate{
				Name:        "project-command-" + sanitizeName(cmd),
				Description: fmt.Sprintf("Project uses command: %s (used %d times)", cmd, count),
				Scope:       inferCommandScope(cmd),
				Score:       float64(count) * 0.4,
				Reason:      fmt.Sprintf("frequent command in session %s", ses.ID),
			})
		}
	}

	// Frequently edited files → hot files
	for p, count := range editPaths {
		if count >= 2 {
			candidates = append(candidates, SkillCandidate{
				Name:        "hot-file-" + sanitizeName(filepath.Base(p)),
				Description: fmt.Sprintf("Frequently edited file: %s (%d edits)", p, count),
				Scope:       "project",
				Score:       float64(count) * 0.3,
				Reason:      fmt.Sprintf("hot file in session %s", ses.ID),
			})
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
		// Truncate large files
		content := string(data)
		if len(content) > 2000 {
			content = content[:2000] + "\n... (truncated)"
		}
		conventions = append(conventions, fmt.Sprintf("=== %s ===\n%s", f, content))
	}

	return strings.Join(conventions, "\n\n")
}

// --- LLM skill generation ---

// GenerateSkillFromAnalysis uses LLM to create a skill from a high-value candidate.
func (sa *SessionAnalyzer) GenerateSkillFromAnalysis(ctx context.Context, candidate SkillCandidate, factory AgentFactory) (string, error) {
	// Collect project conventions for context
	conventions := sa.CollectProjectConventions()
	conventionsSection := ""
	if conventions != "" {
		conventionsSection = fmt.Sprintf("\n\n## Project conventions (from config files)\n%s", conventions)
	}

	prompt := fmt.Sprintf(`Analyze the following reusable development pattern and create a skill document for it.

Pattern name: %s
Description: %s
Scope: %s
Reason discovered: %s%s

Create a SKILL.md document with:
1. YAML frontmatter with fields: name, description, scope, platforms, requires, created_by: knight
   IMPORTANT: all string values containing colons, quotes, or special chars MUST be quoted
2. Clear step-by-step procedure
3. Known gotchas or pitfalls
4. When to use and when NOT to use

Output ONLY the skill document content, starting with ---`, candidate.Name, candidate.Description, candidate.Scope, candidate.Reason, conventionsSection)

	result := sa.knight.RunTask(ctx, "skill-generation", prompt, factory)
	if result.Error != nil {
		return "", result.Error
	}

	return strings.TrimSpace(result.Output), nil
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

func extractToolDetailFromBlock(block provider.ContentBlock) toolCallDetail {
	d := toolCallDetail{ToolName: block.ToolName}
	if block.Input != nil {
		d.RawInput = block.Input
		json.Unmarshal(block.Input, &d.Input)
	}
	if block.IsError {
		d.ErrorMsg = block.Output
	}
	d.Output = block.Output
	return d
}

func (d toolCallDetail) command() string {
	if cmd, ok := d.Input["command"].(string); ok {
		return cmd
	}
	return ""
}

func (d toolCallDetail) filePath() string {
	if p, ok := d.Input["file_path"].(string); ok {
		return p
	}
	return ""
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
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	return strings.Trim(s, "-")
}

// --- Tool pattern types ---

type toolPattern struct {
	Tools []string
	Count int
}

func (p *toolPattern) SuggestedName() string {
	names := make([]string, len(p.Tools))
	for i, t := range p.Tools {
		switch t {
		case "run_command":
			names[i] = "run"
		case "read_file":
			names[i] = "read"
		case "edit_file":
			names[i] = "edit"
		case "write_file":
			names[i] = "write"
		case "search_files":
			names[i] = "search"
		case "glob":
			names[i] = "find"
		default:
			names[i] = t
		}
	}
	return strings.Join(names, "-")
}

func (p *toolPattern) Description() string {
	return fmt.Sprintf("Workflow: %s (repeated %d times)", strings.Join(p.Tools, " -> "), p.Count)
}

func detectRepeatedPatterns(tools []string) []toolPattern {
	var patterns []toolPattern
	seen := make(map[string]int)

	for size := 2; size <= 3; size++ {
		if len(tools) < size {
			continue
		}
		for i := 0; i <= len(tools)-size; i++ {
			seq := strings.Join(tools[i:i+size], "|")
			seen[seq]++
		}
	}

	for seq, count := range seen {
		if count >= 2 {
			patterns = append(patterns, toolPattern{
				Tools: strings.Split(seq, "|"),
				Count: count,
			})
		}
	}

	return patterns
}

func inferScope(tools []string) string {
	if len(tools) == 0 {
		return "project"
	}
	for _, t := range tools {
		switch t {
		case "edit_file", "write_file", "read_file", "search_files", "glob", "run_command":
			return "project"
		}
	}
	for _, t := range tools {
		switch t {
		case "web_fetch", "web_search":
		default:
			return "project"
		}
	}
	return "global"
}

func inferCommandScope(cmd string) string {
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	if cmd == "" {
		return "project"
	}
	projectHints := []string{
		"./", "../", ".ggcode", "go.mod", "go.sum", "package.json", "pyproject.toml",
		"make ", "internal/", "cmd/", "scripts/", "docs/", "src/", "test ",
	}
	for _, hint := range projectHints {
		if strings.Contains(cmd, hint) {
			return "project"
		}
	}
	globalPrefixes := []string{
		"git status", "git diff", "git log", "docker ps", "docker images", "docker logs",
		"kubectl get", "go env", "go version", "python --version", "node --version",
	}
	for _, prefix := range globalPrefixes {
		if strings.HasPrefix(cmd, prefix) {
			return "global"
		}
	}
	return "project"
}

func inferCorrectionScope(text, prevText string, toolsUsed []string) string {
	scope := inferScope(toolsUsed)
	if scope == "project" {
		return scope
	}
	combined := strings.ToLower(text + " " + prevText)
	for _, hint := range []string{"internal/", "cmd/", "src/", ".ggcode", "go.mod", "package.json", "pyproject.toml"} {
		if strings.Contains(combined, hint) {
			return "project"
		}
	}
	if len(toolsUsed) == 0 {
		return "project"
	}
	return scope
}

func buildCorrectionName(text string, toolsUsed []string) string {
	parts := []string{"correction"}
	if len(toolsUsed) > 0 {
		parts = append(parts, sanitizeName(strings.Join(uniqueStrings(toolsUsed), "-")))
	}
	if slug := sanitizeName(truncate(text, 40)); slug != "" {
		parts = append(parts, slug)
	}
	name := strings.Join(parts, "-")
	if len(name) > 80 {
		name = name[:80]
	}
	return strings.Trim(name, "-")
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

type candidateAggregate struct {
	candidate SkillCandidate
	hits      int
	scoreSum  float64
	sessions  map[string]struct{}
}

func aggregateCandidate(aggregated map[string]*candidateAggregate, candidate SkillCandidate, sessionID string) {
	key := strings.ToLower(strings.TrimSpace(candidate.Scope + "|" + candidate.Name))
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
	if len(candidate.Description) > len(agg.candidate.Description) {
		agg.candidate.Description = candidate.Description
	}
	if len(candidate.Reason) > len(agg.candidate.Reason) {
		agg.candidate.Reason = candidate.Reason
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
