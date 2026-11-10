package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ghRunFunc is the injectable gh CLI runner (defaults to the shared ghRun
// helper used by ci_status). Tests inject fakes to pin predicate logic.
type ghRunFunc func(ctx context.Context, dir string, args ...string) (string, error)

// GatesAccess is the configuration+session surface the merge_preflight
// tool needs beyond gh queries. It is implemented by the runtime config
// access layer so CRUD goes through the existing config persistence path.
type GatesAccess interface {
	// Describe renders the current gates configuration (as configured,
	// defaults resolved) as JSON.
	Describe() (string, error)
	// Set validates and persists a single gates key ("enabled",
	// "commands", "hold_patterns", "preflight_ttl"); value is a JSON value.
	Set(key, value string) error
	// Unset resets a scalar key to its default, or removes a list entry
	// by value. Empty value on a list key is an error.
	Unset(key, value string) error
	// Test dry-runs a command against the protected patterns and reports
	// match + preflight snapshot freshness. It performs no writes.
	Test(command string) (string, error)
	// HoldPatterns returns the compiled hold regexes currently in force
	// (nil/empty when none configured). Compile errors are surfaced.
	HoldPatterns() ([]*regexp.Regexp, error)
}

// MergePreflightTool is the read-only lifecycle preflight check for PR
// merge/close commands: it queries gh (read-only) and evaluates four
// predicates, plus it exposes describe/set/unset/test actions for the
// gates configuration (LLM channel b).
type MergePreflightTool struct {
	WorkingDir string
	// GHRun is the gh CLI runner; nil uses the shared ghRun helper.
	GHRun ghRunFunc
	// Access provides gates config CRUD + session snapshot state; nil
	// means CRUD actions are unavailable (preflight still works with
	// default hold patterns).
	Access GatesAccess
}

func (t MergePreflightTool) Name() string { return "merge_preflight" }

func (t MergePreflightTool) Description() string {
	return "Read-only merge/close lifecycle preflight for a pull request, plus gates " +
		"configuration management. With pr_number: queries gh (read-only) and evaluates " +
		"head_pinned (current head SHA to pin via --match-head-commit), checks_green " +
		"(all checks SUCCESS on the current head), reviews_resolved (no outstanding " +
		"CHANGES_REQUESTED review), holds_clear (no comment/label matches the configured " +
		"hold regexes), concluding 'PREFLIGHT: PASS|HOLD(reasons)'. The first output line " +
		"is machine-readable JSON. With action: describe|set|unset|test manages the " +
		"gates.merge.* configuration (dual channel: the generic config tool also reads " +
		"and writes the same keys)."
}

func (t MergePreflightTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pr_number": {
				"type": "integer",
				"description": "PR number to preflight (mutually exclusive with action)."
			},
			"action": {
				"type": "string",
				"enum": ["describe", "set", "unset", "test"],
				"description": "Gates configuration management: describe (current config), set (key+value, value is a JSON value), unset (key, plus value for list entries removed by value; scalars reset to default), test (command dry-run: protection match + preflight freshness)."
			},
			"key": {
				"type": "string",
				"description": "For set/unset: one of enabled, commands, hold_patterns, preflight_ttl (gates.merge. scope)."
			},
			"value": {
				"type": "string",
				"description": "For set: the JSON value for the key (e.g. 'false', '[\"gh pr merge\"]', '\"90s\"'). For unset of a list entry: the entry to remove."
			},
			"command": {
				"type": "string",
				"description": "For action=test: the shell command to dry-run against the protected patterns."
			},
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

// preflightReport is the machine-readable first line of a preflight
// result. The agent-side lifecycle gate parses this to record PASS
// snapshots (verdict=="PASS") keyed by PR number with the head SHA.
type preflightReport struct {
	PR              int      `json:"pr"`
	HeadSHA         string   `json:"head_sha"`
	HeadPinned      bool     `json:"head_pinned"`
	ChecksGreen     bool     `json:"checks_green"`
	ReviewsResolved bool     `json:"reviews_resolved"`
	HoldsClear      bool     `json:"holds_clear"`
	Verdict         string   `json:"verdict"`
	Reasons         []string `json:"reasons,omitempty"`
	CheckedAt       string   `json:"checked_at"`
}

func (t MergePreflightTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		PRNumber int    `json:"pr_number"`
		Action   string `json:"action"`
		Key      string `json:"key"`
		Value    string `json:"value"`
		Command  string `json:"command"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Action != "" {
		return t.runAction(args.Action, args.Key, args.Value, args.Command)
	}
	if args.PRNumber <= 0 {
		return Result{IsError: true, Content: "pr_number is required (or pass action=describe|set|unset|test)."}, nil
	}
	return t.runPreflight(ctx, args.PRNumber, resolveDir(args.Path, t.WorkingDir))
}

func (t MergePreflightTool) gh(ctx context.Context, dir string, argv ...string) (string, error) {
	run := t.GHRun
	if run == nil {
		run = ghRun
	}
	return run(ctx, dir, argv...)
}

func (t MergePreflightTool) runAction(action, key, value, command string) (Result, error) {
	if t.Access == nil {
		return Result{IsError: true, Content: "gates configuration management is unavailable in this runtime (preflight by pr_number still works)."}, nil
	}
	switch action {
	case "describe":
		out, err := t.Access.Describe()
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: out}, nil
	case "set":
		if strings.TrimSpace(key) == "" {
			return Result{IsError: true, Content: "action=set requires key (enabled, commands, hold_patterns, preflight_ttl)."}, nil
		}
		if value == "" {
			return Result{IsError: true, Content: "action=set requires value (a JSON value, e.g. false, [\"gh pr merge\"], \"90s\")."}, nil
		}
		if err := t.Access.Set(key, value); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("set gates.merge.%s rejected: %v", key, err)}, nil
		}
		return Result{Content: fmt.Sprintf("Set gates.merge.%s = %s\n", key, value)}, nil
	case "unset":
		if strings.TrimSpace(key) == "" {
			return Result{IsError: true, Content: "action=unset requires key (enabled, commands, hold_patterns, preflight_ttl)."}, nil
		}
		if err := t.Access.Unset(key, value); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("unset gates.merge.%s rejected: %v", key, err)}, nil
		}
		return Result{Content: fmt.Sprintf("Unset gates.merge.%s\n", key)}, nil
	case "test":
		if strings.TrimSpace(command) == "" {
			return Result{IsError: true, Content: "action=test requires command."}, nil
		}
		out, err := t.Access.Test(command)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		return Result{Content: out}, nil
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q (use describe, set, unset, test).", action)}, nil
	}
}

func (t MergePreflightTool) runPreflight(ctx context.Context, prNumber int, dir string) (Result, error) {
	report := preflightReport{PR: prNumber, CheckedAt: time.Now().UTC().Format(time.RFC3339)}

	// Resolve the repo through the injectable runner so tests (and any
	// alternate transport) never depend on a real gh CLI.
	repoOut, err := t.gh(ctx, dir, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("Not a GitHub repository or gh not authenticated: %v", err)}, nil
	}
	repo := strings.TrimSpace(repoOut)
	if repo == "" {
		return Result{IsError: true, Content: "gh repo view returned an empty repository name"}, nil
	}

	// Single atomic query: head SHA + rollup + labels + issue comments.
	prOut, err := t.gh(ctx, dir, "pr", "view", strconv.Itoa(prNumber), "--json",
		"number,headRefOid,statusCheckRollup,labels,comments")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("gh pr view %d failed: %v", prNumber, err)}, nil
	}
	var prView struct {
		Number       int                     `json:"number"`
		HeadRefOid   string                  `json:"headRefOid"`
		StatusRollup []rollupEntry           `json:"statusCheckRollup"`
		Labels       []struct{ Name string } `json:"labels"`
		Comments     []struct{ Body string } `json:"comments"`
	}
	if err := json.Unmarshal([]byte(prOut), &prView); err != nil || prView.Number == 0 {
		return Result{IsError: true, Content: fmt.Sprintf("failed to parse PR #%d data: %v", prNumber, err)}, nil
	}
	report.HeadSHA = prView.HeadRefOid
	// head_pinned is always true when a head was resolved: its value IS
	// the pin the caller must use (--match-head-commit).
	report.HeadPinned = report.HeadSHA != ""

	// --- checks_green: rollup all SUCCESS, and still the current head ---
	checkReasons, moved := t.evaluateChecks(ctx, dir, prNumber, prView.StatusRollup, report.HeadSHA)
	report.ChecksGreen = len(checkReasons) == 0
	if moved != "" {
		// Re-pin to the freshest head so the caller merges the real head.
		report.HeadSHA = moved
	}

	// --- reviews_resolved: latest review per author not CHANGES_REQUESTED ---
	revOut, err := t.gh(ctx, dir, "api", fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, prNumber))
	reviewReasons, resolved := t.evaluateReviews(revOut, err)
	report.ReviewsResolved = resolved

	// --- holds_clear: no label/comment matches a hold pattern ---
	holdReasons := t.evaluateHolds(prView.Labels, prView.Comments)
	report.HoldsClear = len(holdReasons) == 0

	var reasons []string
	reasons = append(reasons, checkReasons...)
	reasons = append(reasons, reviewReasons...)
	reasons = append(reasons, holdReasons...)
	report.Reasons = reasons
	report.Verdict = "HOLD"
	if report.ChecksGreen && report.ReviewsResolved && report.HoldsClear && report.HeadPinned {
		report.Verdict = "PASS"
	}

	return Result{Content: renderPreflight(report, repo)}, nil
}

// evaluateChecks computes the checks_green predicate inputs: reasons for
// every rollup entry that is not SUCCESS, plus a head-move detection.
// It re-reads headRefOid after the rollup so checks that belong to an
// older head (the head moved during the preflight) fail the predicate;
// the returned moved value is the fresh head to re-pin to ("" if none).
func (t MergePreflightTool) evaluateChecks(ctx context.Context, dir string, prNumber int, rollup []rollupEntry, capturedHead string) (reasons []string, moved string) {
	for _, e := range rollup {
		if !e.success() {
			reasons = append(reasons, e.describe())
		}
	}
	pinOut, err := t.gh(ctx, dir, "pr", "view", strconv.Itoa(prNumber), "--json", "headRefOid")
	if err != nil {
		return append(reasons, fmt.Sprintf("head re-verification failed: %v", err)), ""
	}
	var pinView struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(pinOut), &pinView); err != nil {
		return append(reasons, fmt.Sprintf("failed to parse head re-verification: %v", err)), ""
	}
	if pinView.HeadRefOid != capturedHead {
		reasons = append(reasons, fmt.Sprintf("head moved during preflight (checks belong to %s, current head %s)", shortSHA(capturedHead), shortSHA(pinView.HeadRefOid)))
		return reasons, pinView.HeadRefOid
	}
	return reasons, ""
}

// evaluateReviews computes the reviews_resolved predicate from the
// reviews API response (err non-nil means the query itself failed; the
// predicate is then conservatively false with the failure recorded).
func (t MergePreflightTool) evaluateReviews(revOut string, err error) (reasons []string, resolved bool) {
	if err != nil {
		return []string{fmt.Sprintf("reviews query failed: %v", err)}, false
	}
	var reviews []prReview
	if jerr := json.Unmarshal([]byte(revOut), &reviews); jerr != nil {
		return []string{fmt.Sprintf("reviews parse failed: %v", jerr)}, false
	}
	resolved = resolveReviewStates(reviews, &reasons)
	return reasons, resolved
}

// evaluateHolds computes the holds_clear predicate inputs: one reason
// per hold pattern that matched any label name or comment body.
func (t MergePreflightTool) evaluateHolds(labels []struct{ Name string }, comments []struct{ Body string }) []string {
	var reasons []string
	var patterns []*regexp.Regexp
	if t.Access == nil {
		return nil
	}
	patterns, err := t.Access.HoldPatterns()
	if err != nil {
		return []string{fmt.Sprintf("hold_patterns failed to compile: %v", err)}
	}
	if len(patterns) == 0 {
		return nil
	}
	candidates := make([]string, 0, len(labels)+len(comments))
	for _, l := range labels {
		candidates = append(candidates, l.Name)
	}
	for _, c := range comments {
		candidates = append(candidates, c.Body)
	}
	for _, re := range patterns {
		for _, c := range candidates {
			if re.MatchString(c) {
				reasons = append(reasons, fmt.Sprintf("hold pattern %q matched", re.String()))
				break
			}
		}
	}
	return reasons
}

// prReview is one entry of the pulls reviews API.
type prReview struct {
	User        struct{ Login string } `json:"user"`
	State       string                 `json:"state"`
	SubmittedAt string                 `json:"submitted_at"`
}

// resolveReviewStates computes the reviews_resolved predicate. The latest
// review per author (by submitted_at) is authoritative: an outstanding
// CHANGES_REQUESTED as an author's latest state makes the predicate false;
// a later APPROVED/DISMISSED/COMMENTED supersedes it. No reviews at all
// means nothing outstanding.
func resolveReviewStates(reviews []prReview, reasons *[]string) bool {
	latestState := map[string]string{}
	latestAt := map[string]string{}
	for _, rv := range reviews {
		author := rv.User.Login
		if author == "" {
			continue
		}
		if prevAt, ok := latestAt[author]; ok && prevAt > rv.SubmittedAt {
			continue
		}
		latestAt[author] = rv.SubmittedAt
		latestState[author] = rv.State
	}
	resolved := true
	for author, state := range latestState {
		if state == "CHANGES_REQUESTED" {
			*reasons = append(*reasons, fmt.Sprintf("review by %s: CHANGES_REQUESTED (unresolved)", author))
			resolved = false
		}
	}
	return resolved
}

// rollupEntry models one statusCheckRollup context (CheckRun or
// StatusContext shapes).
type rollupEntry struct {
	Typ        string `json:"__typename"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	Context    string `json:"context"`
}

func (e rollupEntry) success() bool {
	if e.Typ == "CheckRun" || e.Status != "" || e.Conclusion != "" {
		return strings.EqualFold(e.Status, "COMPLETED") && strings.EqualFold(e.Conclusion, "SUCCESS")
	}
	return strings.EqualFold(e.State, "SUCCESS")
}

func (e rollupEntry) describe() string {
	label := e.Name
	if label == "" {
		label = e.Context
	}
	return fmt.Sprintf("check %q not SUCCESS (status=%s conclusion=%s state=%s)", label, e.Status, e.Conclusion, e.State)
}

func renderPreflight(r preflightReport, repo string) string {
	machine, _ := json.Marshal(r)
	var b strings.Builder
	b.Write(machine)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "Merge preflight for PR #%d (%s)\n", r.PR, repo)
	fmt.Fprintf(&b, "  head_pinned:      %v (head %s -> use --match-head-commit %s)\n", r.HeadPinned, shortSHA(r.HeadSHA), r.HeadSHA)
	fmt.Fprintf(&b, "  checks_green:     %v\n", r.ChecksGreen)
	fmt.Fprintf(&b, "  reviews_resolved: %v\n", r.ReviewsResolved)
	fmt.Fprintf(&b, "  holds_clear:      %v\n", r.HoldsClear)
	if len(r.Reasons) > 0 {
		b.WriteString("  reasons:\n")
		for _, why := range r.Reasons {
			fmt.Fprintf(&b, "    - %s\n", why)
		}
	}
	if r.Verdict == "PASS" {
		b.WriteString("\nPREFLIGHT: PASS\n")
	} else {
		fmt.Fprintf(&b, "\nPREFLIGHT: HOLD(%s)\n", strings.Join(r.Reasons, "; "))
	}
	return b.String()
}

func (t MergePreflightTool) Clone() Tool {
	return &MergePreflightTool{WorkingDir: t.WorkingDir, GHRun: t.GHRun, Access: t.Access}
}
