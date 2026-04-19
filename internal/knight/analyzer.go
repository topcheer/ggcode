package knight

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Name        string
	Description string
	Scope       string // "global" or "project"
	Score       float64
	Reason      string
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
		result.SkillCandidates = append(result.SkillCandidates, candidates...)
		result.SessionsAnalyzed++
	}

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
			Name:        "correction-" + ses.ID[:8],
			Description: desc,
			Scope:       "project",
			Score:       2.0, // Corrections are the highest value signal
			Reason:      fmt.Sprintf("user correction in session %s (tools: %s)", ses.ID, strings.Join(toolsUsed, ",")),
		})
	}

	return candidates
}

// --- Heuristic 3: Failure → fix pairs ---

func (sa *SessionAnalyzer) detectFailurePatterns(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

	// Build a map: toolID → toolName from tool_use blocks
	toolIDToName := make(map[string]string)
	for _, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_use" {
				toolIDToName[block.ToolID] = block.ToolName
			}
		}
	}

	// Find [tool_result(is_error=true)] followed by [tool_result(success)] for the same tool
	type failure struct {
		toolName string
		errMsg   string
		index    int
	}
	var failures []failure

	for i, msg := range ses.Messages {
		for _, block := range msg.Content {
			if block.Type == "tool_result" && block.IsError {
				toolName := toolIDToName[block.ToolID]
				if toolName == "" {
					continue
				}
				failures = append(failures, failure{
					toolName: toolName,
					errMsg:   truncate(block.Output, 200),
					index:    i,
				})
			}
		}
	}

	// For each failure, look for a successful use of the same tool within 4 messages
	for _, f := range failures {
		for j := f.index + 1; j < len(ses.Messages) && j <= f.index+4; j++ {
			for _, block := range ses.Messages[j].Content {
				if block.Type == "tool_result" && !block.IsError {
					toolName := toolIDToName[block.ToolID]
					if toolName == f.toolName {
						candidates = append(candidates, SkillCandidate{
							Name:        "troubleshoot-" + sanitizeName(f.toolName),
							Description: fmt.Sprintf("%s failure recovery: %s", f.toolName, f.errMsg),
							Scope:       "project",
							Score:       1.5,
							Reason:      fmt.Sprintf("error->fix pattern for %s in session %s", f.toolName, ses.ID),
						})
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
				Scope:       "project",
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
	for _, t := range tools {
		if t == "edit_file" || t == "write_file" {
			return "project"
		}
	}
	return "global"
}

// RecordUsage records token usage for session analysis tasks.
func (sa *SessionAnalyzer) RecordUsage(usage provider.TokenUsage) {
	// Intentionally empty — usage is recorded by Knight.RunTask via budget
}
