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

const (
	evSave             = "save"
	evStartRun         = "start_run"
	evUndo             = "undo"
	evRedo             = "redo"
	evRevert           = "revert"
	evUndoRun          = "undo_run"
	evDropRunFiles     = "drop_run_files"
	evClear            = "clear"
	evClearCorrections = "clear_corrections"
)

type journalEvent struct {
	Type   string      `json:"type"`
	Cp     *Checkpoint `json:"cp,omitempty"`
	RunID  string      `json:"run_id,omitempty"`
	ID     string      `json:"id,omitempty"`
	Source string      `json:"source,omitempty"`
	Files  []string    `json:"files,omitempty"`
	Time   time.Time   `json:"time"`
}

type journalWriter struct {
	mu       sync.Mutex
	path     string
	f        *os.File
	disabled bool
}

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

func loadJournal(path string) (events []journalEvent, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	var offset int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			offset += int64(len(line)) + 1
			continue
		}
		var ev journalEvent
		if jerr := json.Unmarshal(line, &ev); jerr != nil {
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
		debug.Log("checkpoint", "journal %s: scan stopped: %v", path, serr)
	}
	return events, truncated, nil
}

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

func NewPersistentManager(maxCheckpoints int, projectKey string) *Manager {
	path, err := journalPathFor(projectKey)
	if err != nil {
		debug.Log("checkpoint", "journal path unavailable, persistence off: %v", err)
		return NewManager(maxCheckpoints)
	}
	return openPersistentManagerAt(maxCheckpoints, path)
}

// ListPersisted 按事件顺序回放 projectKey 的持久化 journal,返回回放后仍存活的检查点
// (save/redo 入列,undo/revert/undo_run/drop_run_files/clear 相应移除)——与 TUI 面板
// 看到的状态一致,而非简单收集所有 save 行。只读:不打开、不创建 journal 写句柄。
// maxCheckpoints 应与写入方 Manager 的容量一致(生产为 50)以对齐回放时的淘汰语义。
func ListPersisted(maxCheckpoints int, projectKey string) []Checkpoint {
	path, err := journalPathFor(projectKey)
	if err != nil {
		return nil
	}
	events, _, err := loadJournal(path)
	if err != nil || len(events) == 0 {
		return nil
	}
	m := NewManager(maxCheckpoints)
	m.mu.Lock()
	for _, ev := range events {
		m.applyJournalEventLocked(ev)
	}
	out := make([]Checkpoint, len(m.checkpoints))
	copy(out, m.checkpoints)
	m.mu.Unlock()
	return out
}

func openPersistentManagerAt(maxCheckpoints int, path string) *Manager {
	m := NewManager(maxCheckpoints)
	events, _, err := loadJournal(path)
	if err != nil {
		debug.Log("checkpoint", "journal %s unreadable, starting fresh: %v", path, err)
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

func (m *Manager) journalAppendLocked(ev journalEvent) {
	if m.journal == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	m.journal.append(ev)
}

func (m *Manager) applyJournalEventLocked(ev journalEvent) {
	switch ev.Type {
	case evStartRun:
		m.currentRunID = ev.RunID

	case evSave:
		if ev.Cp == nil {
			return
		}
		m.applySaveLocked(*ev.Cp)

	case evUndo:
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

	case evRedo:
		if len(m.redoStack) == 0 {
			return
		}
		cp := m.redoStack[len(m.redoStack)-1]
		m.redoStack = m.redoStack[:len(m.redoStack)-1]
		m.checkpoints = append(m.checkpoints, cp)

	case evRevert:
		idx := -1
		for i, c := range m.checkpoints {
			if c.ID == ev.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		cp := m.checkpoints[idx]
		m.checkpoints = m.checkpoints[:idx]
		m.redoStack = nil
		m.corrections = append(m.corrections, Correction{
			Files:    ev.Files,
			ToolCall: cp.ToolCall,
			RunID:    cp.RunID,
			Time:     ev.Time,
			Source:   ev.Source,
		})

	case evUndoRun:
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

	case evDropRunFiles:
		for _, f := range ev.Files {
			for j := len(m.checkpoints) - 1; j >= 0; j-- {
				if m.checkpoints[j].FilePath == f && m.checkpoints[j].RunID == ev.RunID {
					m.checkpoints = append(m.checkpoints[:j], m.checkpoints[j+1:]...)
				}
			}
		}

	case evClear:
		m.checkpoints = nil
		m.redoStack = nil
		m.corrections = nil
		m.evictedRuns = nil
		m.fileExisted = nil

	case evClearCorrections:
		m.corrections = nil
	}
}
