package checkpoint

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// PersistDirName is the subdirectory (under the project's .ggcode dir) where
// file-edit checkpoints are persisted.
const PersistDirName = "undo"

// persistFilePrefix / persistFileSuffix make session records recognizable when
// listing the store directory.
const (
	persistFilePrefix = "ggundo-"
	persistFileSuffix = ".jsonl"
)

// MaxPersistedSessions bounds on-disk growth: when a Persist store opens its
// first session file it prunes the directory down to this many most-recent
// session records (AgentUndo-style GC with a count cutoff instead of a time
// cutoff, so an idle project never loses its last rollback point).
const MaxPersistedSessions = 10

// Persist appends every file checkpoint of a session to an on-disk JSONL file
// so that rollback survives process exit. The in-memory Manager is scoped to
// one process (and capped at maxCheckpoints FIFO entries); once ggcode exits,
// its rollback history was gone. Persistence closes that gap and enables the
// post-mortem `ggcode undo` CLI: roll back the most recent session's file
// edits without resuming it.
//
// Persistence is best-effort by design: a failed disk write never blocks the
// edit that produced the checkpoint — it is logged and the in-memory undo
// path keeps working. Design references: AgentUndo (local-first provenance,
// https://agent-undo.com) and the saga-undo write-up on post-hoc rollback.
type Persist struct {
	mu        sync.Mutex
	dir       string
	sessionID string
	f         *os.File
	w         *bufio.Writer
	pruned    bool
}

// DefaultPersistDir returns the undo store directory for a project directory.
func DefaultPersistDir(projectDir string) string {
	return filepath.Join(projectDir, ".ggcode", PersistDirName)
}

// NewPersist creates a persist layer bound to dir. An empty sessionID is
// allowed; call SetSession once the real ID is known (TUI creates sessions
// asynchronously). File handles open lazily on first Append.
func NewPersist(dir, sessionID string) *Persist {
	p := &Persist{dir: dir}
	if sessionID != "" {
		p.sessionID = sessionID
	}
	return p
}

// SetSession rebinds the persist layer to a session. The currently open
// session file is flushed and closed; the next Append opens the new file.
func (p *Persist) SetSession(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sessionID == p.sessionID {
		return
	}
	p.closeLocked()
	p.sessionID = sessionID
}

// Append records a checkpoint. Best-effort: errors are logged, never
// propagated, so file edits never fail because of undo bookkeeping.
func (p *Persist) Append(cp Checkpoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessionID == "" {
		return
	}
	if err := p.ensureOpenLocked(); err != nil {
		debug.Log("checkpoint", "undo persist open failed: %v", err)
		return
	}
	line, err := json.Marshal(cp)
	if err != nil {
		debug.Log("checkpoint", "undo persist marshal failed: %v", err)
		return
	}
	if _, err := p.w.Write(append(line, '\n')); err != nil {
		debug.Log("checkpoint", "undo persist write failed: %v", err)
		return
	}
	// Flush per record: an edit is LLM-paced (seconds apart), so the fsync
	// cost is negligible and a crash/crtrl-C keeps every record written
	// before it — the whole point of post-mortem rollback.
	if err := p.w.Flush(); err != nil {
		debug.Log("checkpoint", "undo persist flush failed: %v", err)
	}
}

// Close flushes and closes the open session file, if any.
func (p *Persist) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

func (p *Persist) closeLocked() error {
	if p.w != nil {
		_ = p.w.Flush()
		p.w = nil
	}
	if p.f != nil {
		err := p.f.Close()
		p.f = nil
		return err
	}
	return nil
}

func (p *Persist) ensureOpenLocked() error {
	if p.w != nil && p.f != nil {
		return nil
	}
	if !p.pruned {
		if err := PrunePersistedSessions(p.dir, MaxPersistedSessions); err != nil {
			// Prune failure must not block recording.
			debug.Log("checkpoint", "undo persist prune failed: %v", err)
		}
		p.pruned = true
	}
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(p.dir, SessionFileName(p.sessionID))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	p.f = f
	p.w = bufio.NewWriter(f)
	return nil
}

// SessionFileName maps a session ID to its store file name. IDs are sanitized
// to a filesystem-safe subset so hostile or exotic IDs cannot escape dir.
func SessionFileName(sessionID string) string {
	return persistFilePrefix + sanitizeSessionID(sessionID) + persistFileSuffix
}

func sanitizeSessionID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	if out == "" || out == "." || out == ".." {
		out = "session"
	}
	return out
}

func sessionIDFromFileName(name string) (string, bool) {
	if !strings.HasPrefix(name, persistFilePrefix) || !strings.HasSuffix(name, persistFileSuffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, persistFilePrefix), persistFileSuffix)
	if id == "" {
		return "", false
	}
	return id, true
}

// PersistedSession describes one persisted session record file.
type PersistedSession struct {
	ID       string
	Modified time.Time
	Size     int64
}

// ListPersistedSessions returns the session records in dir, newest first.
func ListPersistedSessions(dir string) ([]PersistedSession, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []PersistedSession
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := sessionIDFromFileName(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, PersistedSession{ID: id, Modified: info.ModTime(), Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// PrunePersistedSessions keeps only the keep newest session record files.
func PrunePersistedSessions(dir string, keep int) error {
	if keep <= 0 {
		keep = MaxPersistedSessions
	}
	sessions, err := ListPersistedSessions(dir)
	if err != nil || len(sessions) <= keep {
		return err
	}
	for _, s := range sessions[keep:] {
		if rmErr := os.Remove(filepath.Join(dir, SessionFileName(s.ID))); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			debug.Log("checkpoint", "undo persist prune remove failed: %v", rmErr)
		}
	}
	return nil
}

// LoadSessionRecords loads all checkpoints recorded for a session. Malformed
// lines (e.g. a torn final line after a crash) are skipped rather than
// failing the whole load: every intact record still restores correctly
// because rollback uses per-file baselines, not a contiguous history.
func LoadSessionRecords(dir, sessionID string) ([]Checkpoint, error) {
	f, err := os.Open(filepath.Join(dir, SessionFileName(sessionID)))
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	var records []Checkpoint
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var cp Checkpoint
		if json.Unmarshal([]byte(line), &cp) != nil || cp.ID == "" || cp.FilePath == "" {
			continue
		}
		records = append(records, cp)
	}
	return records, sc.Err()
}

// SessionBaseline is the pre-session state of one file touched during a
// session, derived from the FIRST checkpoint recorded for that file.
type SessionBaseline struct {
	FilePath     string
	Existed      bool   // whether the file existed before the session touched it
	OldContent   string // pre-session content ("" when Existed is false)
	FinalContent string // content after the session's last recorded edit
	LastToolCall string
	TouchedAt    time.Time
}

// SessionBaselines reduces a session's checkpoint records to one baseline per
// file, in first-touch order. The first record per file is the authoritative
// pre-session state — later records only update FinalContent.
func SessionBaselines(records []Checkpoint) []SessionBaseline {
	index := make(map[string]int)
	var out []SessionBaseline
	for _, r := range records {
		if i, seen := index[r.FilePath]; seen {
			out[i].FinalContent = r.NewContent
			out[i].LastToolCall = r.ToolCall
			continue
		}
		index[r.FilePath] = len(out)
		out = append(out, SessionBaseline{
			FilePath:     r.FilePath,
			Existed:      r.Existed,
			OldContent:   r.OldContent,
			FinalContent: r.NewContent,
			LastToolCall: r.ToolCall,
			TouchedAt:    r.Timestamp,
		})
	}
	return out
}

// RollbackResult reports the outcome of restoring one file baseline.
type RollbackResult struct {
	FilePath string
	Action   string // "restored", "deleted", or "unchanged"
	Err      error
}

// RollbackSessionBaselines restores every file to its pre-session state:
// files the session created are removed, files it modified are rewritten
// with their pre-session content. Files whose current content already equals
// the baseline are reported as "unchanged" and left alone.
//
// Limitation (inherent to checkpoint-based tracking): only edits made through
// ggcode's file tools are recorded. Shell-command side effects (rm, git
// checkout, sed -i, ...) are invisible here — the preview and the rollback
// both reflect tracked tool edits only.
func RollbackSessionBaselines(baselines []SessionBaseline) []RollbackResult {
	results := make([]RollbackResult, 0, len(baselines))
	for _, b := range baselines {
		res := RollbackResult{FilePath: b.FilePath}
		if !b.Existed {
			if err := os.Remove(b.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				res.Err = err
			} else {
				res.Action = "deleted"
			}
			results = append(results, res)
			continue
		}
		if cur, err := os.ReadFile(b.FilePath); err == nil && string(cur) == b.OldContent {
			res.Action = "unchanged"
			results = append(results, res)
			continue
		}
		if err := util.AtomicWriteFile(b.FilePath, []byte(b.OldContent), 0o644); err != nil {
			res.Err = err
		} else {
			res.Action = "restored"
		}
		results = append(results, res)
	}
	return results
}

// CountLines counts lines the way a plain text editor does: "\n"-separated,
// with a trailing partial line counting as one and "" as zero.
func CountLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// DescribeBaseline renders the one-line preview shown by `ggcode undo`.
func DescribeBaseline(b SessionBaseline) string {
	verb := "modified"
	if !b.Existed {
		verb = "created"
	}
	return fmt.Sprintf("%-9s %s  %s", verb, b.FilePath, lineDelta(b))
}

func lineDelta(b SessionBaseline) string {
	if !b.Existed {
		return fmt.Sprintf("%4d lines added", CountLines(b.FinalContent))
	}
	return fmt.Sprintf("%4d -> %d lines", CountLines(b.OldContent), CountLines(b.FinalContent))
}
