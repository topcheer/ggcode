package knight

import (
	"context"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// SessionAnalyzer analyzes session history to discover reusable patterns.
type SessionAnalyzer struct {
	store  session.Store
	budget *Budget
	knight *Knight
}

// NewSessionAnalyzer creates a session analyzer.
func NewSessionAnalyzer(k *Knight) *SessionAnalyzer {
	return &SessionAnalyzer{
		store:  k.store,
		budget: k.budget,
		knight: k,
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

// AnalyzeRecent scans recent sessions for reusable patterns.
// It uses heuristics first (no LLM cost), then optionally uses LLM for
// high-value candidates.
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
		ses := sessions[i]
		candidates := sa.analyzeSession(ses)
		result.SkillCandidates = append(result.SkillCandidates, candidates...)
		result.SessionsAnalyzed++
	}

	debug.Log("knight", "session analysis: %d sessions, %d candidates",
		result.SessionsAnalyzed, len(result.SkillCandidates))

	return result, nil
}

// analyzeSession uses heuristics to detect reusable patterns in a session.
func (sa *SessionAnalyzer) analyzeSession(ses *session.Session) []SkillCandidate {
	var candidates []SkillCandidate

	// Heuristic 1: Session with many messages likely involved complex workflows
	msgCount := len(ses.Messages)
	if msgCount < 10 {
		return candidates // too short to be interesting
	}

	// Count tool calls by type
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

	// Heuristic 2: Repeated tool sequences suggest a workflow
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

	// Heuristic 3: Heavy use of specific tool combinations
	if toolCounts["run_command"] >= 3 && toolCounts["read_file"] >= 2 {
		candidates = append(candidates, SkillCandidate{
			Name:        "build-and-verify",
			Description: "Build project, run tests, and verify output",
			Scope:       "project",
			Score:       0.5,
			Reason:      fmt.Sprintf("build-verify pattern in session %s", ses.ID),
		})
	}

	if toolCounts["edit_file"] >= 3 && toolCounts["search_files"] >= 2 {
		candidates = append(candidates, SkillCandidate{
			Name:        "search-and-edit",
			Description: "Search codebase and make targeted edits",
			Scope:       "global",
			Score:       0.4,
			Reason:      fmt.Sprintf("search-edit pattern in session %s", ses.ID),
		})
	}

	return candidates
}

// toolPattern represents a repeated sequence of tool calls.
type toolPattern struct {
	Tools []string
	Count int
}

// SuggestedName generates a name from the tool pattern.
func (p *toolPattern) SuggestedName() string {
	names := make([]string, len(p.Tools))
	for i, t := range p.Tools {
		// Simplify tool names
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

// Description generates a description from the tool pattern.
func (p *toolPattern) Description() string {
	return fmt.Sprintf("Workflow: %s (repeated %d times)", strings.Join(p.Tools, " → "), p.Count)
}

// detectRepeatedPatterns finds tool sequences that appear multiple times.
func detectRepeatedPatterns(tools []string) []toolPattern {
	var patterns []toolPattern
	seen := make(map[string]int)

	// Check 2-tool and 3-tool sequences
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

// inferScope guesses whether a pattern is global or project-specific.
func inferScope(tools []string) string {
	// Patterns involving file editing in project dirs → project
	for _, t := range tools {
		if t == "edit_file" || t == "write_file" {
			return "project"
		}
	}
	// Read-only patterns or command-heavy → global
	return "global"
}

// GenerateSkillFromAnalysis uses LLM to create a skill from a high-value candidate.
// This is the expensive path — only called for candidates with score >= threshold.
func (sa *SessionAnalyzer) GenerateSkillFromAnalysis(ctx context.Context, candidate SkillCandidate, factory AgentFactory) (string, error) {
	prompt := fmt.Sprintf(`Analyze the following reusable development pattern and create a skill document for it.

Pattern name: %s
Description: %s
Scope: %s
Reason discovered: %s

Create a SKILL.md document with:
1. YAML frontmatter (name, description, scope, platforms, requires, created_by: knight)
2. Clear step-by-step procedure
3. Known gotchas or pitfalls
4. When to use and when NOT to use

Output ONLY the skill document content, starting with ---`, candidate.Name, candidate.Description, candidate.Scope, candidate.Reason)

	result := sa.knight.RunTask(ctx, "skill-generation", prompt, factory)
	if result.Error != nil {
		return "", result.Error
	}

	return strings.TrimSpace(result.Output), nil
}

// RecordUsage records token usage for session analysis tasks.
func (sa *SessionAnalyzer) RecordUsage(usage provider.TokenUsage) {
	// Intentionally empty — usage is recorded by Knight.RunTask via budget
}
