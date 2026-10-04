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

	"github.com/topcheer/ggcode/internal/util"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Rule represents a learned rule extracted from agent errors.
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
	// Source records where the rule was learned from. Empty (pre-r444)
	// and "error" mean the classic error-driven ratchet path;
	// "user_edit" (r444) means it was promoted from repeated user
	// rewrites of agent output. omitempty keeps old agent-rules.json
	// byte-compatible on rewrite.
	Source string `json:"source,omitempty"`
	// TaskType (intent filter) records the classifyTaskType label of the
	// run that learned this rule (bugfix/test/build/refactor/review/
	// feature). Empty (legacy or unclassified) rules stay always-eligible
	// via the global fallback in TopRulesForPromptFiltered. Mirrors the
	// playbook-side matchesIntent read-path gating (#3266 research P1:
	// unfiltered top-N injection polluted bugfix runs with release/test
	// lessons and left the two memory systems inconsistent).
	TaskType string `json:"task_type,omitempty"`
}

const defaultMaxRules = 60

// Staleness thresholds for ratchet rule lifecycle.
const (
	// staleRuleThreshold: rules not hit in this duration are considered stale.
	staleRuleThreshold = 30 * 24 * time.Hour // 30 days
	// staleRuleMinHits: rules with HitCount >= this are preserved even if stale.
	staleRuleMinHits = 3
)

// RuleStore manages harness rules persisted to .ggcode/agent-rules.json.
type RuleStore struct {
	mu         sync.Mutex
	path       string
	rules      []Rule
	loaded     bool
	maxRules   int
	regexCache map[string]*regexp.Regexp // pre-compiled patterns to avoid repeated compilation
	// runIntent is the classifyTaskType label of the current run, set
	// from prompt injection each run and inherited by newly added rules
	// whose TaskType is empty (guarded by mu).
	runIntent string
}

// NewRuleStore creates a RuleStore for the given working directory.
func NewRuleStore(workingDir string) *RuleStore {
	if workingDir == "" {
		return nil
	}
	path := filepath.Join(workingDir, ".ggcode", "agent-rules.json")
	return &RuleStore{
		path:       path,
		maxRules:   defaultMaxRules,
		regexCache: make(map[string]*regexp.Regexp),
	}
}

// getRuleStore returns THE single cached RuleStore for the agent's
// working directory. The store is created once and reused across ALL
// consumers (hot-path rule injection, runRatchet, generalizeErrorsWithRetry,
// asyncVerify, prompt injection) so there is exactly one in-memory truth
// per agent.
//
// #3227: previously runRatchet/generalizeErrorsWithRetry/asyncVerify each
// built their own NewRuleStore; every instance load()s ONCE and save()
// overwrites the file with that frozen snapshot, so a long-lived cached
// instance (held by the user-edit observer) silently erased everything the
// per-run instances learned - a classic cross-instance lost-update. A
// single shared instance makes rs.mu cover every read/write by
// construction.
//
// The cache write is guarded by a.mu because asyncVerify reaches this from
// a background goroutine. Note the single-instance trade-off, deliberate:
// rules are loaded once per agent session and accumulate in memory;
// hand-edits to agent-rules.json mid-session are no longer re-read per run
// (per-run reload was exactly the lost-update vector).
func (a *Agent) getRuleStore() *RuleStore {
	// a.mu (RWMutex) guards the cache because asyncVerify reaches this
	// from a background goroutine. Read a.workingDir UNDER the lock -
	// calling a.WorkingDir() here would self-deadlock (it takes a.mu.RLock,
	// and Go mutexes are not reentrant).
	a.mu.Lock()
	if a.ruleStore != nil {
		rs := a.ruleStore
		a.mu.Unlock()
		return rs
	}
	workingDir := a.workingDir
	if workingDir == "" {
		a.mu.Unlock()
		return nil
	}
	rs := NewRuleStore(workingDir)
	a.ruleStore = rs // cache for future calls
	a.mu.Unlock()
	return rs
}

// resetRuleStoreLocked drops the cached RuleStore singleton (and the
// user-edit observer bound to it) so the next getRuleStore call lazily
// re-anchors to the CURRENT working directory.
// #3233: the #3227 singleton froze the store at first-build; when the
// working dir changes mid-session (enter_worktree adoption, SetWorkingDir)
// rules learned inside a worktree were persisted back into the MAIN
// tree's agent-rules.json - cross-branch pollution of persistent
// guidance. Callers must hold a.mu.
func (a *Agent) resetRuleStoreLocked() {
	a.ruleStore = nil
	// The observer holds a direct reference to the old store; without
	// clearing it too, user-edit tracking would keep writing to the
	// abandoned directory.
	a.userEditObs = nil
}

// compilePattern returns a cached compiled regex for the given pattern.
// This avoids re-compiling the same patterns on every tool call.
func (rs *RuleStore) compilePattern(pattern string) (*regexp.Regexp, error) {
	if re, ok := rs.regexCache[pattern]; ok {
		return re, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	rs.regexCache[pattern] = re
	return re, nil
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
	return util.AtomicWriteFile(rs.path, data, 0644)
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
			re, err := rs.compilePattern(rs.rules[i].MatchPattern)
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
		if err := rs.save(); err != nil {
			debug.Log("ratchet", "failed to save rules after match: %v", err)
		}
	}
	return matched, unmatched
}

// AddRule adds a new rule, enforcing the max limit with LRU eviction.
// If a semantically similar rule already exists, it merges hit counts
// instead of creating a duplicate.
// RemoveUserEditRules deletes user_edit-sourced rules whose ToolPattern
// targets base (r447 negative-signal recycling). Returns how many were
// removed. Error-sourced and LLM rules are never touched.
func (rs *RuleStore) RemoveUserEditRules(base string) int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()
	pattern := regexp.QuoteMeta(base)
	kept := make([]Rule, 0, len(rs.rules))
	removed := 0
	for _, r := range rs.rules {
		if r.Source == ruleSourceUserEdit && r.ToolPattern == pattern {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	if removed > 0 {
		rs.rules = kept
		if err := rs.save(); err != nil {
			debug.Log("ratchet", "failed to save rules after removing %d user_edit rule(s) for %s: %v", removed, base, err)
		}
	}
	return removed
}

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
	// Inherit the current run's intent so future injections can filter
	// by task type (legacy rules with empty TaskType stay global).
	if r.TaskType == "" && rs.runIntent != "" && rs.runIntent != "other" {
		r.TaskType = rs.runIntent
	}
	// #1008: Category comes straight from LLM JSON output, where the
	// prompt's enum (build/test/git/convention/security) is only a soft
	// constraint - variants like "Build", "lint" or " build " slip through.
	// Go switch is case-sensitive and categoryMatchesTool defaults to
	// false, so a non-canonical category made the rule never match any
	// tool: preventive injection silently dead, /rules counted it but
	// never listed it, and it still accrued HitCount to squeeze canonical
	// rules out of the 60-slot LRU. Normalize (trim + lower) and fall back
	// to "convention" for anything unrecognized.
	r.Category = normalizeRuleCategory(r.Category)

	// Check for semantic duplicates before adding.
	// #3229: user_edit rules generated from a fixed template tokenize to
	// near-identical token sets for ANY two files (Jaccard >= 0.75 always),
	// but their identity IS the target file. Similarity merge must never
	// collapse two distinct files - that silently discards every rule after
	// the first file, degrading r444 to single-file mode.
	for i := range rs.rules {
		if userEditIdentityDistinct(rs.rules[i], r) {
			continue
		}
		if ruleSimilarity(rs.rules[i], r) >= 0.75 {
			// Merge into existing rule: bump hit count and update LastSeen
			rs.rules[i].HitCount += r.HitCount
			rs.rules[i].LastSeen = r.LastSeen
			debug.Log("ratchet", "merged duplicate rule into %s (similarity >= 0.75): %s", rs.rules[i].ID, r.Rule)
			if err := rs.save(); err != nil {
				debug.Log("ratchet", "failed to save rules after merge: %v", err)
			}
			return
		}
	}

	rs.rules = append(rs.rules, r)
	rs.evict()
	if err := rs.save(); err != nil {
		debug.Log("ratchet", "failed to save rules after adding rule %s: %v", r.ID, err)
	}
	debug.Log("ratchet", "added rule %s: %s (match=%s, tool=%s)", r.ID, r.Rule, r.MatchPattern, r.ToolPattern)
}

// ruleSimilarity returns a 0-1 similarity score between two rules based on
// Jaccard similarity of normalized word tokens from their Rule text and
// MatchPattern. If both rules share the same ToolPattern, a bonus is applied
// since rules targeting the same tool with overlapping keywords are very
// likely semantic duplicates. Used to prevent near-duplicate rules from
// accumulating.
func ruleSimilarity(a, b Rule) float64 {
	tokensA := tokenizeRule(a)
	tokensB := tokenizeRule(b)
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0
	}
	// #3229 guard: two user_edit rules bound to DIFFERENT files are never
	// semantic duplicates regardless of token overlap - see AddRule.
	if userEditIdentityDistinct(a, b) {
		return 0
	}
	// Jaccard similarity: intersection / union
	intersection := 0
	for t := range tokensA {
		if tokensB[t] {
			intersection++
		}
	}
	union := len(tokensA) + len(tokensB) - intersection
	base := float64(intersection) / float64(union)

	// If both rules target the same tool pattern, boost similarity.
	// Rules like "run gofmt before commit" vs "run gofmt before staging"
	// have low Jaccard but are clearly the same concept.
	if a.ToolPattern == b.ToolPattern && a.ToolPattern != "" {
		base += 0.3
	}
	return base
}

// userEditIdentityDistinct reports whether a and b are both user_edit
// rules (r444) bound to DIFFERENT target files. user_edit rules come from
// a fixed template whose only variable is the file base name, so any two
// of them tokenize to near-identical sets (Jaccard 0.8+ for ANY file pair)
// while carrying distinct identities in ToolPattern. Similarity-based
// dedup must skip them (#3229): merging silently discarded every rule
// after the first file and mis-accumulated HitCount onto it.
func userEditIdentityDistinct(a, b Rule) bool {
	return a.Source == ruleSourceUserEdit && b.Source == ruleSourceUserEdit &&
		a.ToolPattern != b.ToolPattern
}

// tokenizeRule extracts normalized lowercase word tokens from a rule,
// combining Rule text and MatchPattern. Filters common stopwords.
func tokenizeRule(r Rule) map[string]bool {
	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "to": true, "is": true,
		"are": true, "in": true, "on": true, "for": true, "of": true,
		"and": true, "or": true, "before": true, "after": true,
		"with": true, "that": true, "this": true, "it": true,
		"from": true, "by": true, "be": true, "not": true,
		"will": true, "was": true, "all": true, "if": true,
		"use": true, "using": true, "used": true, "ensure": true,
	}
	text := strings.ToLower(r.Rule + " " + r.MatchPattern + " " + r.ToolPattern)
	// Split on non-alphanumeric
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	})
	tokens := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f == "" || stopwords[f] {
			continue
		}
		tokens[f] = true
	}
	return tokens
}

// DeduplicateRules removes semantically similar rules from the store,
// merging hit counts into the higher-hit-count rule. This is intended
// for periodic cleanup of accumulated near-duplicates.
func (rs *RuleStore) DeduplicateRules() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	if len(rs.rules) <= 1 {
		return 0
	}

	merged := 0
	kept := []Rule{rs.rules[0]}

	for i := 1; i < len(rs.rules); i++ {
		isDup := false
		for j := range kept {
			sim := ruleSimilarity(kept[j], rs.rules[i])
			if sim >= 0.55 {
				// Merge into the kept rule: sum hit counts, keep latest LastSeen.
				// #1011: the previous unconditional assignment ran BEFORE this
				// comparison, making the After check dead (both sides equal) and
				// silently inverting the semantics to last-wins - a stale
				// duplicate's LastSeen overwrote the kept rule's fresh one,
				// tanking recency-weighted scores and eviction order.
				kept[j].HitCount += rs.rules[i].HitCount
				if rs.rules[i].LastSeen.After(kept[j].LastSeen) {
					kept[j].LastSeen = rs.rules[i].LastSeen
				}
				merged++
				isDup = true
				debug.Log("ratchet", "dedup merged #%d into #%d (sim=%.2f)", i+1, j+1, sim)
				break
			}
		}
		if !isDup {
			kept = append(kept, rs.rules[i])
		}
	}

	if merged > 0 {
		rs.rules = kept
		if err := rs.save(); err != nil {
			debug.Log("ratchet", "failed to save after dedup: %v", err)
		}
		debug.Log("ratchet", "dedup removed %d duplicate rules (%d → %d)", merged, len(rs.rules)+merged, len(kept))
	}
	return merged
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

// TopRulesForPrompt returns a concise summary of the most relevant rules
// for proactive injection into the system prompt. Rules are scored by a
// combined metric of HitCount and recency: recent high-hit rules rank
// highest, while stale rules (not seen in staleRuleThreshold) are heavily
// penalized. Only includes rules that have been hit at least once
// (HitCount > 0), limited to maxRules entries.
//
// This implements the "rules as first-class citizens" pattern from the
// self-improving agent literature: learned rules should be visible to the
// agent from the start of each run, not just reactively when a tool pattern
// matches. The recency weighting follows arXiv:2603.07670 which emphasizes
// that production memory systems must account for staleness.
func (rs *RuleStore) TopRulesForPrompt(maxRules int) string {
	return rs.topRulesForPrompt(maxRules, "")
}

// SetRunIntent records the classifyTaskType label of the current run so
// newly added rules inherit it (TaskType tagging) and prompt injection
// can filter by relevance. Called once per run from prompt assembly.
func (rs *RuleStore) SetRunIntent(intent string) {
	rs.mu.Lock()
	rs.runIntent = intent
	rs.mu.Unlock()
}

// TopRulesForPromptFiltered returns the top rules for prompt injection,
// restricted to rules learned in runs of the same task type when that
// subset is rich enough (>= 2 entries). This mirrors the playbook-side
// matchesIntent gating: a bugfix run should not spend its 5 injection
// slots on release/test lessons, and vice versa. Falls back to the
// global top-N when the intent subset is too thin (anti-starvation;
// legacy rules with empty TaskType also live in the global pool).
func (rs *RuleStore) TopRulesForPromptFiltered(maxRules int, intent string) string {
	return rs.topRulesForPrompt(maxRules, intent)
}

// topRulesForPrompt is the shared implementation. filterIntent, when
// non-empty and not "other", prefers rules whose TaskType matches; the
// filtered view is used only if it holds at least 2 entries, otherwise
// the unfiltered ranking applies.
func (rs *RuleStore) topRulesForPrompt(maxRules int, filterIntent string) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.load()

	if len(rs.rules) == 0 {
		return ""
	}

	now := time.Now()
	type ruleScore struct {
		rule  string
		hint  string
		score float64
	}
	var active, intentActive []ruleScore
	useIntent := filterIntent != "" && filterIntent != "other"
	for _, r := range rs.rules {
		if r.HitCount <= 0 {
			continue
		}
		sc := ruleScore{
			rule:  r.Rule,
			hint:  r.FixHint,
			score: recencyWeightedScore(r.HitCount, r.LastSeen, now),
		}
		active = append(active, sc)
		if useIntent && r.TaskType == filterIntent {
			intentActive = append(intentActive, sc)
		}
	}
	if len(active) == 0 {
		return ""
	}

	// Intent view is only adopted when the same-type subset is rich
	// enough to fill a meaningful prompt slice; otherwise fall back to
	// the global ranking so thin stores keep injecting their best rules.
	if useIntent && len(intentActive) >= 2 {
		active = intentActive
	}

	// Sort by combined score descending (small N, insertion sort)
	for i := 1; i < len(active); i++ {
		for j := i; j > 0 && active[j].score > active[j-1].score; j-- {
			active[j], active[j-1] = active[j-1], active[j]
		}
	}

	if maxRules > 0 && len(active) > maxRules {
		active = active[:maxRules]
	}

	var b strings.Builder
	b.WriteString("Lessons from previous runs in this workspace:\n")
	for _, a := range active {
		b.WriteString(fmt.Sprintf("- %s", a.rule))
		if a.hint != "" {
			b.WriteString(fmt.Sprintf(" → %s", a.hint))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// recencyWeightedScore computes a combined score from hit count and recency.
// score = hitCount * recencyWeight, where recencyWeight decays based on
// days since last seen: weight = 1.0 / (1 + daysSinceLastSeen/7).
// Rules seen very recently get weight ~1.0; rules seen 7 days ago get ~0.5;
// rules seen 30+ days ago get < 0.2, effectively deprioritizing stale rules.
func recencyWeightedScore(hitCount int, lastSeen, now time.Time) float64 {
	daysSince := now.Sub(lastSeen).Hours() / 24
	if daysSince < 0 {
		daysSince = 0
	}
	recencyWeight := 1.0 / (1.0 + daysSince/7.0)
	return float64(hitCount) * recencyWeight
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
		// #1485: no fallback to MatchPattern here. MatchPattern is documented
		// (and used by the reactive path) as matching error OUTPUT; running it
		// against tool ARGS is a domain mismatch that both dead-ended the
		// preventive injection for output-shaped rules and occasionally
		// injected unrelated build hints into commands whose args merely
		// contained phrases like "command not found". A rule joins the
		// preventive path only via an explicit ToolPattern.
		pattern := r.ToolPattern
		if pattern == "" {
			continue
		}
		re, err := rs.compilePattern(pattern)
		if err != nil {
			continue
		}
		if re.MatchString(args) {
			result = append(result, r)
		}
	}
	return result
}

// normalizeRuleCategory canonicalizes a rule category coming from LLM JSON
// output: trim, lowercase, and fall back to "convention" for anything not
// in the canonical set. #1008: without this, variants like "Build"/"lint"/
// " build " never matched any tool (categoryMatchesTool default->false),
// silently killing preventive injection while /rules counted but never
// listed them.
func normalizeRuleCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	switch cat {
	case "build", "test", "git", "convention", "security":
		return cat
	default:
		return "convention"
	}
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
func (a *Agent) ProcessErrorsWithLLM(parentCtx context.Context, errors []string, existingRules []Rule) (*ratchetLLMOutput, error) {
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

	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	resp, err := prov.Chat(ctx, []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: ratchetSystemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: userPrompt.String()}}},
	}, nil)
	if err != nil || resp == nil {
		return nil, fmt.Errorf("ratchet LLM call failed: %w", err)
	}
	a.emitUsageWithSource(resp.Usage, "ratchet")

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
	rs := a.getRuleStore() // #3227: shared singleton - per-run instances lost updates
	if rs == nil {
		return
	}

	matched, unmatched := rs.MatchErrors(stats.Errors)
	if len(unmatched) == 0 {
		return
	}

	debug.Log("ratchet", "processing %d unmatched errors (%d matched)", len(unmatched), len(matched))

	// Use the agent's shutdown context as parent so reflection is cancelled
	// when the agent shuts down, instead of an uncancellable context.Background().
	parentCtx := a.shutdownCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, 60*time.Second)
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
	rs := a.getRuleStore() // #3227: shared singleton - per-run instances lost updates
	existingRules := []Rule{}
	if rs != nil {
		existingRules = rs.Rules()
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		output, err := a.ProcessErrorsWithLLM(ctx, errors, existingRules)
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
