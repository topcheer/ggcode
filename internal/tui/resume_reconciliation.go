package tui

// Resume Reconciliation — verify restored task-board claims against the live
// workspace on session resume.
//
// Research basis: ContinuityBench (2025-26 agent evaluation) scores handoff
// quality by whether an agent, on resume, re-grounds its plan in the *live*
// environment rather than trusting a stale handoff signal. ggcode's task
// board persistence (PR #2438) restores the board verbatim: "task-3
// completed" is re-presented as fact even if the workspace moved while the
// session was paused — branch switched, HEAD advanced, files committed or
// reverted externally. The agent then builds on claims that may no longer
// hold, wasting work or clobbering newer state.
//
// Solution (zero LLM calls, purely deterministic):
//  1. When the board is snapshotted into a session, also capture a bounded
//     VCS fingerprint (branch, HEAD, dirty-file sample) of the workspace.
//  2. On resume of a session with a non-empty board, capture the fingerprint
//     again and diff. Any drift yields a short reconciliation note that is
//     (a) shown to the user next to the session recap, and (b) injected into
//     the model's system prompt for the first post-resume runs via a named
//     dynamic prompt layer, so the model re-verifies claims before acting.
//
// Protocol-safe: the note rides the existing dynamic system-prompt layers
// (rebuilt every run, never injected as a synthetic user message).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/task"
)

// envFingerprintDirtyCap bounds the persisted dirty-file sample. The full
// count is kept separately so large trees still report scale without
// bloating every session meta record.
const envFingerprintDirtyCap = 64

// envFingerprint is a bounded snapshot of workspace VCS state.
type envFingerprint struct {
	CapturedAt time.Time `json:"captured_at"`
	Branch     string    `json:"branch,omitempty"`
	Head       string    `json:"head,omitempty"`
	// DirtyFiles is a bounded sample of paths reported dirty by
	// `git status --porcelain -z` at capture time; DirtyTotal is the
	// untruncated count.
	DirtyFiles []string `json:"dirty_files,omitempty"`
	DirtyTotal int      `json:"dirty_total,omitempty"`
}

// resumeNoteStore carries the reconciliation note from the TUI update loop
// (writer) to the agent's system-prompt goroutine (reader) without a race.
// Model holds it by pointer (Model is passed by value all over the TUI, so
// an embedded mutex would be copied).
type resumeNoteStore struct {
	mu   sync.Mutex
	note string
}

func (s *resumeNoteStore) set(note string) {
	s.mu.Lock()
	s.note = note
	s.mu.Unlock()
}

func (s *resumeNoteStore) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.note
}

// setResumeNote stores the reconciliation note; nil-safe and lazily
// initializing (all writers run serialized in the TUI update loop, so the
// lazy pointer store cannot race).
func (m *Model) setResumeNote(note string) {
	if m == nil {
		return
	}
	if m.resumeNote == nil {
		m.resumeNote = &resumeNoteStore{}
	}
	m.resumeNote.set(note)
}

// resumeNoteValue returns the current note ("" before the first resume that
// produced one). Safe to call from the agent goroutine once the prompt layer
// has been registered - registration happens after lazy initialization.
func (m *Model) resumeNoteValue() string {
	if m == nil || m.resumeNote == nil {
		return ""
	}
	return m.resumeNote.get()
}

// captureEnvFingerprint snapshots the VCS state of dir. Returns nil when the
// directory has no usable git metadata or git calls fail — reconciliation is
// best-effort and must never block or fail a session switch.
func captureEnvFingerprint(dir string) *envFingerprint {
	if dir == "" {
		return nil
	}
	fp := &envFingerprint{CapturedAt: time.Now()}
	if branch, err := gitBranchForDir(dir); err == nil {
		fp.Branch = branch
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := runGitCapture(dir, ctx, "rev-parse", "HEAD"); err == nil {
		fp.Head = strings.TrimSpace(out)
	}
	if out, err := runGitCapture(dir, ctx, "status", "--porcelain", "-z"); err == nil {
		files := parsePorcelainPaths(out)
		fp.DirtyTotal = len(files)
		if len(files) > envFingerprintDirtyCap {
			files = files[:envFingerprintDirtyCap]
		}
		fp.DirtyFiles = files
	}
	if fp.Branch == "" && fp.Head == "" && fp.DirtyTotal == 0 {
		return nil // no git signal at all — nothing to reconcile against
	}
	return fp
}

// runGitCapture runs git in dir with the given context and returns stdout.
func runGitCapture(dir string, ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

// parsePorcelainPaths extracts path entries from `git status --porcelain -z`
// output. Rename/copy records carry the old path as a separate NUL field; it
// is kept as its own entry — for drift heuristics a spuriously tracked old
// path is harmless, and mis-parsing would be worse.
func parsePorcelainPaths(out string) []string {
	var files []string
	for _, e := range strings.Split(out, "\x00") {
		// Parse the fixed "XY " status prefix on the RAW entry first: the
		// worktree-only status (" M path") starts with a space, which
		// TrimSpace would destroy and shift the prefix check.
		if len(e) > 3 && e[2] == ' ' {
			e = e[3:]
		}
		e = strings.TrimSpace(e)
		if e != "" {
			files = append(files, e)
		}
	}
	return files
}

// decodeEnvFingerprint parses a persisted fingerprint; nil when absent or
// corrupt (snapshots written before this feature carry no field).
func decodeEnvFingerprint(data []byte) *envFingerprint {
	if len(data) == 0 {
		return nil
	}
	var fp envFingerprint
	if err := json.Unmarshal(data, &fp); err != nil {
		return nil
	}
	if fp.CapturedAt.IsZero() && fp.Branch == "" && fp.Head == "" && len(fp.DirtyFiles) == 0 {
		return nil
	}
	return &fp
}

// diffEnvFingerprints reports human-readable drift findings between the
// snapshot-time fingerprint and the live one. Empty means "no detectable
// drift" (or nothing comparable).
func diffEnvFingerprints(prev, cur *envFingerprint) []string {
	if prev == nil || cur == nil {
		return nil
	}
	var findings []string
	if prev.Branch != cur.Branch {
		findings = append(findings, fmt.Sprintf("branch switched: %s → %s",
			displayBranchName(prev.Branch), displayBranchName(cur.Branch)))
	}
	if prev.Head != "" && cur.Head != "" && prev.Head != cur.Head {
		findings = append(findings, fmt.Sprintf("HEAD moved: %s → %s", shortSha(prev.Head), shortSha(cur.Head)))
	}

	prevSet := make(map[string]struct{}, len(prev.DirtyFiles))
	for _, f := range prev.DirtyFiles {
		prevSet[f] = struct{}{}
	}
	curSet := make(map[string]struct{}, len(cur.DirtyFiles))
	for _, f := range cur.DirtyFiles {
		curSet[f] = struct{}{}
	}

	var resolved, added []string
	for f := range prevSet {
		if _, ok := curSet[f]; !ok {
			resolved = append(resolved, f)
		}
	}
	for f := range curSet {
		if _, ok := prevSet[f]; !ok {
			added = append(added, f)
		}
	}
	sort.Strings(resolved)
	sort.Strings(added)

	if len(resolved) > 0 {
		findings = append(findings, fmt.Sprintf("dirty at snapshot, clean now (committed, reverted, or changed externally): %s", joinCapPaths(resolved, 8)))
	}
	if len(added) > 0 {
		findings = append(findings, fmt.Sprintf("newly modified since snapshot: %s", joinCapPaths(added, 8)))
	}
	return findings
}

// buildResumeReconciliationNote composes the reconciliation message shown to
// both the user and the model. Empty when there is no drift.
func buildResumeReconciliationNote(stats string, findings []string) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Resume reconciliation** — task board restored (%s), but the workspace changed since the snapshot:\n", stats)
	for _, f := range findings {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	b.WriteString("Completed/in-progress claims may be stale. Re-verify affected outputs against the live workspace (re-read files, re-run checks) before building on them; reclassify stale tasks, and mark work that already exists as done.")
	return b.String()
}

// prepareResumeReconciliation diffs the resumed session's task board against
// the live workspace. Returns the reconciliation note (also stored for the
// model-facing prompt layer); empty when there is nothing to reconcile.
func (m *Model) prepareResumeReconciliation(ses *session.Session) string {
	m.setResumeNote("")
	if ses == nil {
		return ""
	}
	completed, inProgress, pending, ok := task.BoardStats(ses.TasksJSON)
	if !ok || completed+inProgress+pending == 0 {
		return ""
	}
	prev := decodeEnvFingerprint(ses.TasksEnvJSON)
	if prev == nil {
		return "" // snapshot predates fingerprinting — no baseline to diff
	}
	cur := captureEnvFingerprint(cwdOrEmpty())
	findings := diffEnvFingerprints(prev, cur)
	if len(findings) == 0 {
		return ""
	}
	stats := fmt.Sprintf("%d completed, %d in progress, %d pending", completed, inProgress, pending)
	note := buildResumeReconciliationNote(stats, findings)
	m.setResumeNote(note)
	// Register the model-facing layer (idempotent by name). Reads go through
	// the mutex-guarded store, so the agent goroutine never races the updater.
	if m.agent != nil {
		m.agent.AddSystemPromptLayer("resume-reconciliation", func() string {
			return m.resumeNoteValue()
		})
	}
	return note
}

// cwdOrEmpty returns the process working directory (the same convention the
// sidebar's cached branch uses) or "" when unavailable.
func cwdOrEmpty() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// displayBranchName renders an empty branch as "(detached)".
func displayBranchName(b string) string {
	if b == "" {
		return "(detached)"
	}
	return b
}

func shortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// joinCapPaths renders at most cap sorted paths, appending "+N more" for the
// remainder.
func joinCapPaths(paths []string, cap int) string {
	shown := paths
	if len(shown) > cap {
		shown = shown[:cap]
	}
	out := strings.Join(shown, ", ")
	if rest := len(paths) - len(shown); rest > 0 {
		out += fmt.Sprintf(" (+%d more)", rest)
	}
	return out
}
