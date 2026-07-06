package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Overseer implements the async-overseer pattern from SICA (Robeyns et al.
// 2025, arXiv:2504.15228) in a deterministic, zero-LLM-cost form.
//
// SICA's overseer is a separate LLM running in a concurrent thread that
// monitors the agent's execution trace and intervenes when it detects
// pathological behavior (stuck, drifting, repeating). Our implementation
// replaces the LLM with deterministic heuristics that analyze the tool-call
// trajectory — no extra API calls, no latency overhead.
//
// Detection modes (all triggered every overseerInterval iterations):
//  1. Tool spam: same tool called >spamThreshold times without meaningful
//     progress (no file edits or command execution).
//  2. Read-only stall: >stallThreshold iterations with only read/search
//     operations and no writes/edits.
//  3. Stuck on same file: read_file called on the same path >fileStuckThreshold
//     times consecutively without editing it.
//  4. Error escalation: error rate in recent half > 2× error rate in first half.
//  5. Drift detection: >driftThreshold iterations since last productive action
//     (edit, write, command, commit, notebook_edit).
//
// Intervention: injects a targeted guidance message into the agent context.
// Each pattern type fires at most once per run to avoid spamming.

const (
	overseerInterval      = 12  // run analysis every N iterations
	spamThreshold         = 6   // same tool >N times = spam
	stallThreshold        = 15  // >N read-only iterations = stall
	fileStuckThreshold    = 4   // same file read >N times without edit = stuck
	driftThreshold        = 20  // >N iterations without productive action = drift
	errorEscalationFactor = 2.0 // recent error rate > 2× early rate
)

// overseerState tracks the agent's tool-call trajectory for analysis.
type overseerState struct {
	mu sync.Mutex

	// Rolling history of tool calls within the current run.
	// Each entry is {toolName, isError, fileHint}.
	trajectory []trajectoryEntry

	// File reads since last edit (for stuck-on-file detection).
	fileReadsSinceEdit map[string]int

	// Iterations since last productive action (edit/write/command/commit).
	itersSinceProductive int

	// Intervention tracking — which patterns have already fired.
	fired map[string]bool

	// Drift detection level (0=not fired, 1/2/3=escalation levels).
	// Unlike other patterns, drift can fire multiple times with escalating
	// guidance for long autonomous runs (SICA progressive intervention concept).
	driftLevel int

	// Last analysis iteration (to avoid re-analyzing too frequently).
	lastAnalysisIter int
}

type trajectoryEntry struct {
	toolName string
	isError  bool
	fileHint string // file path if extractable, empty otherwise
}

// productiveTools are tools that represent forward progress on the task.
var productiveTools = map[string]bool{
	"edit_file":           true,
	"write_file":          true,
	"multi_edit_file":     true,
	"multi_file_edit":     true,
	"run_command":         true,
	"start_command":       true,
	"git_commit":          true,
	"git_add":             true,
	"notebook_edit":       true,
	"enter_worktree":      true,
	"write_command_input": true,
}

// readOnlyTools are tools that only consume information without changing state.
var readOnlyTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
	"list_directory":  true,
	"search_files":    true,
	"glob":            true,
	"grep":            true,
	"web_fetch":       true,
	"web_search":      true,
	"git_status":      true,
	"git_diff":        true,
	"git_log":         true,
	"git_blame":       true,
	"git_show":        true,
	"git_branch_list": true,
	"git_remote":      true,
	"lsp_definition":  true,
	"lsp_references":  true,
	"lsp_hover":       true,
	"lsp_symbols":     true,
	"lsp_diagnostics": true,
}

func newOverseerState() *overseerState {
	return &overseerState{
		fileReadsSinceEdit: make(map[string]int),
		fired:              make(map[string]bool),
	}
}

// recordToolCall adds a tool call to the trajectory.
func (o *overseerState) recordToolCall(toolName string, isError bool, fileHint string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.trajectory = append(o.trajectory, trajectoryEntry{
		toolName: toolName,
		isError:  isError,
		fileHint: fileHint,
	})

	// A tool call is productive if it's a productive tool AND it succeeded.
	// Failed run_command calls (e.g. failed builds) are NOT productive —
	// they don't represent forward progress. This prevents the drift detector
	// from being suppressed when the agent is stuck running failing commands.
	if productiveTools[toolName] && !(isError && toolName == "run_command") {
		o.itersSinceProductive = 0
		// Reset file-read tracking after an edit.
		o.fileReadsSinceEdit = make(map[string]int)
		// Reset drift level — a productive action means the agent is
		// making progress, so future drift should start from level 1 again.
		o.driftLevel = 0
	} else {
		o.itersSinceProductive++
	}

	// Track file reads for stuck-on-file detection.
	if toolName == "read_file" || toolName == "multi_file_read" {
		if fileHint != "" {
			o.fileReadsSinceEdit[fileHint]++
		}
	}
}

// reset clears the overseer state for a new run.
func (o *overseerState) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.trajectory = nil
	o.fileReadsSinceEdit = make(map[string]int)
	o.itersSinceProductive = 0
	o.driftLevel = 0
	o.fired = make(map[string]bool)
	o.lastAnalysisIter = 0
}

// analyze checks the trajectory for pathological patterns. Returns a
// non-empty guidance message if intervention is needed, empty string otherwise.
func (o *overseerState) analyze(iteration int) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.trajectory) < overseerInterval {
		return ""
	}
	if iteration-o.lastAnalysisIter < overseerInterval {
		return ""
	}
	o.lastAnalysisIter = iteration

	// Build analysis from a copy of trajectory (avoid holding lock too long).
	traj := make([]trajectoryEntry, len(o.trajectory))
	copy(traj, o.trajectory)

	// Run all checks; return first unfired intervention.
	// Order matters: more specific patterns first.
	if msg := o.checkReadOnlyStall(traj); msg != "" {
		return msg
	}
	if msg := o.checkToolSpam(traj); msg != "" {
		return msg
	}
	if msg := o.checkFileStuck(traj); msg != "" {
		return msg
	}
	if msg := o.checkErrorEscalation(traj); msg != "" {
		return msg
	}
	if msg := o.checkDrift(traj); msg != "" {
		return msg
	}

	return ""
}

// checkToolSpam detects when a single non-productive tool is called
// excessively without progress. Only fires when there are NO productive
// actions in the trajectory (to avoid false positives on healthy read-edit cycles).
func (o *overseerState) checkToolSpam(traj []trajectoryEntry) string {
	if o.fired["spam"] {
		return ""
	}
	// Only check spam if there are no productive tools at all.
	hasProductive := false
	for _, e := range traj {
		if productiveTools[e.toolName] {
			hasProductive = true
			break
		}
	}
	if hasProductive {
		return ""
	}
	counts := make(map[string]int)
	for _, e := range traj {
		counts[e.toolName]++
	}
	for tool, count := range counts {
		if count > spamThreshold {
			o.fired["spam"] = true
			debug.Log("overseer", "tool spam detected: %s called %d times without productive action", tool, count)
			return fmt.Sprintf(
				"Overseer: You have called %s %d times without making progress. "+
					"You may be stuck exploring without acting. Consider:\n"+
					"1. Summarize what you've learned so far\n"+
					"2. Make a concrete edit or run a command to move forward\n"+
					"3. If you need more context, batch your reads instead of one-by-one",
				tool, count,
			)
		}
	}
	return ""
}

// checkReadOnlyStall detects when the agent has been only reading/searching
// for too many iterations without any writes or commands.
func (o *overseerState) checkReadOnlyStall(traj []trajectoryEntry) string {
	if o.fired["stall"] {
		return ""
	}
	// Look at the last stallThreshold entries.
	if len(traj) < stallThreshold {
		return ""
	}
	recent := traj[len(traj)-stallThreshold:]
	allReadOnly := true
	for _, e := range recent {
		if !readOnlyTools[e.toolName] {
			allReadOnly = false
			break
		}
	}
	if allReadOnly {
		o.fired["stall"] = true
		debug.Log("overseer", "read-only stall: %d consecutive read-only iterations", stallThreshold)
		return fmt.Sprintf(
			"Overseer: You have spent %d iterations only reading and searching without "+
				"making any changes. You have enough context to act. Start implementing your "+
				"solution — make edits, run builds, or execute commands to move the task forward.",
			stallThreshold,
		)
	}
	return ""
}

// checkFileStuck detects when the same file is read repeatedly without
// being edited, suggesting the agent can't figure out what to change.
func (o *overseerState) checkFileStuck(traj []trajectoryEntry) string {
	if o.fired["file_stuck"] {
		return ""
	}
	for file, count := range o.fileReadsSinceEdit {
		if count >= fileStuckThreshold {
			o.fired["file_stuck"] = true
			debug.Log("overseer", "file stuck: %s read %d times without edit", file, count)
			return fmt.Sprintf(
				"Overseer: You have read %s %d times without editing it. "+
					"If you're unsure what to change, try:\n"+
					"1. Search for the specific function or symbol you need to modify\n"+
					"2. Look at how similar patterns are handled elsewhere in the codebase\n"+
					"3. Make a small experimental edit and build to test your understanding",
				file, count,
			)
		}
	}
	return ""
}

// checkErrorEscalation detects when the error rate is increasing over time.
func (o *overseerState) checkErrorEscalation(traj []trajectoryEntry) string {
	if o.fired["error_escalation"] {
		return ""
	}
	mid := len(traj) / 2
	if mid < 5 {
		return "" // not enough data
	}
	firstHalf := traj[:mid]
	recentHalf := traj[mid:]

	earlyErrors := float64(countErrors(firstHalf)) / float64(len(firstHalf))
	recentErrors := float64(countErrors(recentHalf)) / float64(len(recentHalf))

	if earlyErrors == 0 {
		earlyErrors = 0.01 // avoid division by zero
	}

	if recentErrors > earlyErrors*errorEscalationFactor && recentErrors > 0.3 {
		o.fired["error_escalation"] = true
		debug.Log("overseer", "error escalation: early=%.1f%% recent=%.1f%%", earlyErrors*100, recentErrors*100)
		return fmt.Sprintf(
			"Overseer: Your error rate is increasing (early: %.0f%%, recent: %.0f%%). "+
				"Your approach may be fundamentally wrong. Consider:\n"+
				"1. Re-read the original task requirements carefully\n"+
				"2. Check if you're modifying the right files\n"+
				"3. Consider a completely different strategy\n"+
				"4. If stuck, use ask_user to clarify requirements",
			earlyErrors*100, recentErrors*100,
		)
	}
	return ""
}

// checkDrift detects when no productive action has occurred for too long.
// Uses progressive escalation (SICA-inspired): each level fires at most once,
// with increasingly direct guidance for long autonomous runs.
//
// Level 1 (driftThreshold, 20 iters): "re-anchor your task"
// Level 2 (2×driftThreshold, 40 iters): "summarize progress and consider asking user"
// Level 3 (3×driftThreshold, 60 iters): "escalate — try fundamentally different approach"
func (o *overseerState) checkDrift(traj []trajectoryEntry) string {
	// Determine which level we should be at based on iterations without progress.
	var targetLevel int
	switch {
	case o.itersSinceProductive >= driftThreshold*3:
		targetLevel = 3
	case o.itersSinceProductive >= driftThreshold*2:
		targetLevel = 2
	case o.itersSinceProductive >= driftThreshold:
		targetLevel = 1
	default:
		return ""
	}

	// Only fire if we haven't already fired at this level.
	if o.driftLevel >= targetLevel {
		return ""
	}
	o.driftLevel = targetLevel
	debug.Log("overseer", "drift level %d: %d iterations since last productive action",
		targetLevel, o.itersSinceProductive)

	switch targetLevel {
	case 1:
		return fmt.Sprintf(
			"Overseer: %d iterations since you last made a productive change (edit, command, commit). "+
				"You may be drifting from the original task. Re-anchor:\n"+
				"1. State your current goal in one sentence\n"+
				"2. Identify the single next concrete action\n"+
				"3. Execute it immediately — stop researching and start doing",
			o.itersSinceProductive,
		)
	case 2:
		return fmt.Sprintf(
			"Overseer: %d iterations without productive changes. This is a significant stall.\n"+
				"1. Summarize what you've accomplished so far in 2-3 sentences\n"+
				"2. Identify the specific blocker preventing progress\n"+
				"3. If stuck on the same problem, try a completely different approach\n"+
				"4. If the task is unclear, ask the user for clarification rather than continuing to spin",
			o.itersSinceProductive,
		)
	default: // level 3
		return fmt.Sprintf(
			"Overseer: %d iterations without productive changes — critical stall detected.\n"+
				"This task may require:\n"+
				"- Asking the user for guidance or clarification\n"+
				"- Skipping the current subtask and moving to the next one\n"+
				"- Using ask_user to surface the blocker\n"+
				"Stop researching and take decisive action now.",
			o.itersSinceProductive,
		)
	}
}

// overseerCheck is called from the agent loop. It records the tool call and
// periodically runs analysis, returning guidance if intervention is needed.
func (a *Agent) overseerCheck(toolName string, isError bool, fileHint string, iteration int) string {
	if a.overseer == nil {
		return ""
	}
	a.overseer.recordToolCall(toolName, isError, fileHint)
	return a.overseer.analyze(iteration)
}

// resetOverseer clears trajectory for a new run.
func (a *Agent) resetOverseer() {
	if a.overseer == nil {
		return
	}
	a.overseer.reset()
}

// countErrors returns the number of error entries in a trajectory slice.
func countErrors(traj []trajectoryEntry) int {
	count := 0
	for _, e := range traj {
		if e.isError {
			count++
		}
	}
	return count
}

// extractFileHint tries to extract a file path from tool arguments JSON.
// Returns the first "path" or "file_path" value, or empty string.
func extractFileHint(toolName string, args []byte) string {
	// Quick extraction without full JSON parse — look for "path":"..." or "file_path":"..."
	s := string(args)
	for _, key := range []string{`"path"`, `"file_path"`, `"filePath"`} {
		idx := strings.Index(s, key)
		if idx < 0 {
			continue
		}
		// Find the value after the key
		rest := s[idx+len(key):]
		// Skip : and whitespace and opening quote
		rest = strings.TrimLeft(rest, " :\"")
		// Find closing quote
		end := strings.IndexByte(rest, '"')
		if end > 0 {
			hint := rest[:end]
			if len(hint) > 80 {
				hint = hint[:77] + "..."
			}
			return hint
		}
	}
	return ""
}

// overseerAnalysisTiming returns a human-readable description of the overseer
// configuration for debugging.
func overseerAnalysisTiming() string {
	return fmt.Sprintf("interval=%d spam>%d stall>%d fileStuck>%d drift>%d(%d/%d/%d)",
		overseerInterval, spamThreshold, stallThreshold, fileStuckThreshold,
		driftThreshold, driftThreshold, driftThreshold*2, driftThreshold*3)
}

// Compile-time assertion that time is used (for the duration tracking we may
// add later for time-based overseer checks).
var _ = time.Second
