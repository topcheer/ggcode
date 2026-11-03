package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	complexityThreshold = 3

	planSuggestionIter = 1

	planReminderIter = 5

	maxPlanSuggestionLen = 600
)

type planState struct {
	mu sync.Mutex

	isComplex bool

	suggested bool

	reminded bool

	todoCreated bool

	userPrompt string
}

func newPlanState() *planState {
	return &planState{}
}

func (p *planState) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isComplex = false
	p.suggested = false
	p.reminded = false
	p.todoCreated = false
	p.userPrompt = ""
}

func (p *planState) analyzeUserPrompt(prompt string) int {
	if strings.TrimSpace(prompt) == "" {
		return 0
	}

	score := 0
	lower := strings.ToLower(prompt)

	fileRefs := countFileReferences(lower)
	if fileRefs >= 2 {
		score += 1
	}
	if fileRefs >= 4 {
		score += 1
	}

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

	archKeywords := []string{
		"architecture", "design", "system", "module", "component",
		"layer", "service", "pipeline", "workflow", "framework",
		"pattern", "abstraction", "interface", "protocol",
	}
	if countKeywordMatches(lower, archKeywords) >= 1 {
		score += 1
	}

	scopeKeywords := []string{
		"across", "throughout", "every", "all ", "entire", "full ",
		"complete", "comprehensive", "end-to-end", "whole ",
	}
	if countKeywordMatches(lower, scopeKeywords) >= 1 {
		score += 1
	}

	multiStepPatterns := []string{
		"refactor",
		"migrate",
		"add test",
		"error handl",
		"add logging",
		"add metric",
		"unify",
		"standardize",
	}
	if countKeywordMatches(lower, multiStepPatterns) >= 1 {
		score += 1
	}

	runeCount := len([]rune(prompt))
	if runeCount > 500 {
		score += 1
	}
	if runeCount > 1500 {
		score += 1
	}

	return score
}

func (p *planState) markTodoCreated() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.todoCreated = true
}

func (p *planState) setIsComplex(complex bool, prompt string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isComplex = complex
	p.userPrompt = prompt
}

func (p *planState) shouldSuggestPlan(iteration int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.isComplex || p.suggested || p.todoCreated {
		return false
	}
	return iteration >= planSuggestionIter
}

func (p *planState) markSuggested() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.suggested = true
}

func (p *planState) shouldRemindPlan(iteration int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.isComplex || !p.suggested || p.reminded || p.todoCreated {
		return false
	}
	return iteration >= planReminderIter
}

func (p *planState) markReminded() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reminded = true
}

func planSuggestionText() string {
	return "Planning hint: This request appears to involve multiple steps or files. " +
		"Consider using `todo_write` to create a structured plan before diving into implementation. " +
		"Break the work into 3-7 concrete sub-tasks, work through them systematically, " +
		"and mark each as complete as you finish. This improves quality and prevents missed steps."
}

func planReminderText() string {
	return "Planning reminder: You haven't created a todo list yet for this multi-step task. " +
		"Use `todo_write` to outline your remaining steps so nothing is missed."
}

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

func (a *Agent) plannerMarkTodoCreated() {
	if a.planner != nil {
		a.planner.markTodoCreated()
	}
}

func (a *Agent) resetPlanner() {
	if a.planner != nil {
		a.planner.reset()
	}
}

var fileRefExtensions = []string{
	".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".java",
	".rb", ".c", ".cpp", ".h", ".hpp", ".yaml", ".yml", ".json",
	".toml", ".xml", ".md", ".txt", ".sql", ".sh", ".bash",
	".vue", ".svelte", ".kt", ".swift", ".dart", ".scala",
	".proto", ".graphql", ".tf", ".dockerfile",
}

func isAlphaByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func countFileReferences(lower string) int {
	seen := make(map[string]bool)
	for _, ext := range fileRefExtensions {
		collectExtensionRefs(lower, ext, seen)
	}
	refCount := len(seen)
	if hasMultiSegmentPath(lower) {
		refCount++
	}
	return refCount
}

func validFilenameCharBefore(lower string, absPos int) bool {
	return absPos > 0 && isAlphaByte(lower[absPos-1])
}

func collectExtensionRefs(lower, ext string, seen map[string]bool) {
	idx := 0
	for {
		pos := strings.Index(lower[idx:], ext)
		if pos < 0 {
			return
		}
		absPos := idx + pos
		end := absPos + len(ext)
		idx = end
		if !validFilenameCharBefore(lower, absPos) {
			continue
		}
		if end < len(lower) {
			if next := lower[end]; next != 'x' && next != '/' && isAlphaByte(next) {
				continue
			}
		}

		start := absPos
		for start > 0 && isFilenameByte(lower[start-1]) {
			start--
		}
		for end < len(lower) && lower[end] == 'x' {
			end++
		}
		if filename := lower[start:end]; len(filename) > len(ext) {
			seen[filename] = true
		}
	}
}

func isFilenameByte(b byte) bool {
	switch {
	case b == '/' || b == '_' || b == '-' || b == '.':
		return true
	case b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	}
	return false
}

func stripURLs(s string) string {
	for _, prefix := range []string{"http://", "https://", "ftp://", "ssh://"} {
		for {
			idx := strings.Index(s, prefix)
			if idx < 0 {
				break
			}
			end := idx + len(prefix)
			for end < len(s) && s[end] != ' ' && s[end] != '\n' && s[end] != '\t' {
				end++
			}
			s = s[:idx] + s[end:]
		}
	}
	return s
}

func hasMultiSegmentPath(lower string) bool {
	if !strings.Contains(lower, "/") {
		return false
	}
	return strings.Count(stripURLs(lower), "/") >= 2
}

func countKeywordMatches(lower string, keywords []string) int {
	count := 0
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			count++
		}
	}
	return count
}
