package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Rule represents a learned harness rule extracted from agent errors.
type Rule struct {
	ID           string    `json:"id"`
	Category     string    `json:"category"`               // build | test | git | convention | security
	Rule         string    `json:"rule"`                   // human-readable rule text
	MatchPattern string    `json:"match_pattern"`          // regexp to match error OUTPUT (for auto-verify)
	ToolPattern  string    `json:"tool_pattern,omitempty"` // regexp to match tool ARGS (for injection)
	FixHint      string    `json:"fix_hint,omitempty"`     // optional actionable hint
	HitCount     int       `json:"hit_count"`
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
}

const defaultMaxRules = 60

// RuleStore manages harness rules persisted to .ggcode/agent-rules.json.
type RuleStore struct {
	mu       sync.Mutex
	path     string
	rules    []Rule
	loaded   bool
	maxRules int
}

// NewRuleStore creates a RuleStore for the given working directory.
func NewRuleStore(workingDir string) *RuleStore {
	if workingDir == "" {
		return nil
	}
	path := filepath.Join(workingDir, ".ggcode", "agent-rules.json")
	return &RuleStore{
		path:     path,
		maxRules: defaultMaxRules,
	}
}

func (rs *RuleStore) load() {
	if rs.loaded {
		return
	}
	rs.loaded = true
	data, err := os.ReadFile(rs.path)
	if err != nil {
		return
	}
	var store struct {
		Version int    `json:"version"`
		Rules   []Rule `json:"rules"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		debug.Log("ratchet", "failed to parse %s: %v", rs.path, err)
		return
	}
	rs.rules = store.Rules
}

func (rs *RuleStore) save() error {
	store := struct {
		Version int    `json:"version"`
		Rules   []Rule `json:"rules"`
	}{
		Version: 1,
		Rules:   rs.rules,
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(rs.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(rs.path, data, 0644)
}

// MatchErrors checks each error against existing rules. Returns matched
// (with updated hit_count) and unmatched errors.
func (rs *RuleStore) MatchErrors(errors []string) (matched []string, unmatched []string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	now := time.Now()
	changed := false

	for _, errMsg := range errors {
		found := false
		for i := range rs.rules {
			re, err := regexp.Compile(rs.rules[i].MatchPattern)
			if err != nil {
				continue
			}
			if re.MatchString(errMsg) {
				rs.rules[i].HitCount++
				rs.rules[i].LastSeen = now
				found = true
				matched = append(matched, errMsg)
				changed = true
				debug.Log("ratchet", "matched rule %s (hit_count=%d): %s",
					rs.rules[i].ID, rs.rules[i].HitCount, truncStr(errMsg, 80))
				break
			}
		}
		if !found {
			unmatched = append(unmatched, errMsg)
		}
	}

	if changed {
		_ = rs.save()
	}
	return matched, unmatched
}

// AddRule adds a new rule, enforcing the max limit with LRU eviction.
func (rs *RuleStore) AddRule(r Rule) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	r.ID = fmt.Sprintf("r-%s", shortUUID())
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	r.LastSeen = time.Now()
	if r.HitCount == 0 {
		r.HitCount = 1
	}

	rs.rules = append(rs.rules, r)
	rs.evict()
	_ = rs.save()
	debug.Log("ratchet", "added rule %s: %s (match=%s, tool=%s)", r.ID, r.Rule, r.MatchPattern, r.ToolPattern)
}

// Rules returns a copy of all rules.
func (rs *RuleStore) Rules() []Rule {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()
	result := make([]Rule, len(rs.rules))
	copy(result, rs.rules)
	return result
}

// MatchingRulesForTool returns rules whose category matches the tool name
// and whose ToolPattern (or MatchPattern as fallback) matches the tool
// arguments. Used for rule injection into tool results.
func (rs *RuleStore) MatchingRulesForTool(toolName, args string) []Rule {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	var result []Rule
	for _, r := range rs.rules {
		if !categoryMatchesTool(r.Category, toolName) {
			continue
		}
		// Use ToolPattern if available; fall back to MatchPattern for
		// backward compatibility with existing rules that only have one.
		pattern := r.ToolPattern
		if pattern == "" {
			pattern = r.MatchPattern
		}
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if re.MatchString(args) {
			result = append(result, r)
		}
	}
	return result
}

func categoryMatchesTool(category, toolName string) bool {
	switch category {
	case "build", "test":
		return toolName == "run_command" || toolName == "start_command"
	case "git":
		return strings.HasPrefix(toolName, "git_")
	case "convention":
		return toolName == "write_file" || toolName == "edit_file" || toolName == "multi_edit_file"
	case "security":
		return true
	default:
		return false
	}
}

// evict removes lowest-scoring rules when over the limit.
// score = hit_count * (1.0 / (1 + days_since_last_seen))
func (rs *RuleStore) evict() {
	if len(rs.rules) <= rs.maxRules {
		return
	}
	now := time.Now()
	type ruleScore struct {
		idx   int
		score float64
	}
	scores := make([]ruleScore, len(rs.rules))
	for i, r := range rs.rules {
		days := now.Sub(r.LastSeen).Hours() / 24
		scores[i] = ruleScore{i, float64(r.HitCount) * (1.0 / (1 + days))}
	}
	slices.SortFunc(scores, func(a, b ruleScore) int {
		if a.score < b.score {
			return -1
		}
		if a.score > b.score {
			return 1
		}
		return 0
	})
	evictCount := len(rs.rules) - rs.maxRules
	toEvict := make(map[int]bool)
	for i := 0; i < evictCount; i++ {
		toEvict[scores[i].idx] = true
	}
	var kept []Rule
	for i, r := range rs.rules {
		if !toEvict[i] {
			kept = append(kept, r)
		} else {
			debug.Log("ratchet", "evicted rule %s (score too low)", r.ID)
		}
	}
	rs.rules = kept
}

// --- LLM generalization ---

type ratchetResult struct {
	Action       string `json:"action"`        // "new" | "skip"
	Category     string `json:"category"`      // build | test | git | convention | security
	Rule         string `json:"rule"`          // generalized rule
	MatchPattern string `json:"match_pattern"` // regexp to match error OUTPUT
	ToolPattern  string `json:"tool_pattern"`  // regexp to match tool ARGS that trigger this error
	FixHint      string `json:"fix_hint"`      // actionable hint
	SkipReason   string `json:"skip_reason,omitempty"`
}

type ratchetLLMOutput struct {
	Results []ratchetResult `json:"results"`
}

const ratchetSystemPrompt = `You are a harness rule extractor. Your job is to analyze agent errors and extract generalized, actionable rules that will prevent similar errors in the future.

For each error, decide:
- "new": The error reveals a pattern worth remembering. Extract a rule.
- "skip": The error is transient (network timeout, rate limit) or too specific to generalize.

For "new" rules:
- category: One of "build", "test", "git", "convention", "security"
- rule: A concise, imperative rule (e.g., "All Go commands must use -tags goolm")
- match_pattern: A regexp that matches the error OUTPUT messages (e.g., "libolm.*C header|cannot find.*olm")
- tool_pattern: A regexp that matches the tool ARGS that would trigger this error (e.g., "go build|go test|go vet" for missing -tags flag). Leave empty if not applicable.
- fix_hint: A short actionable hint (e.g., "Add -tags goolm to the go command")

Rules should be GENERAL (applicable across sessions), not specific to one file.
The match_pattern should catch the CLASS of error, not just the exact message.
The tool_pattern should match the TOOL arguments (command string, file path) that lead to this error.

Respond with JSON only. No markdown, no explanation.`

// ProcessErrorsWithLLM sends unmatched errors to the agent's provider for
// generalization. Uses the current model. Called asynchronously after a run.
func (a *Agent) ProcessErrorsWithLLM(errors []string, existingRules []Rule) (*ratchetLLMOutput, error) {
	if len(errors) == 0 {
		return &ratchetLLMOutput{}, nil
	}
	a.mu.RLock()
	prov := a.provider
	a.mu.RUnlock()
	if prov == nil {
		return nil, fmt.Errorf("no provider available for ratchet")
	}

	// Build existing rules summary
	ruleSummaries := make([]string, len(existingRules))
	for i, r := range existingRules {
		ruleSummaries[i] = fmt.Sprintf("- [%s] %s (match: %s, tool: %s)", r.Category, r.Rule, r.MatchPattern, r.ToolPattern)
	}

	var userPrompt strings.Builder
	userPrompt.WriteString("Unmatched errors from the last run:\n\n")
	for i, e := range errors {
		fmt.Fprintf(&userPrompt, "%d. %s\n", i+1, e)
	}
	if len(ruleSummaries) > 0 {
		userPrompt.WriteString("\nExisting rules (avoid duplicates):\n")
		for _, s := range ruleSummaries {
			fmt.Fprintf(&userPrompt, "%s\n", s)
		}
	}
	userPrompt.WriteString("\nAnalyze each error and respond with JSON:\n")
	userPrompt.WriteString(`{"results":[{"action":"new","category":"build","rule":"...","match_pattern":"...","tool_pattern":"...","fix_hint":"..."},{"action":"skip","skip_reason":"..."}]}`)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := prov.Chat(ctx, []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: ratchetSystemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userPrompt.String()}}},
	}, nil)
	if err != nil || resp == nil {
		return nil, fmt.Errorf("ratchet LLM call failed: %w", err)
	}

	text := ""
	for _, b := range resp.Message.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}

	var output ratchetLLMOutput
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		debug.Log("ratchet", "failed to parse LLM output: %v (text=%s)", err, truncStr(text, 200))
		return nil, fmt.Errorf("ratchet: invalid LLM JSON: %w", err)
	}
	return &output, nil
}

// runRatchet is the full pipeline: match -> generalize with retry -> store.
// Called from reflection after a run with errors.
func (a *Agent) runRatchet(stats *RunStats) {
	if len(stats.Errors) == 0 {
		return
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	rs := NewRuleStore(workingDir)
	if rs == nil {
		return
	}

	matched, unmatched := rs.MatchErrors(stats.Errors)
	if len(unmatched) == 0 {
		return
	}

	debug.Log("ratchet", "processing %d unmatched errors (%d matched)", len(unmatched), len(matched))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	newRules := a.generalizeErrorsWithRetry(ctx, unmatched, "reflection")
	for _, r := range newRules {
		rs.AddRule(r)
	}
	debug.Log("ratchet", "learned %d new rules from reflection", len(newRules))
}

func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// generalizeErrorsWithRetry wraps ProcessErrorsWithLLM with retry logic.
// On final failure, logs a system-level debug message.
// Returns whatever rules were successfully generalized.
func (a *Agent) generalizeErrorsWithRetry(ctx context.Context, errors []string, verifyCmd string) []Rule {
	const maxRetries = 2
	rs := NewRuleStore(a.WorkingDir())
	existingRules := []Rule{}
	if rs != nil {
		existingRules = rs.Rules()
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		output, err := a.ProcessErrorsWithLLM(errors, existingRules)
		if err == nil && output != nil {
			var rules []Rule
			for _, result := range output.Results {
				if result.Action == "new" && result.Rule != "" && result.MatchPattern != "" {
					rules = append(rules, Rule{
						Category:     result.Category,
						Rule:         result.Rule,
						MatchPattern: result.MatchPattern,
						ToolPattern:  result.ToolPattern,
						FixHint:      result.FixHint,
					})
				}
			}
			if len(rules) > 0 {
				debug.Log("ratchet", "generalized %d rules from %d errors (attempt %d)", len(rules), len(errors), attempt+1)
			}
			return rules
		}
		lastErr = err
		if attempt < maxRetries {
			debug.Log("ratchet", "generalization attempt %d failed: %v, retrying...", attempt+1, err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
			}
		}
	}

	// All retries exhausted
	debug.Log("ratchet", "generalization failed after %d attempts for %d errors (cmd: %s): %v",
		maxRetries+1, len(errors), verifyCmd, lastErr)
	return nil
}

// shortUUID returns a random 8-char hex string for rule IDs.
func shortUUID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
