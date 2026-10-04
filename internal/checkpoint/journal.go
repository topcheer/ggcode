package checkpoint

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Persistent checkpoint journal.
//
// The in-memory Manager loses all undo history when the process exits or
// crashes. Frontier agents survive restarts: Claude Code persists
// project-scoped file-history checkpoints (its /rewind works after a crash),
// Aider snapshots edits into git. This journal closes that gap with an
// append-only JSONL event stream: every checkpoint mutation appends one
// event; on startup the events are replayed (bookkeeping only, never disk
// writes) to rebuild checkpoints, redo stack, corrections, eviction flags.
//
// Design choices:
//   - Events, not snapshots: one small append per mutation, no O(n) rewrite
//     of the full (content-bearing) history on the edit hot path.
//   - Fail-open: any journal error (unreadable path, write failure) disables
//     persistence and logs — checkpointing must never block or break edits.
//     The process then behaves exactly like the pre-journal in-memory
//     Manager.
//   - Crash tolerance: a process killed mid-append leaves a torn final line;
//     loadJournal truncates it so future appends stay parseable. Corrupt
//     non-final lines are skipped with a log (interleaving from two
//     processes on one project is not supported, same policy as run_journal).
//   - No fsync: a crash can lose the most recent event(s), degrading to the
//     old in-memory behavior for the very last edits. Full history loss is
//     what this journal eliminates.

// Journal event types. Each corresponds to one Manager mutation.
const (
	evSave             = "save"              // Checkpoint recorded (with full contents)
	evStartRun         = "start_run"         // StartRun tagged subsequent saves
	evUndo             = "undo"              // last checkpoint popped to redo stack
	evRedo             = "redo"              // redo stack top re-applied
	evRevert           = "revert"            // Revert(id): history truncated at id
	evUndoRun          = "undo_run"          // UndoRun(runID): run segment removed
	evDropRunFiles     = "drop_run_files"    // partial-failure cleanup of reverted files
	evClear            = "clear"             // Clear(): all history dropped
	evClearCorrections = "clear_corrections" // ClearCorrections()
)

// journalEvent is one line of the journal. Fields are unioned by Type; see
// the ev* constants for which fields each type carries.
type journalEvent struct {
	Type   string      `json:"type"`
	Cp     *Checkpoint `json:"cp,omitempty"` // evSave
	RunID  string      `json:"run_id,omitempty"`
	ID     string      `json:"id,omitempty"`     // evRevert target checkpoint
	Source string      `json:"source,omitempty"` // evUndo / evRevert correction source
	Files  []string    `json:"files,omitempty"`  // evRevert / evDropRunFiles
	Time   time.Time   `json:"time"`
}

// journalWriter appends events to the journal file. Safe for concurrent use;
// the Manager serializes appends under its own mutex, the internal mutex
// guards the disabled transition and file handle.
type journalWriter struct {
	mu       sync.Mutex
	path     string
	f        *os.File
	disabled bool
}

// openJournalWriter opens (creating if needed) the journal file for append.
func openJournalWriter(path string) (*journalWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &journalWriter{path: path, f: f}, nil
}

// append writes one event line. On the first write error the writer disables
// itself (fail-open) — callers never see errors and edits proceed exactly as
// with the in-memory Manager.
func (w *journalWriter) append(ev journalEvent) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.disabled {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		w.disableLocked(err)
		return
	}
	b = append(b, '\n')
	if _, err := w.f.Write(b); err != nil {
		w.disableLocked(err)
	}
}

func (w *journalWriter) disableLocked(err error) {
	w.disabled = true
	debug.Log("checkpoint", "journal disabled (%s): %v", w.path, err)
	_ = w.f.Close()
	w.f = nil
}

// loadJournal reads and parses the journal at path, returning the events in
// append order. A torn final line (crash mid-append) is truncated away; a
// corrupt line in the middle is skipped with a log. It also reports whether
// the file was truncated so tests can assert the recovery.
func loadJournal(path string) (events []journalEvent, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	// Track the byte offset of the start of the current line so a torn tail
	// can be truncated exactly there.
	var offset int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // checkpoints carry file contents
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			offset += int64(len(line)) + 1
			continue
		}
		var ev journalEvent
		if jerr := json.Unmarshal(line, &ev); jerr != nil {
			// Torn final line: crash while appending. Truncate the file back
			// to the last complete event so later appends stay parseable.
			if tErr := os.Truncate(path, offset); tErr != nil {
				debug.Log("checkpoint", "journal %s: failed to truncate torn tail: %v", path, tErr)
			} else {
				truncated = true
			}
			break
		}
		events = append(events, ev)
		offset += int64(len(line)) + 1
	}
	if serr := sc.Err(); serr != nil {
		// Unreadable mid-file (e.g. a line over the buffer cap): keep the
		// events parsed so far; the journal stays append-only.
		debug.Log("checkpoint", "journal %s: scan stopped: %v", path, serr)
	}
	return events, truncated, nil
}

// journalPathFor maps a project key (typically the working directory) to a
// stable per-project journal file, mirroring run_journal's config-dir layout.
func journalPathFor(projectKey string) (string, error) {
	dir := defaultJournalDir()
	if dir == "" {
		return "", os.ErrInvalid
	}
	sum := sha256.Sum256([]byte(projectKey))
	name := hex.EncodeToString(sum[:8]) + ".jsonl"
	return filepath.Join(dir, string(name[:2]), name), nil
}

func defaultJournalDir() string {
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "ggcode", "checkpoints")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode", "checkpoints")
}

// NewPersistentManager creates a Manager whose history survives process
// exit: prior events for projectKey are replayed into the new Manager and
// every subsequent mutation is journaled. Fail-open: if the journal cannot
// be loaded or opened the result is a plain in-memory Manager (persistence
// off, edits unaffected).
func NewPersistentManager(maxCheckpoints int, projectKey string) *Manager {
	path, err := journalPathFor(projectKey)
	if err != nil {
		debug.Log("checkpoint", "journal path unavailable, persistence off: %v", err)
		return NewManager(maxCheckpoints)
	}
	return openPersistentManagerAt(maxCheckpoints, path)
}

// openPersistentManagerAt is the path-injectable core of
// NewPersistentManager (tests point it at a TempDir).
func openPersistentManagerAt(maxCheckpoints int, path string) *Manager {
	m := NewManager(maxCheckpoints)
	events, _, err := loadJournal(path)
	if err != nil {
		debug.Log("checkpoint", "journal %s unreadable, starting fresh: %v", path, err)
		// A load error (permissions) usually also blocks appends; still try
		// the writer, it disables itself on first failure.
	}
	w, err := openJournalWriter(path)
	if err != nil {
		debug.Log("checkpoint", "journal %s not writable, persistence off: %v", path, err)
		return m
	}
	m.mu.Lock()
	for _, ev := range events {
		m.applyJournalEventLocked(ev)
	}
	m.journal = w
	m.mu.Unlock()
	if len(events) > 0 {
		debug.Log("checkpoint", "journal %s: replayed %d events", path, len(events))
	}
	return m
}

// journalAppendLocked persists one mutation event. Nil-safe: Managers built
// by NewManager journal nothing. Must hold m.mu (mutations already do).
func (m *Manager) journalAppendLocked(ev journalEvent) {
	if m.journal == nil {
		return
	}
	// Zero timestamps (callers that don't need one for the live path) still
	// serialize as a real time — the JSON tag is non-omitempty, and a
	// "0001-01-01" event would replay into Correction.Time garbage.
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	m.journal.append(ev)
}

// applyJournalEventLocked rebuilds in-memory state from one journal event.
// It mirrors the bookkeeping half of each mutating method WITHOUT the disk
// writes (Undo/Revert/UndoRun already wrote the files before the event was
// journaled; replaying must not rewrite them). Each case names the method it
// mirrors — keep them in sync. Must hold m.mu.
func (m *Manager) applyJournalEventLocked(ev journalEvent) {
	switch ev.Type {
	case evStartRun: // mirrors StartRun
		m.currentRunID = ev.RunID

	case evSave: // mirrors SaveWithExistence (via applySaveLocked)
		if ev.Cp == nil {
			return
		}
		m.applySaveLocked(*ev.Cp)

	case evUndo: // mirrors Undo bookkeeping after restoreCheckpointState
		if len(m.checkpoints) == 0 {
			return
		}
		cp := m.checkpoints[len(m.checkpoints)-1]
		m.checkpoints = m.checkpoints[:len(m.checkpoints)-1]
		m.redoStack = append(m.redoStack, cp)
		m.corrections = append(m.corrections, Correction{
			Files:    []string{cp.FilePath},
			ToolCall: cp.ToolCall,
			RunID:    cp.RunID,
			Time:     ev.Time,
			Source:   ev.Source,
		})

	case evRedo: // mirrors Redo bookkeeping after AtomicWriteFile
		if len(m.redoStack) == 0 {
			return
		}
		cp := m.redoStack[len(m.redoStack)-1]
		m.redoStack = m.redoStack[:len(m.redoStack)-1]
		m.checkpoints = append(m.checkpoints, cp)

	case evRevert: // mirrors revertWithFiles bookkeeping after disk writes
		idx := -1
		for i, c := range m.checkpoints {
			if c.ID == ev.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return // target already gone (older journal, partial replay)
		}
		cp := m.checkpoints[idx]
		m.checkpoints = m.checkpoints[:idx]
		m.redoStack = nil // jumping to a past state invalidates redo (#554 E)
		m.corrections = append(m.corrections, Correction{
			Files:    ev.Files,
			ToolCall: cp.ToolCall,
			RunID:    cp.RunID,
			Time:     ev.Time,
			Source:   ev.Source,
		})

	case evUndoRun: // mirrors UndoRun bookkeeping after writeBaselines
		indices := m.runSegmentIndices(ev.RunID)
		if len(indices) == 0 {
			return
		}
		cutoff := indices[len(indices)-1]
		removed := make([]Checkpoint, len(m.checkpoints[cutoff:]))
		copy(removed, m.checkpoints[cutoff:])
		m.checkpoints = m.checkpoints[:cutoff]
		delete(m.evictedRuns, ev.RunID)
		for i := len(removed) - 1; i >= 0; i-- {
			m.redoStack = append(m.redoStack, removed[i])
		}
		// recordRunCorrection, but with the event's timestamp for fidelity.
		fileSet := make(map[string]bool)
		for _, cp := range removed {
			fileSet[cp.FilePath] = true
		}
		files := make([]string, 0, len(fileSet))
		for f := range fileSet {
			files = append(files, f)
		}
		toolCall := ""
		if len(removed) > 0 {
			toolCall = removed[0].ToolCall
		}
		m.corrections = append(m.corrections, Correction{
			Files:    files,
			ToolCall: toolCall,
			RunID:    ev.RunID,
			Time:     ev.Time,
		})

	case evDropRunFiles: // mirrors writeBaselines partial-failure cleanup
		for _, f := range ev.Files {
			for j := len(m.checkpoints) - 1; j >= 0; j-- {
				if m.checkpoints[j].FilePath == f && m.checkpoints[j].RunID == ev.RunID {
					m.checkpoints = append(m.checkpoints[:j], m.checkpoints[j+1:]...)
				}
			}
		}

	case evClear: // mirrors Clear (journal itself survives)
		m.checkpoints = nil
		m.redoStack = nil
		m.corrections = nil
		m.evictedRuns = nil
		m.fileExisted = nil

	case evClearCorrections: // mirrors ClearCorrections
		m.corrections = nil
	}
}
