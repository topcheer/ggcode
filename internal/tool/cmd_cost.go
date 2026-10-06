package tool

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Command cost hints (r25, research round sa-56 gap A).
//
// arXiv 2607.27250 ("Do Context Files Help Coding Agents?", Khatri 2026,
// 288 runs, two agents) ablated AGENTS.md injection strategies and found
// only ONE component with a measurable behavior effect: operational cost
// warnings ("the full test suite takes >20 minutes"). Given the warning,
// agents cut blind full-suite invocations monotonically (3.67 -> 2.44 ->
// 1.67 per task) and wall-clock dropped ~24%. Coding conventions and
// architecture prose had no detectable correctness effect.
//
// ggcode equivalent: when a command has empirically cost minutes in past
// runs, say so in its result so the agent reaches for a narrower scope
// (package filter, -run pattern) on the next iteration. Advisory only —
// the command still executes; a hint that blocks would be a regression,
// not a warning.

const (
	// costHintMinRuns requires at least this many recorded runs before a
	// hint fires, so one-off slow runs (cold cache, CI hiccup) don't
	// trigger advice.
	costHintMinRuns = 2
	// costHintThreshold is the average-duration floor for a hint. Chosen
	// to match stalledCommandDelay: anything the harness itself considers
	// "long-running" deserves a nudge toward narrower scope.
	costHintThreshold = 2 * time.Minute
	// costReportMinElapsed: observed durations below this floor are not
	// reported at all (unless a history hint fires) - a [cost] took 0s
	// tail on every fast command is pure noise; the warning value lies in
	// surfacing EXPENSIVE operations (arXiv 2607.27250).
	costReportMinElapsed = 2 * time.Second
	// costHistoryMax keeps the per-command ring small; commands repeat
	// heavily in a session and unbounded growth buys nothing.
	costHistoryMax = 16
)

type costStat struct {
	mu     sync.Mutex
	recent []time.Duration // ring of the last costHistoryMax runs
}

func (c *costStat) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recent = append(c.recent, d)
	if len(c.recent) > costHistoryMax {
		c.recent = c.recent[len(c.recent)-costHistoryMax:]
	}
}

func (c *costStat) snapshot() (n int, avg time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.recent) == 0 {
		return 0, 0
	}
	var total time.Duration
	for _, d := range c.recent {
		total += d
	}
	return len(c.recent), total / time.Duration(len(c.recent))
}

var commandCosts sync.Map // normalized command string -> *costStat

// normalizeCostKey collapses a shell command to a stable key so that
// trivial variations (extra whitespace, trailing comment) still hit the
// same history bucket (#3463): the comment promise was previously
// documented but not implemented - comment-bearing variants of the same
// command landed in separate buckets and never reached costHintMinRuns.
func normalizeCostKey(cmd string) string {
	cmd = stripTrailingComment(strings.TrimSpace(cmd))
	// Collapse internal whitespace runs to single spaces so "go  test"
	// and "go test" share a bucket.
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	cmd = strings.Join(fields, " ")
	if len(cmd) > 160 {
		cmd = cmd[:160]
	}
	return cmd
}

// stripTrailingComment removes an unquoted trailing shell comment ("# ..."
// or "// ...") from cmd. '#' or '/' inside single/double quotes is literal
// and never starts a comment (e.g. awk '{print $1 "#"}', URLs "host//path").
func stripTrailingComment(cmd string) string {
	var quote rune
	for i, r := range cmd {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case '#':
			return cmd[:i]
		case '/':
			if i+1 < len(cmd) && cmd[i+1] == '/' {
				return cmd[:i]
			}
		}
	}
	return cmd
}

// recordCommandCost files an observed wall-clock duration for a command.
// Called after every synchronous (non-backgrounded) run_command execution.
func recordCommandCost(cmd string, d time.Duration) {
	if cmd == "" || d <= 0 {
		return
	}
	key := normalizeCostKey(cmd)
	v, _ := commandCosts.LoadOrStore(key, &costStat{})
	v.(*costStat).add(d)
}

// commandCostHint returns the advisory cost line for a command whose
// recorded history averages above the threshold, or "" when the history
// is too thin or too cheap to warrant advice.
func commandCostHint(cmd string) string {
	key := normalizeCostKey(cmd)
	v, ok := commandCosts.Load(key)
	if !ok {
		return ""
	}
	n, avg := v.(*costStat).snapshot()
	if n < costHintMinRuns || avg < costHintThreshold {
		return ""
	}
	return fmt.Sprintf("[cost hint] this command averaged ~%s over %d recent runs - consider a narrower scope (package filter / -run pattern) for faster iteration",
		avg.Round(time.Second), n)
}

// formatCommandCost renders the observed duration plus, when history is
// significant, the advisory hint. Appended to run_command results so the
// cost is visible in-context exactly where the next planning turn reads.
// Sub-second fast paths return "" - a [cost] took 0s tail on every echo
// is noise, and the paper's effect comes from warning about EXPENSIVE
// operations, not annotating cheap ones.
func formatCommandCost(cmd string, elapsed time.Duration) string {
	hint := commandCostHint(cmd)
	if hint == "" && elapsed.Round(time.Second) < costReportMinElapsed {
		return ""
	}
	line := fmt.Sprintf("[cost] took %s", elapsed.Round(time.Second))
	if hint != "" {
		line += "\n" + hint
	}
	return line
}
