package checkpoint

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

type Checkpoint struct {
	ID         string     `json:"id"`
	FilePath   string     `json:"file_path"`
	OldContent string     `json:"old_content"`
	NewContent string     `json:"new_content"`
	Existed    bool       `json:"existed,omitempty"`
	Timestamp  time.Time  `json:"timestamp"`
	ToolCall   string     `json:"tool_call"`
	RunID      string     `json:"run_id,omitempty"`
	Intent     EditIntent `json:"intent,omitempty"`
}

type EditIntent struct {
	TaskID    string `json:"task_id,omitempty"`
	TaskDesc  string `json:"task_desc,omitempty"`
	Rationale string `json:"rationale,omitempty"`
}

type Correction struct {
	Files    []string
	ToolCall string
	RunID    string
	Time     time.Time
	Source   string
}

type Manager struct {
	checkpoints    []Checkpoint
	redoStack      []Checkpoint
	maxCheckpoints int
	mu             sync.Mutex
	currentRunID   string

	evictedRuns map[string]bool

	fileExisted map[string]bool

	corrections []Correction

	activeIntent EditIntent

	journal *journalWriter
}

func NewManager(maxCheckpoints int) *Manager {
	if maxCheckpoints <= 0 {
		maxCheckpoints = 50
	}
	return &Manager{maxCheckpoints: maxCheckpoints}
}

func (m *Manager) StartRun(runID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentRunID = runID
	m.activeIntent = EditIntent{}
	m.journalAppendLocked(journalEvent{Type: evStartRun, RunID: runID})
}

func (m *Manager) SetActiveIntent(in EditIntent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeIntent = in
}

func (m *Manager) Save(filePath, oldContent, newContent, toolCall string) Checkpoint {
	return m.SaveWithExistence(filePath, oldContent, newContent, toolCall, true)
}

func (m *Manager) SaveWithExistence(filePath, oldContent, newContent, toolCall string, existed bool) Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp := Checkpoint{
		ID:         generateID(),
		FilePath:   filePath,
		OldContent: oldContent,
		NewContent: newContent,
		Existed:    existed,
		Timestamp:  time.Now(),
		ToolCall:   toolCall,
		RunID:      m.currentRunID,
		Intent:     m.activeIntent,
	}

	m.applySaveLocked(cp)
	m.journalAppendLocked(journalEvent{Type: evSave, Cp: &cp})
	return cp
}

func (m *Manager) applySaveLocked(cp Checkpoint) {
	m.checkpoints = append(m.checkpoints, cp)

	if m.fileExisted == nil {
		m.fileExisted = make(map[string]bool)
	}
	if _, seen := m.fileExisted[cp.FilePath]; !seen {
		m.fileExisted[cp.FilePath] = cp.Existed
	}

	for len(m.checkpoints) > m.maxCheckpoints {
		evictIdx := 0
		for i, c := range m.checkpoints {
			if c.RunID != m.currentRunID {
				evictIdx = i
				break
			}
		}
		if m.evictedRuns == nil {
			m.evictedRuns = make(map[string]bool)
		}
		m.evictedRuns[m.checkpoints[evictIdx].RunID] = true
		m.checkpoints = append(m.checkpoints[:evictIdx], m.checkpoints[evictIdx+1:]...)
	}

	m.redoStack = nil
}

var ErrNothingToUndo = errors.New("checkpoint: nothing to undo")

func (m *Manager) Undo(source string) (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.checkpoints) == 0 {
		return nil, fmt.Errorf("%w: no checkpoints to undo", ErrNothingToUndo)
	}

	cp := m.checkpoints[len(m.checkpoints)-1]

	if err := restoreCheckpointState(cp.FilePath, cp.OldContent, cp.Existed); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	m.checkpoints = m.checkpoints[:len(m.checkpoints)-1]
	m.redoStack = append(m.redoStack, cp)

	now := time.Now()
	m.corrections = append(m.corrections, Correction{
		Files:    []string{cp.FilePath},
		ToolCall: cp.ToolCall,
		RunID:    cp.RunID,
		Time:     now,
		Source:   source,
	})

	m.journalAppendLocked(journalEvent{Type: evUndo, Source: source, Time: now})

	return &cp, nil
}

func (m *Manager) Revert(id string) (*Checkpoint, error) {
	cp, _, err := m.RevertWithFilesSource(id, "user")
	return cp, err
}

func (m *Manager) RevertWithFiles(id string) (*Checkpoint, []string, error) {
	return m.RevertWithFilesSource(id, "user")
}

func (m *Manager) RevertWithFilesSource(id, source string) (*Checkpoint, []string, error) {
	return m.revertWithFiles(id, source)
}

func (m *Manager) revertWithFiles(id, source string) (*Checkpoint, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	idx := -1
	for i, cp := range m.checkpoints {
		if cp.ID == id {
			idx = i
			break
		}
	}

	if idx < 0 {
		return nil, nil, fmt.Errorf("checkpoint %q not found", id)
	}

	cp := m.checkpoints[idx]

	targets := make(map[string]baselineState)
	for i := idx; i < len(m.checkpoints); i++ {
		f := m.checkpoints[i].FilePath
		if _, ok := targets[f]; ok {
			continue
		}
		state := baselineState{content: m.checkpoints[i].OldContent, existed: m.checkpoints[i].Existed}
		for j := idx - 1; j >= 0; j-- {
			if m.checkpoints[j].FilePath == f {
				state = baselineState{content: m.checkpoints[j].NewContent, existed: true}
				break
			}
		}
		targets[f] = state
	}

	order := make([]string, 0, len(targets))
	seen := make(map[string]bool, len(targets))
	for i := idx; i < len(m.checkpoints); i++ {
		f := m.checkpoints[i].FilePath
		if !seen[f] {
			seen[f] = true
			order = append(order, f)
		}
	}
	files := make([]string, 0, len(order))
	for _, f := range order {
		st := targets[f]
		if err := restoreFile(f, st.content, st.existed); err != nil {
			if len(files) == 0 {
				return nil, nil, fmt.Errorf("failed to revert %s: %w", f, err)
			}
			return nil, nil, fmt.Errorf("failed to revert %s: %w (partial state: restored %v; still pending %v - history kept, retry or use single-step Undo)",
				f, err, files, order[len(files):])
		}
		files = append(files, f)
	}

	m.checkpoints = m.checkpoints[:idx]
	m.redoStack = nil

	m.corrections = append(m.corrections, Correction{
		Files:    files,
		ToolCall: cp.ToolCall,
		RunID:    cp.RunID,
		Time:     time.Now(),
		Source:   source,
	})

	m.journalAppendLocked(journalEvent{Type: evRevert, ID: id, Files: files, Source: source})

	return &cp, files, nil
}

func restoreCheckpointState(path, oldContent string, existed bool) error {
	if !existed && oldContent == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return util.AtomicWriteFile(path, []byte(oldContent), 0644)
}

var restoreFile = restoreCheckpointState

type FileSummary struct {
	Path     string
	Edits    int
	LastTool string
	IsNew    bool
}

func (m *Manager) ModifiedFiles() []FileSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	order := make([]string, 0, len(m.checkpoints))
	summary := make(map[string]*FileSummary)

	for _, cp := range m.checkpoints {
		fs, ok := summary[cp.FilePath]
		if !ok {
			fs = &FileSummary{
				Path:     cp.FilePath,
				IsNew:    !m.firstExisted(cp.FilePath),
				LastTool: cp.ToolCall,
			}
			summary[cp.FilePath] = fs
			order = append(order, cp.FilePath)
		}
		fs.Edits++
		fs.LastTool = cp.ToolCall
	}

	out := make([]FileSummary, 0, len(order))
	for _, p := range order {
		out = append(out, *summary[p])
	}
	return out
}

func (m *Manager) firstExisted(path string) bool {
	if v, ok := m.fileExisted[path]; ok {
		return v
	}
	return true
}

func (m *Manager) UndoRun() ([]Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.checkpoints) == 0 {
		return nil, fmt.Errorf("%w: no checkpoints to undo", ErrNothingToUndo)
	}

	runID := m.checkpoints[len(m.checkpoints)-1].RunID

	if m.evictedRuns[runID] {
		return nil, fmt.Errorf(
			"refusing to undo run %q: its earliest checkpoints were evicted by the %d-checkpoint limit, "+
				"so the pre-run baseline is no longer recoverable (rolling back would restore a mid-run state); "+
				"use single-step Undo instead",
			runID, m.maxCheckpoints)
	}

	runIndices := m.runSegmentIndices(runID)
	if len(runIndices) == 0 {
		return nil, fmt.Errorf("no checkpoints in current run")
	}

	tailStart := runIndices[len(runIndices)-1]
	for i := 0; i < tailStart; i++ {
		if m.checkpoints[i].RunID == runID {
			return nil, fmt.Errorf(
				"refusing to undo run %q: its checkpoints are split into non-contiguous segments (mid-run entries survive before the tail segment, e.g. after a partial failure cleanup), so the pre-run baseline is no longer recoverable; use single-step Undo instead",
				runID)
		}
	}
	baselines := m.preRunBaselines(runIndices)

	reverted, failErr := m.writeBaselines(runIndices, baselines, runID)
	if failErr != nil {
		return reverted, failErr
	}

	cutoff := runIndices[len(runIndices)-1]
	removed := make([]Checkpoint, len(m.checkpoints[cutoff:]))
	copy(removed, m.checkpoints[cutoff:])
	m.checkpoints = m.checkpoints[:cutoff]
	delete(m.evictedRuns, runID)
	for i := len(removed) - 1; i >= 0; i-- {
		m.redoStack = append(m.redoStack, removed[i])
	}

	m.recordRunCorrection(reverted, runID)

	m.journalAppendLocked(journalEvent{Type: evUndoRun, RunID: runID})

	return reverted, nil
}

func (m *Manager) RevertRecentRuns(count int) ([]Checkpoint, error) {
	if count <= 0 {
		return nil, nil
	}
	var all []Checkpoint
	for i := 0; i < count; i++ {
		if m.Last() == nil {
			m.mu.Lock()
			evicted := len(m.evictedRuns)
			m.mu.Unlock()
			if evicted > 0 {
				return all, fmt.Errorf(
					"reverted %d run(s), but %d earlier run(s) with edits have evicted baselines "+
						"(FIFO checkpoint limit) and cannot be rolled back; their file changes remain on disk - "+
						"use git or single-step Undo inspection to reconcile",
					i, evicted)
			}
			break
		}
		cps, err := m.UndoRun()
		if err != nil {
			if len(all) > 0 {
				return all, fmt.Errorf("reverted %d run(s) before refusing: %w", i, err)
			}
			return nil, err
		}
		all = append(all, cps...)
	}
	return all, nil
}

func (m *Manager) runSegmentIndices(runID string) []int {
	var runIndices []int
	for i := len(m.checkpoints) - 1; i >= 0; i-- {
		if m.checkpoints[i].RunID != runID {
			break
		}
		runIndices = append(runIndices, i)
	}
	return runIndices
}

func (m *Manager) preRunBaselines(runIndices []int) map[string]baselineState {
	baselines := make(map[string]baselineState)
	for i := len(runIndices) - 1; i >= 0; i-- {
		cp := m.checkpoints[runIndices[i]]
		if _, exists := baselines[cp.FilePath]; !exists {
			baselines[cp.FilePath] = baselineState{content: cp.OldContent, existed: cp.Existed}
		}
	}
	return baselines
}

type baselineState struct {
	content string
	existed bool
}

func (m *Manager) writeBaselines(runIndices []int, baselines map[string]baselineState, runID string) ([]Checkpoint, error) {
	var reverted []Checkpoint
	revertedFiles := make(map[string]bool)
	for _, idx := range runIndices {
		cp := m.checkpoints[idx]
		if revertedFiles[cp.FilePath] {
			continue
		}
		if bl, ok := baselines[cp.FilePath]; ok {
			if err := restoreCheckpointState(cp.FilePath, bl.content, bl.existed); err != nil {
				m.removeRunCheckpointsFor(reverted, runID)
				dropped := make([]string, 0, len(reverted))
				seenFile := make(map[string]bool, len(reverted))
				for _, r := range reverted {
					if !seenFile[r.FilePath] {
						seenFile[r.FilePath] = true
						dropped = append(dropped, r.FilePath)
					}
				}
				m.journalAppendLocked(journalEvent{Type: evDropRunFiles, RunID: runID, Files: dropped})
				return reverted, fmt.Errorf("failed to revert %s: %w", cp.FilePath, err)
			}
		}
		revertedFiles[cp.FilePath] = true
		reverted = append(reverted, cp)
	}
	return reverted, nil
}

func (m *Manager) removeRunCheckpointsFor(cps []Checkpoint, runID string) {
	for _, r := range cps {
		for j := len(m.checkpoints) - 1; j >= 0; j-- {
			if m.checkpoints[j].FilePath == r.FilePath && m.checkpoints[j].RunID == runID {
				m.checkpoints = append(m.checkpoints[:j], m.checkpoints[j+1:]...)
			}
		}
	}
}

func (m *Manager) recordRunCorrection(reverted []Checkpoint, runID string) {
	fileSet := make(map[string]bool)
	for _, cp := range reverted {
		fileSet[cp.FilePath] = true
	}
	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}
	toolCall := ""
	if len(reverted) > 0 {
		toolCall = reverted[0].ToolCall
	}
	m.corrections = append(m.corrections, Correction{
		Files:    files,
		ToolCall: toolCall,
		RunID:    runID,
		Time:     time.Now(),
	})
}

func (m *Manager) List() []Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Checkpoint, len(m.checkpoints))
	copy(out, m.checkpoints)
	return out
}

func (m *Manager) Last() *Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.checkpoints) == 0 {
		return nil
	}
	cp := m.checkpoints[len(m.checkpoints)-1]
	return &cp
}

func (m *Manager) Redo() (*Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.redoStack) == 0 {
		return nil, fmt.Errorf("nothing to redo")
	}

	cp := m.redoStack[len(m.redoStack)-1]

	if err := util.AtomicWriteFile(cp.FilePath, []byte(cp.NewContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.checkpoints = append(m.checkpoints, cp)

	m.journalAppendLocked(journalEvent{Type: evRedo})

	return &cp, nil
}

func (m *Manager) CanRedo() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.redoStack) > 0
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints = nil
	m.redoStack = nil
	m.corrections = nil
	m.evictedRuns = nil
	m.fileExisted = nil

	m.journalAppendLocked(journalEvent{Type: evClear})
}

func (m *Manager) RecentCorrections() []Correction {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.corrections) == 0 {
		return nil
	}
	out := make([]Correction, len(m.corrections))
	copy(out, m.corrections)
	return out
}

func (m *Manager) ClearCorrections() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.corrections = nil
	m.journalAppendLocked(journalEvent{Type: evClearCorrections})
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
