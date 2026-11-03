package tui

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

const envFingerprintDirtyCap = 64

type envFingerprint struct {
	CapturedAt time.Time `json:"captured_at"`
	Branch     string    `json:"branch,omitempty"`
	Head       string    `json:"head,omitempty"`

	DirtyFiles []string `json:"dirty_files,omitempty"`
	DirtyTotal int      `json:"dirty_total,omitempty"`
}

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

func (m *Model) setResumeNote(note string) {
	if m == nil {
		return
	}
	if m.resumeNote == nil {
		m.resumeNote = &resumeNoteStore{}
	}
	m.resumeNote.set(note)
}

func (m *Model) resumeNoteValue() string {
	if m == nil || m.resumeNote == nil {
		return ""
	}
	return m.resumeNote.get()
}

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
		return nil
	}
	return fp
}

func runGitCapture(dir string, ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

func parsePorcelainPaths(out string) []string {
	var files []string
	for _, e := range strings.Split(out, "\x00") {

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
		return ""
	}
	cur := captureEnvFingerprint(cwdOrEmpty())
	findings := diffEnvFingerprints(prev, cur)
	if len(findings) == 0 {
		return ""
	}
	stats := fmt.Sprintf("%d completed, %d in progress, %d pending", completed, inProgress, pending)
	note := buildResumeReconciliationNote(stats, findings)
	m.setResumeNote(note)

	if m.agent != nil {
		m.agent.AddSystemPromptLayer("resume-reconciliation", func() string {
			return m.resumeNoteValue()
		})
	}
	return note
}

func cwdOrEmpty() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

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
