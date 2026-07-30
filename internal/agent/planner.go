package agent

// Agent-Side Planning & Automatic Task Decomposition
//
// Research: Devin, Claude Code, Cursor, OpenHands all implement planning systems
// that detect complex multi-step requests and proactively decompose them into
// structured sub-task plans. Key patterns from the literature:
//
//   - Devin's "Planner" module: breaks complex tasks into an ordered checklist
//     before coding begins (Cognition Labs, 2024-2025)
//   - Claude Code's auto-todo: the agent uses todo_write proactively for
//     multi-file/multi-step requests (Anthropic, 2025)
//   - Cursor's agent mode: generates an implicit plan for multi-file refactors
//   - OpenHands' "Planner" agent: produces a task DAG with dependencies
//     (AllHands AI / OpenDevin, arXiv:2407.16741)
//
// ggcode already has todo_write (manual checklist) and task_create/task_update
// (manual task tracking). The gap is: the agent doesn't AUTOMATICALLY detect
// when a request is complex enough to warrant a plan and proactively create
// one. This planner fills that gap with a deterministic, zero-LLM-cost approach:
//
//   1. COMPLEXITY DETECTION: heuristic analysis of the user's first message
//      to determine whether a structured plan would help (multi-file, multi-goal,
//      multi-step indicators).
//
//   2. PLAN SUGGESTION: when complexity is detected, inject a concise message
//      early in the conversation suggesting the agent use todo_write to create
//      a plan. This leverages the LLM's own decomposition capability rather
//      than a fixed heuristic plan.
//
//   3. PROGRESS MONITORING: track whether the agent created a todo list within
//      a reasonable number of iterations after a plan suggestion. If not, and
//      the task is still complex, inject a gentle reminder.
//
// All operations are pure computation (string matching, counters) — no I/O,
// no blocking, no external dependencies. Fully compatible with existing
// todo_write and task systems.

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// complexityThreshold: minimum score to trigger plan suggestion.
	// Tuned so simple requests (single file edit, quick question) don't
	// trigger, while multi-file refactors and multi-goal requests do.
	complexityThreshold = 3

	// planSuggestionIter: inject the plan suggestion at this iteration.
	// Setting it to 1 means right after the first LLM turn completes —
	// early enough to guide the agent, late enough to not interfere
	// with the initial response generation.
	planSuggestionIter = 1

	// planReminderIter: if the agent hasn't created a todo by this
	// iteration after a suggestion, inject a reminder.
	planReminderIter = 5

	// maxPlanSuggestionLen: cap the suggestion text length for context economy.
	maxPlanSuggestionLen = 600
)

// planState holds the planner's per-run state.
type planState struct {
	mu sync.Mutex

	// Whether the initial user request was detected as complex.
	isComplex bool

	// Whether a plan suggestion has already been injected.
	suggested bool

	// Whether a reminder has already been injected.
	reminded bool

	// Whether the agent created a todo_write at any point during this run.
	todoCreated bool

	// The initial user prompt (cached for analysis).
	userPrompt string
}

func newPlanState() *planState {
	return &planState{}
}

// reset clears state for a new run.
func (p *planState) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isComplex = false
	p.suggested = false
	p.reminded = false
	p.todoCreated = false
	p.userPrompt = ""
}

// analyzeUserPrompt runs deterministic complexity detection on the user's
// first message. Returns a complexity score (higher = more complex).
//
// Detection signals (each contributes 1-2 points):
//   - Multiple files/directories referenced (multi-file indicators)
//   - Multiple action verbs (create, add, fix, update, implement, refactor...)
//   - Sequential indicators (then, after, next, finally, also, also, and then)
//   - Architecture/design language (design, architecture, system, component...)
//   - Feature scope language (feature, across, throughout, all, entire, full)
//   - Known multi-step patterns (refactor, migrate, add test, add error handling)
func (p *planState) analyzeUserPrompt(prompt string) int {
	if strings.TrimSpace(prompt) == "" {
		return 0
	}

	score := 0
	lower := strings.ToLower(prompt)

	// --- Multi-file indicators ---
	// Count distinct file-like references (paths, file extensions).
	fileRefs := countFileReferences(lower)
	if fileRefs >= 2 {
		score += 1
	}
	if fileRefs >= 4 {
		score += 1 // bonus for many files
	}

	// --- Multiple action verbs ---
	// Count imperative/infinitive verbs that typically start coding tasks.
	actionVerbs := []string{
		"create", "add", "fix", "update", "implement", "refactor",
		"modify", "change", "remove", "delete", "rename", "move",
		"extract", "optimize", "clean", "restructure", "migrate",
		"configure", "set up", "build", "integrate", "write",
	}
	verbCount := countKeywordMatches(lower, actionVerbs)
	if verbCount >= 2 {
		score += 1
	}
	if verbCount >= 4 {
		score += 1
	}

	// --- Sequential / multi-step indicators ---
	sequenceMarkers := []string{
		"first", "then", "after that", "next", "finally",
		"step", "phase", "stage", "part ", "part 1", "part 2",
		"also ", "additionally", "moreover", "and then",
		"1.", "2.", "3.", "4.", "5.",
		"- ", "• ",
	}
	seqCount := countKeywordMatches(lower, sequenceMarkers)
	if seqCount >= 2 {
		score += 1
	}

	// --- Architecture / system design language ---
	archKeywords := []string{
		"architecture", "design", "system", "module", "component",
		"layer", "service", "pipeline", "workflow", "framework",
		"pattern", "abstraction", "interface", "protocol",
	}
	if countKeywordMatches(lower, archKeywords) >= 1 {
		score += 1
	}

	// --- Broad scope language ---
	scopeKeywords := []string{
		"across", "throughout", "every", "all ", "entire", "full ",
		"complete", "comprehensive", "end-to-end", "whole ",
	}
	if countKeywordMatches(lower, scopeKeywords) >= 1 {
		score += 1
	}

	// --- Known multi-step patterns ---
	multiStepPatterns := []string{
		"refactor",    // refactor almost always involves multiple files
		"migrate",     // migration = multi-step
		"add test",    // adding tests often means multiple test files
		"error handl", // adding error handling across code paths
		"add logging", // instrumentation across files
		"add metric",  // metrics across codebase
		"unify",       // unifying patterns = multi-file
		"standardize", // standardizing = multi-file
	}
	if countKeywordMatches(lower, multiStepPatterns) >= 1 {
		score += 1
	}

	// --- Length bonus: very long prompts often describe complex tasks ---
	runeCount := len([]rune(prompt))
	if runeCount > 500 {
		score += 1
	}
	if runeCount > 1500 {
		score += 1
	}

	return score
}

// markTodoCreated records that the agent used todo_write during this run.
func (p *planState) markTodoCreated() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.todoCreated = true
}

// setIsComplex sets the complexity flag and caches the user prompt.
func (p *planState) setIsComplex(complex bool, prompt string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isComplex = complex
	p.userPrompt = prompt
}

// shouldSuggestPlan returns true if the plan suggestion should be injected
// at the given iteration. Only fires once per run, and only when the task
// was detected as complex.
func (p *planState) shouldSuggestPlan(iteration int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.isComplex || p.suggested || p.todoCreated {
		return false
	}
	return iteration >= planSuggestionIter
}

// markSuggested marks that the suggestion has been injected.
func (p *planState) markSuggested() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.suggested = true
}

// shouldRemindPlan returns true if a reminder should be injected.
// Fires when the suggestion was given but the agent hasn't created a todo
// by planReminderIter iterations.
func (p *planState) shouldRemindPlan(iteration int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.isComplex || !p.suggested || p.reminded || p.todoCreated {
		return false
	}
	return iteration >= planReminderIter
}

// markReminded marks that the reminder has been injected.
func (p *planState) markReminded() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reminded = true
}

// planSuggestionText returns the text to inject when suggesting a plan.
// This is a concise, non-prescriptive message — it tells the agent to create
// a structured plan using todo_write, not what the plan should be.
func planSuggestionText() string {
	return "Planning hint: This request appears to involve multiple steps or files. " +
		"Consider using `todo_write` to create a structured plan before diving into implementation. " +
		"Break the work into 3-7 concrete sub-tasks, work through them systematically, " +
		"and mark each as complete as you finish. This improves quality and prevents missed steps."
}

// planReminderText returns the reminder message when the agent hasn't created a plan.
func planReminderText() string {
	return "Planning reminder: You haven't created a todo list yet for this multi-step task. " +
		"Use `todo_write` to outline your remaining steps so nothing is missed."
}

// --- Agent integration methods ---

// plannerAnalyze runs complexity detection on the user's first message.
// Called once at the start of RunStreamWithContent.
func (a *Agent) plannerAnalyze(content string) {
	if a.planner == nil {
		return
	}
	score := a.planner.analyzeUserPrompt(content)
	isComplex := score >= complexityThreshold
	a.planner.setIsComplex(isComplex, content)
	if isComplex {
		debug.Log("planner", "complex request detected (score=%d, threshold=%d)", score, complexityThreshold)
	}
}

// maybeSuggestPlan checks whether to inject a plan suggestion at the given
// iteration. Returns the suggestion text if it should be injected, empty otherwise.
func (a *Agent) maybeSuggestPlan(iteration int) string {
	if a.planner == nil {
		return ""
	}
	if a.planner.shouldSuggestPlan(iteration) {
		a.planner.markSuggested()
		text := planSuggestionText()
		if len(text) > maxPlanSuggestionLen {
			text = text[:maxPlanSuggestionLen]
		}
		debug.Log("planner", "injecting plan suggestion at iteration %d", iteration)
		return text
	}
	if a.planner.shouldRemindPlan(iteration) {
		a.planner.markReminded()
		debug.Log("planner", "injecting plan reminder at iteration %d", iteration)
		return planReminderText()
	}
	return ""
}

// plannerMarkTodoCreated records that the agent used todo_write.
// Called whenever todo_write is executed.
func (a *Agent) plannerMarkTodoCreated() {
	if a.planner != nil {
		a.planner.markTodoCreated()
	}
}

// resetPlanner clears state for a new run.
func (a *Agent) resetPlanner() {
	if a.planner != nil {
		a.planner.reset()
	}
}

// --- Complexity detection helpers ---

// countFileReferences counts distinct file-like references in the text.
// Detects: .go, .py, .js, .ts, .rs, .java, .rb, .c, .cpp, .h, .yaml, .json,
// .toml, .md, .sql, .sh, paths with slashes, backtick-quoted identifiers.
func countFileReferences(lower string) int {
	extensions := []string{
		".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".java",
		".rb", ".c", ".cpp", ".h", ".hpp", ".yaml", ".yml", ".json",
		".toml", ".xml", ".md", ".txt", ".sql", ".sh", ".bash",
		".vue", ".svelte", ".kt", ".swift", ".dart", ".scala",
		".proto", ".graphql", ".tf", ".dockerfile",
	}
	seen := make(map[string]bool)
	isAlpha := func(b byte) bool {
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for _, ext := range extensions {
		idx := 0
		for {
			pos := strings.Index(lower[idx:], ext)
			if pos < 0 {
				break
			}
			absPos := idx + pos
			// Validate: the char before the extension must be a letter (part
			// of a filename, not a domain like "example.com" matching ".c").
			// The char after must be a non-alphanumeric separator (space,
			// quote, comma, end-of-string) so ".c" in "check" doesn't match.
			if absPos > 0 && !isAlpha(lower[absPos-1]) {
				idx = absPos + len(ext)
				continue
			}
			end := absPos + len(ext)
			if end < len(lower) {
				next := lower[end]
				// Allow trailing 'x' for .tsx/.jsx, and '/' for directory refs.
				if next != 'x' && next != '/' && isAlpha(next) {
					idx = absPos + len(ext)
					continue
				}
			}
			// Extract the filename around this extension.
			start := absPos
			for start > 0 && (lower[start-1] == '/' || lower[start-1] == '_' ||
				lower[start-1] == '-' || lower[start-1] == '.' ||
				(lower[start-1] >= 'a' && lower[start-1] <= 'z') ||
				(lower[start-1] >= '0' && lower[start-1] <= '9')) {
				start--
			}
			for end < len(lower) && lower[end] == 'x' { // .tsx etc
				end++
			}
			filename := lower[start:end]
			if filename != "" && len(filename) > len(ext) {
				seen[filename] = true
			}
			idx = absPos + len(ext)
		}
	}

	// Count slash-separated paths (e.g., "internal/agent/", "src/main.go")
	// Look for patterns like word/word indicating directory paths.
	pathPattern := false
	if strings.Contains(lower, "/") {
		// Strip URLs before checking for path separators.
		stripped := lower
		for _, prefix := range []string{"http://", "https://", "ftp://", "ssh://"} {
			for {
				idx := strings.Index(stripped, prefix)
				if idx < 0 {
					break
				}
				// Find end of URL (whitespace or end of string).
				end := idx + len(prefix)
				for end < len(stripped) && stripped[end] != ' ' && stripped[end] != '\n' && stripped[end] != '\t' {
					end++
				}
				stripped = stripped[:idx] + stripped[end:]
			}
		}
		// Simple heuristic: if there are 2+ path separators, likely multi-file
		slashCount := strings.Count(stripped, "/")
		if slashCount >= 2 {
			pathPattern = true
		}
	}

	refCount := len(seen)
	if pathPattern {
		refCount += 1
	}
	return refCount
}

// countKeywordMatches counts how many distinct keywords from the list
// appear in the text. Each keyword is counted at most once.
func countKeywordMatches(lower string, keywords []string) int {
	count := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			count++
		}
	}
	return count
}
