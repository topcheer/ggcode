package harness

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// RouteDecision represents the routing decision for a user prompt.
type RouteDecision int

const (
	// RouteNone means harness routing is disabled; do not interfere.
	RouteNone RouteDecision = iota
	// RouteNormal means the prompt should go through the normal agent path.
	RouteNormal
	// RouteSuggest means the prompt looks like a code-change task, but the
	// system should ask the user before routing to harness.
	RouteSuggest
	// RouteHarness means the prompt should be routed directly to harness.
	RouteHarness
)

func (d RouteDecision) String() string {
	switch d {
	case RouteNone:
		return "none"
	case RouteNormal:
		return "normal"
	case RouteSuggest:
		return "suggest"
	case RouteHarness:
		return "harness"
	default:
		return "unknown"
	}
}

// RouteContext provides contextual signals beyond the raw input text.
type RouteContext struct {
	// Input is the raw user prompt.
	Input string
	// HasSelection indicates the user selected code in the editor before
	// submitting the prompt.
	HasSelection bool
	// RecentFiles lists file paths the user has recently interacted with.
	RecentFiles []string
	// ProjectHasHarness is true when a harness.yaml was found for the project.
	ProjectHasHarness bool
	// WorkingDir is the current working directory, used for project discovery
	// and auto-init. If empty, defaults to os.Getwd().
	WorkingDir string
}

// PromptFeatures are structural features extracted from a user prompt.
// These are used for deterministic routing decisions.
type PromptFeatures struct {
	// HasFilePath is true when the prompt contains a file path with extension
	// (e.g. "auth.go", "src/api/user.ts").
	HasFilePath bool
	// HasCodeBlock is true when the prompt contains ``` or indented code.
	HasCodeBlock bool
	// HasActionVerb is true when the prompt contains an action verb that
	// implies code modification (add, fix, remove, refactor, create, etc.).
	HasActionVerb bool
	// IsQuestionOnly is true when the prompt is purely interrogative — it
	// ends with a question mark and contains no action verbs.
	IsQuestionOnly bool
	// HasTaskGoal is true when the prompt describes a specific goal with
	// concrete scope (e.g. "a deleteUser method", "error handling for auth").
	HasTaskGoal bool
	// IsTooShort is true when the prompt is under 10 characters.
	IsTooShort bool
	// ExplicitExclude is true when the user explicitly says not to modify code.
	ExplicitExclude bool
}

// actionVerbPatterns matches verbs commonly used when requesting code changes.
// These are matched against the beginning of words in the prompt.
var actionVerbPatterns = []string{
	"add", "create", "implement", "write", "build", "make",
	"fix", "repair", "patch", "resolve", "solve",
	"remove", "delete", "drop", "eliminate",
	"update", "change", "modify", "edit", "rename", "replace",
	"refactor", "restructure", "reorganize", "rearrange",
	"migrate", "upgrade", "port",
	"optimize", "improve", "enhance", "simplify", "clean up", "cleanup",
	"extract", "move", "split", "merge", "combine",
	"generate", "scaffold", "init",
	// Chinese equivalents
	"添加", "新增", "创建", "实现", "编写",
	"修复", "解决", "改正",
	"删除", "移除",
	"修改", "更新", "重命名", "替换",
	"重构", "优化", "改进", "简化", "清理",
	"提取", "移动", "拆分", "合并",
	"生成", "初始化",
}

// excludePatterns matches phrases that explicitly indicate the user does NOT
// want code changes.
var excludePatterns = []string{
	"don't change", "do not change", "不要修改", "不要改",
	"just explain", "只是解释", "只是说明",
	"no changes", "不要动", "don't modify",
	"only explain", "only analyze", "只分析", "只解释",
}

// filePathPattern matches common source file paths with extensions.
var filePathPattern = regexp.MustCompile(`[\w./\-]+\.(go|py|js|ts|tsx|jsx|rs|java|rb|c|cpp|h|hpp|cs|swift|kt|scala|sh|bash|zsh|yaml|yml|toml|json|xml|html|css|scss|sql|md|proto|graphql|tf)`)

// codeBlockPattern matches ``` fenced code blocks.
var codeBlockPattern = regexp.MustCompile("```")

// taskGoalIndicators matches phrases that indicate a specific, scoped goal.
var taskGoalIndicators = []string{
	"a ", "an ", "the ", "for ", "of ", "in ",
	"method", "function", "class", "module", "file", "handler",
	"test", "tests", "spec", "specs", "coverage",
	"API", "api", "endpoint", "route",
	"error handling", "validation", "logging",
	"单元测试", "方法", "函数", "模块", "接口",
}

// ExtractFeatures analyzes a prompt and returns its structural features.
func ExtractFeatures(input string) PromptFeatures {
	input = strings.TrimSpace(input)
	normalized := normalizePrompt(input)

	return PromptFeatures{
		HasFilePath:     filePathPattern.MatchString(normalized),
		HasCodeBlock:    codeBlockPattern.MatchString(input),
		HasActionVerb:   hasActionVerb(normalized),
		IsQuestionOnly:  isQuestionOnly(normalized),
		HasTaskGoal:     hasTaskGoal(normalized),
		IsTooShort:      len([]rune(input)) < 10,
		ExplicitExclude: hasExcludePattern(normalized),
	}
}

// DecideRoute determines whether a user prompt should be routed to harness,
// using a three-layer deterministic classifier.
//
// Layer 1: Exclusion — signals that clearly indicate no routing.
// Layer 2: Structural features — file paths, action verbs, task goals.
// Layer 3: Conservative default — ambiguous cases go to normal agent.
//
// The mode parameter controls the decision for detected code-change tasks:
//   - "off":     Never route.
//   - "suggest": Route to RouteSuggest (ask user).
//   - "on"/"strict": Route to RouteHarness.
func DecideRoute(input string, mode string) RouteDecision {
	input = strings.TrimSpace(input)
	if input == "" {
		return RouteNormal
	}

	features := ExtractFeatures(input)

	// Layer 1: Exclusion — definite non-routing signals.
	if features.IsTooShort {
		return RouteNormal
	}
	if strings.HasPrefix(input, "/") {
		return RouteNormal // slash command
	}
	if strings.HasPrefix(input, "$") || strings.HasPrefix(input, ">") {
		return RouteNormal // shell directive
	}
	if features.ExplicitExclude {
		return RouteNormal
	}

	// Check mode first — unknown modes are treated as "off".
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "off" || (mode != "suggest" && mode != "on" && mode != "strict") {
		return RouteNone
	}

	// Layer 2: Structural feature scoring.
	score := 0
	if features.HasFilePath {
		score += 2
	}
	if features.HasActionVerb {
		score += 2
	}
	if features.HasTaskGoal {
		score += 1
	}
	if features.HasCodeBlock {
		score += 1
	}

	// Strong question signal without action → not a code task
	if features.IsQuestionOnly && !features.HasActionVerb {
		return RouteNormal
	}

	// High confidence: multiple structural signals indicate a code task.
	// Threshold: score >= 3 means at least filepath+verb or verb+goal+code.
	if score >= 3 {
		switch mode {
		case "suggest":
			return RouteSuggest
		case "on", "strict":
			return RouteHarness
		}
	}

	// Layer 3: Ambiguous — conservative, go to normal agent.
	return RouteNormal
}

// DecideRouteWithFeatures is like DecideRoute but accepts pre-extracted
// features and route context for richer decision making.
func DecideRouteWithFeatures(input string, mode string, features PromptFeatures, ctx RouteContext) RouteDecision {
	input = strings.TrimSpace(input)
	if input == "" {
		return RouteNormal
	}

	// Layer 1: Exclusion
	if features.IsTooShort {
		return RouteNormal
	}
	if strings.HasPrefix(input, "/") {
		return RouteNormal
	}
	if strings.HasPrefix(input, "$") || strings.HasPrefix(input, ">") {
		return RouteNormal
	}
	if features.ExplicitExclude {
		return RouteNormal
	}

	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "off" || (mode != "suggest" && mode != "on" && mode != "strict") {
		return RouteNone
	}

	// Context boost: selection + action verb = strong signal
	if ctx.HasSelection && features.HasActionVerb {
		switch mode {
		case "suggest":
			return RouteSuggest
		case "on", "strict":
			return RouteHarness
		}
	}

	// Structural scoring
	score := 0
	if features.HasFilePath {
		score += 2
	}
	if features.HasActionVerb {
		score += 2
	}
	if features.HasTaskGoal {
		score += 1
	}
	if features.HasCodeBlock {
		score += 1
	}
	if ctx.HasSelection {
		score += 1
	}

	if features.IsQuestionOnly && !features.HasActionVerb {
		return RouteNormal
	}

	if score >= 3 {
		switch mode {
		case "suggest":
			return RouteSuggest
		case "on", "strict":
			return RouteHarness
		}
	}

	return RouteNormal
}

// normalizePrompt lowercases and trims whitespace for pattern matching.
func normalizePrompt(input string) string {
	// Lower case for matching
	lowered := strings.ToLower(input)
	// Remove extra whitespace
	return strings.Join(strings.Fields(lowered), " ")
}

// hasActionVerb checks if the prompt contains any action verb.
func hasActionVerb(normalized string) bool {
	words := strings.Fields(normalized)
	for _, word := range words {
		cleaned := strings.TrimFunc(word, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
		})
		for _, verb := range actionVerbPatterns {
			if cleaned == verb || strings.HasPrefix(cleaned, verb) {
				return true
			}
		}
	}
	// Also check multi-word verbs
	for _, verb := range actionVerbPatterns {
		if strings.Contains(normalized, verb) && len(verb) > 2 {
			// Only match multi-char verbs as substrings
			if len([]rune(verb)) > 2 {
				return true
			}
		}
	}
	return false
}

// isQuestionOnly checks if the prompt is purely a question.
func isQuestionOnly(normalized string) bool {
	// Must end with ? or ？ to be considered a question
	if !strings.HasSuffix(normalized, "?") && !strings.HasSuffix(normalized, "？") {
		return false
	}
	// Must not contain action verbs
	return !hasActionVerb(normalized)
}

// hasTaskGoal checks for indicators of a specific scoped goal.
func hasTaskGoal(normalized string) bool {
	for _, indicator := range taskGoalIndicators {
		if strings.Contains(normalized, indicator) {
			return true
		}
	}
	return false
}

// hasExcludePattern checks for explicit exclusion phrases.
func hasExcludePattern(normalized string) bool {
	for _, pattern := range excludePatterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

// IsFilePath checks if a string looks like a source file path.
func IsFilePath(s string) bool {
	ext := strings.ToLower(filepath.Ext(s))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".java", ".rb",
		".c", ".cpp", ".h", ".hpp", ".cs", ".swift", ".kt", ".scala",
		".sh", ".bash", ".zsh", ".yaml", ".yml", ".toml", ".json", ".xml",
		".html", ".css", ".scss", ".sql", ".md", ".proto", ".graphql", ".tf":
		return true
	}
	return false
}
