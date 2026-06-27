package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// Session represents a single conversation session.
type Session struct {
	ID                   string                           `json:"id"`
	CreatedAt            time.Time                        `json:"created_at"`
	UpdatedAt            time.Time                        `json:"updated_at"`
	Title                string                           `json:"title"`
	Workspace            string                           `json:"workspace,omitempty"`
	Vendor               string                           `json:"vendor"`
	Endpoint             string                           `json:"endpoint"`
	Model                string                           `json:"model"`
	TokenUsage           provider.TokenUsage              `json:"token_usage,omitempty"`
	EndpointUsage        map[string]provider.TokenUsage   `json:"endpoint_usage,omitempty"`
	UsageHistory         []UsageEntry                     `json:"usage_history,omitempty"`
	Metrics              []metrics.MetricEvent            `json:"metrics,omitempty"`
	EndpointMetrics      map[string][]metrics.MetricEvent `json:"endpoint_metrics,omitempty"`
	Messages             []provider.Message               `json:"messages,omitempty"`
	TunnelEvents         []TunnelEvent                    `json:"tunnel_events,omitempty"`
	TunnelEventsComplete bool                             `json:"tunnel_events_complete,omitempty"`
	// Cost data stored as opaque JSON to avoid circular dependency with cost package.
	CostJSON []byte `json:"cost,omitempty"`
	// endpointStatsMu is nested inside higher-level session/bridge locks and only
	// guards the per-endpoint aggregate maps used by live readers/writers.
	endpointStatsMu sync.RWMutex
}

// TunnelEvent is the canonical persisted tunnel event for a session.
type TunnelEvent struct {
	EventID  string          `json:"event_id"`
	StreamID string          `json:"stream_id,omitempty"`
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// UsageEntry records a single LLM API call's token consumption within a session.
type UsageEntry struct {
	Timestamp time.Time           `json:"timestamp"`
	TurnIndex int                 `json:"turn_index"`
	Model     string              `json:"model,omitempty"`
	Vendor    string              `json:"vendor,omitempty"`
	Endpoint  string              `json:"endpoint,omitempty"`
	Usage     provider.TokenUsage `json:"usage"`
}

// Store is the interface for session persistence.
type Store interface {
	// Save persists the current state of a session (atomic write).
	Save(s *Session) error

	// Load retrieves a session by ID.
	Load(id string) (*Session, error)

	// List returns all sessions, sorted by UpdatedAt descending.
	List() ([]*Session, error)

	// Delete removes a session by ID.
	Delete(id string) error

	// ExportMarkdown renders a session as a markdown document.
	ExportMarkdown(id string) (string, error)

	// CleanupOlderThan removes sessions whose UpdatedAt is before the given time.
	CleanupOlderThan(before time.Time) (int, error)

	// LatestForWorkspace returns the most recently updated session for the
	// given workspace, or nil if none exists.
	LatestForWorkspace(workspace string) (*Session, error)

	// ListForWorkspace returns all sessions for the given workspace,
	// sorted by UpdatedAt descending (most recent first).
	ListForWorkspace(workspace string) ([]*Session, error)

	// AppendCheckpoint persists a checkpoint of compacted messages after summarize.
	// The checkpoint allows --resume to skip re-compacting old history.
	AppendCheckpoint(s *Session, compactedMessages []provider.Message, tokenCount int) error
}

// indexEntry is a lightweight record for fast session listing.
type indexEntry struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	MsgCount  int       `json:"msg_count"`
	Workspace string    `json:"workspace,omitempty"`
	Vendor    string    `json:"vendor,omitempty"`
	Endpoint  string    `json:"endpoint,omitempty"`
	Model     string    `json:"model,omitempty"`
}

// JSONLStore implements Store using JSONL files.
type JSONLStore struct {
	dir string // ~/.ggcode/sessions/
	// mu serializes all on-disk mutations (Save, Append*, EnsureMeta, Delete,
	// and the load/modify/save index sequence). Without this, concurrent
	// O_APPEND writers can interleave inside a single JSONL line (>4KB writes
	// are not atomic) and the index load/modify/save races silently lose
	// updates from the loser. See locks.md S3.
	mu                 sync.Mutex
	indexDirty         bool // set when updateIndex fails; triggers a later reconciliation pass
	maintenanceRunning bool
	lastMaintenance    time.Time
}

const sessionMaintenanceInterval = 30 * time.Second

// NewJSONLStore creates a store rooted at dir (creates dir if needed).
func NewJSONLStore(dir string) (*JSONLStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating session directory %s: %w", dir, err)
	}
	return &JSONLStore{dir: dir}, nil
}

// DefaultDir returns the default session directory.
func DefaultDir() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "sessions"), nil
}

// NewDefaultStore creates a store in the default directory.
func NewDefaultStore() (*JSONLStore, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return NewJSONLStore(dir)
}

// --- index helpers ---

func (s *JSONLStore) indexPath() string {
	return filepath.Join(s.dir, "index.json")
}

func (s *JSONLStore) loadIndex() ([]indexEntry, error) {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var idx []indexEntry
	if err := json.Unmarshal(data, &idx); err != nil {
		// Corrupt index — keep List fast and let a later reconciliation pass rebuild it.
		s.indexDirty = true
		return nil, nil
	}
	return idx, nil
}

func (s *JSONLStore) saveIndex(idx []indexEntry) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	// atomic write
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.indexPath())
}

func (s *JSONLStore) updateIndex(ses *Session) error {
	idx, err := s.loadIndex()
	if err != nil {
		s.indexDirty = true
		return err
	}
	found := false
	for i, e := range idx {
		if e.ID == ses.ID {
			idx[i] = sessionToIndexEntry(ses)
			found = true
			break
		}
	}
	if !found {
		idx = append(idx, sessionToIndexEntry(ses))
	}
	if err := s.saveIndex(idx); err != nil {
		s.indexDirty = true
		return err
	}
	s.indexDirty = false
	return nil
}

func (s *JSONLStore) removeFromIndex(id string) error {
	idx, err := s.loadIndex()
	if err != nil {
		return err
	}
	filtered := make([]indexEntry, 0, len(idx))
	for _, e := range idx {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}
	return s.saveIndex(filtered)
}

func sessionToIndexEntry(s *Session) indexEntry {
	return indexEntry{
		ID:        s.ID,
		Title:     s.Title,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		Workspace: s.Workspace,
		MsgCount:  len(s.Messages),
		Vendor:    s.Vendor,
		Endpoint:  s.Endpoint,
		Model:     s.Model,
	}
}

// --- JSONL helpers ---

// jsonlRecord is written one-per-line in the session file.
type jsonlRecord struct {
	Type                 string              `json:"type"` // "meta", "message", "cost", "usage", "metric", or "checkpoint"
	SessionID            string              `json:"session_id,omitempty"`
	Title                string              `json:"title,omitempty"`
	Workspace            string              `json:"workspace,omitempty"`
	Vendor               string              `json:"vendor,omitempty"`
	Endpoint             string              `json:"endpoint,omitempty"`
	Model                string              `json:"model,omitempty"`
	TokenUsage           provider.TokenUsage `json:"token_usage,omitempty"`
	CreatedAt            time.Time           `json:"created_at,omitempty"`
	UpdatedAt            time.Time           `json:"updated_at,omitempty"`
	TunnelEventsComplete bool                `json:"tunnel_events_complete,omitempty"`
	Message              *provider.Message   `json:"message,omitempty"`
	TunnelEvent          *TunnelEvent        `json:"tunnel_event,omitempty"`
	CostJSON             json.RawMessage     `json:"cost,omitempty"`
	// UsageEntry: per-turn usage record (type == "usage").
	UsageEntry *UsageEntry `json:"usage_entry,omitempty"`
	// MetricEvent: performance metric record (type == "metric").
	MetricEvent *metrics.MetricEvent `json:"metric_event,omitempty"`
	// Checkpoint fields: compacted messages snapshot after summarize.
	CheckpointMessages []provider.Message `json:"checkpoint_messages,omitempty"`
	CheckpointTokens   int                `json:"checkpoint_tokens,omitempty"`
}

func (s *JSONLStore) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".jsonl")
}

// HasUserInteraction returns true if the session contains at least one user
// message with actual text content. Sessions without user interaction (e.g.,
// created then immediately closed) should not be persisted to avoid accumulating
// empty session files.
func (s *Session) HasUserInteraction() bool {
	for _, m := range s.Messages {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return true
			}
		}
	}
	return false
}

// Save writes the full session as a JSONL file (atomic).
// If the session has no user interaction, the file is deleted instead.
func (s *JSONLStore) Save(ses *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ses.UpdatedAt = time.Now()

	path := s.sessionPath(ses.ID)

	// No user interaction — remove the file and index entry instead of saving.
	if !ses.HasUserInteraction() {
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("removing empty session file: %w", err)
		}
		return s.removeFromIndex(ses.ID)
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	// Meta record
	meta := jsonlRecord{
		Type:                 "meta",
		SessionID:            ses.ID,
		Title:                ses.Title,
		Workspace:            ses.Workspace,
		Vendor:               ses.Vendor,
		Endpoint:             ses.Endpoint,
		Model:                ses.Model,
		TokenUsage:           ses.TokenUsage,
		CreatedAt:            ses.CreatedAt,
		UpdatedAt:            ses.UpdatedAt,
		TunnelEventsComplete: ses.TunnelEventsComplete,
	}
	if err := enc.Encode(meta); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encoding meta: %w", err)
	}

	// Message records
	for i := range ses.Messages {
		msg := ses.Messages[i]
		rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg}
		if err := enc.Encode(rec); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encoding message %d: %w", i, err)
		}
	}

	for i := range ses.TunnelEvents {
		ev := ses.TunnelEvents[i]
		rec := jsonlRecord{Type: "tunnel_event", SessionID: ses.ID, TunnelEvent: &ev}
		if err := enc.Encode(rec); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encoding tunnel event %d: %w", i, err)
		}
	}

	// Cost record (if present)
	if len(ses.CostJSON) > 0 {
		costRec := jsonlRecord{Type: "cost", SessionID: ses.ID, CostJSON: ses.CostJSON}
		if err := enc.Encode(costRec); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encoding cost: %w", err)
		}
	}

	// Usage history records
	for i := range ses.UsageHistory {
		entry := ses.UsageHistory[i]
		rec := jsonlRecord{Type: "usage", SessionID: ses.ID, UsageEntry: &entry}
		if err := enc.Encode(rec); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encoding usage %d: %w", i, err)
		}
	}

	// Metric records
	for i := range ses.Metrics {
		m := ses.Metrics[i]
		rec := jsonlRecord{Type: "metric", SessionID: ses.ID, MetricEvent: &m}
		if err := enc.Encode(rec); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encoding metric %d: %w", i, err)
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmp, s.sessionPath(ses.ID)); err != nil {
		return fmt.Errorf("renaming session file: %w", err)
	}

	return s.updateIndex(ses)
}

// Load reads a session from its JSONL file.
func (s *JSONLStore) Load(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSession(id)
}

// loadSession is the lock-free internal version of Load.
func (s *JSONLStore) loadSession(id string) (*Session, error) {
	path := s.sessionPath(id)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session %s not found", id)
		}
		return nil, err
	}
	defer f.Close()

	ses := &Session{ID: id}
	sc := bufio.NewScanner(f)
	// Increase buffer for large tool outputs
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	// Single-pass scan: track the last checkpoint and all post-checkpoint records.
	// We only keep lightweight non-checkpoint records; checkpoint messages are
	// stored once for the latest checkpoint only.
	type lightweightEntry struct {
		recType string
		record  jsonlRecord
	}
	var (
		metaRecords    []jsonlRecord // always accumulate meta for metadata
		lastCpMessages []provider.Message
		postCPEntries  []lightweightEntry // entries after the last checkpoint
		haveCheckpoint bool
	)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // skip malformed lines
		}

		switch rec.Type {
		case "meta":
			metaRecords = append(metaRecords, rec)
		case "checkpoint":
			// New checkpoint replaces any previous one; discard old post-CP entries
			lastCpMessages = rec.CheckpointMessages
			postCPEntries = nil
			haveCheckpoint = true
		case "message", "cost", "tunnel_event", "usage", "metric":
			if haveCheckpoint {
				postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
			} else {
				// No checkpoint yet — treat as legacy
				postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading session %s: %w", id, err)
	}

	// Apply metadata from meta records (always the latest meta wins)
	for _, rec := range metaRecords {
		ses.Title = rec.Title
		ses.Workspace = rec.Workspace
		ses.Vendor = rec.Vendor
		ses.Endpoint = rec.Endpoint
		ses.Model = rec.Model
		ses.TokenUsage = rec.TokenUsage
		ses.CreatedAt = rec.CreatedAt
		ses.UpdatedAt = rec.UpdatedAt
		ses.TunnelEventsComplete = rec.TunnelEventsComplete
	}

	// Build messages
	if haveCheckpoint && len(lastCpMessages) > 0 {
		ses.Messages = make([]provider.Message, len(lastCpMessages))
		copy(ses.Messages, lastCpMessages)
	}

	for _, e := range postCPEntries {
		switch e.recType {
		case "message":
			if e.record.Message != nil {
				ses.Messages = append(ses.Messages, *e.record.Message)
			}
		case "cost":
			if e.record.CostJSON != nil {
				ses.CostJSON = []byte(e.record.CostJSON)
			}
		case "tunnel_event":
			if e.record.TunnelEvent != nil {
				ses.TunnelEvents = append(ses.TunnelEvents, *e.record.TunnelEvent)
			}
		case "usage":
			if e.record.UsageEntry != nil {
				ses.UsageHistory = append(ses.UsageHistory, *e.record.UsageEntry)
			}
		case "metric":
			if e.record.MetricEvent != nil {
				ses.Metrics = append(ses.Metrics, *e.record.MetricEvent)
			}
		}
	}

	if ses.CreatedAt.IsZero() {
		ses.CreatedAt = time.Now()
	}
	if ses.UpdatedAt.IsZero() {
		ses.UpdatedAt = ses.CreatedAt
	}
	ses.RebuildEndpointStats()
	return ses, nil
}

// List returns all sessions sorted by UpdatedAt descending (uses index for speed).
func (s *JSONLStore) List() ([]*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	if len(idx) == 0 {
		changed, err := s.repairIndex(idx)
		if err != nil {
			return nil, err
		}
		if changed {
			idx, err = s.loadIndex()
			if err != nil {
				return nil, err
			}
		}
	}

	// Sort by UpdatedAt descending
	sort.Slice(idx, func(i, j int) bool {
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})
	s.scheduleMaintenanceLocked()

	result := make([]*Session, 0, len(idx))
	for _, e := range idx {
		result = append(result, &Session{
			ID:        e.ID,
			Title:     e.Title,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
			Workspace: e.Workspace,
			Vendor:    e.Vendor,
			Endpoint:  e.Endpoint,
			Model:     e.Model,
		})
	}
	return result, nil
}

func (s *JSONLStore) scheduleMaintenanceLocked() {
	if s.maintenanceRunning {
		return
	}
	if !s.indexDirty && !s.lastMaintenance.IsZero() && time.Since(s.lastMaintenance) < sessionMaintenanceInterval {
		return
	}

	s.maintenanceRunning = true
	safego.Go("session.runMaintenance", func() { s.runMaintenance() })
}

func (s *JSONLStore) runMaintenance() {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		s.lastMaintenance = time.Now()
		s.maintenanceRunning = false
	}()

	idx, err := s.loadIndex()
	if err != nil {
		return
	}
	changed, err := s.repairIndex(idx)
	if err != nil {
		return
	}
	if changed {
		idx, err = s.loadIndex()
		if err != nil {
			return
		}
	}

	validIdx, cleaned := s.pruneInvalidIndexEntries(idx)
	if !cleaned {
		s.indexDirty = false
		return
	}
	if err := s.saveIndex(validIdx); err != nil {
		s.indexDirty = true
		return
	}
	s.indexDirty = false
}

func (s *JSONLStore) pruneInvalidIndexEntries(idx []indexEntry) ([]indexEntry, bool) {
	cleaned := false
	validIdx := make([]indexEntry, 0, len(idx))
	for _, e := range idx {
		ses, loadErr := s.loadSession(e.ID)
		if loadErr != nil {
			_ = os.Remove(s.sessionPath(e.ID))
			cleaned = true
			continue
		}
		if !ses.HasUserInteraction() {
			_ = os.Remove(s.sessionPath(e.ID))
			cleaned = true
			continue
		}
		validIdx = append(validIdx, e)
	}
	return validIdx, cleaned
}

// repairIndex scans the sessions directory and reconciles with the index.
// Returns true if the index was modified (written back).
func (s *JSONLStore) repairIndex(idx []indexEntry) (bool, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return false, err
	}

	// Build set of IDs present on disk
	diskIDs := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		diskIDs[id] = true
	}

	changed := s.indexDirty
	newIdx := make([]indexEntry, 0, len(idx))

	for _, e := range idx {
		if !diskIDs[e.ID] {
			// Index entry has no file — remove
			changed = true
			continue
		}
		newIdx = append(newIdx, e)
	}

	// Add entries for files missing from index
	for id := range diskIDs {
		found := false
		for _, e := range newIdx {
			if e.ID == id {
				found = true
				break
			}
		}
		if !found {
			ses, loadErr := s.loadSession(id)
			if loadErr == nil {
				if ses.HasUserInteraction() {
					newIdx = append(newIdx, sessionToIndexEntry(ses))
				} else {
					_ = os.Remove(s.sessionPath(id))
				}
				changed = true
			}
		}
	}

	if changed {
		if err := s.saveIndex(newIdx); err != nil {
			return false, err
		}
		s.indexDirty = false
	}
	return changed, nil
}

// Delete removes a session file and its index entry.
func (s *JSONLStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return s.removeFromIndex(id)
}

// LatestForWorkspace returns the most recently updated session for the
// given workspace that has at least one message, or nil if none exists.
// Uses the index directly (not List) to access MsgCount.
func (s *JSONLStore) LatestForWorkspace(workspace string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	// Normalize the workspace for comparison so symlinks and path
	// inconsistencies don't prevent finding sessions.
	normalizedWorkspace := NormalizeWorkspacePath(workspace)

	// Sort by UpdatedAt descending.
	sort.Slice(idx, func(i, j int) bool {
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})

	for _, e := range idx {
		if NormalizeWorkspacePath(e.Workspace) == normalizedWorkspace && e.MsgCount > 0 {
			return &Session{
				ID:        e.ID,
				Title:     e.Title,
				CreatedAt: e.CreatedAt,
				UpdatedAt: e.UpdatedAt,
				Workspace: e.Workspace,
				Vendor:    e.Vendor,
				Endpoint:  e.Endpoint,
				Model:     e.Model,
			}, nil
		}
	}
	return nil, nil
}

// ListForWorkspace returns all sessions for the given workspace,
// sorted by UpdatedAt descending (most recent first).
// Uses the index directly (not List) for fast listing.
func (s *JSONLStore) ListForWorkspace(workspace string) ([]*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	// Normalize the workspace for comparison.
	normalizedWorkspace := NormalizeWorkspacePath(workspace)

	// Sort by UpdatedAt descending.
	sort.Slice(idx, func(i, j int) bool {
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})

	result := make([]*Session, 0, len(idx))
	for _, e := range idx {
		if NormalizeWorkspacePath(e.Workspace) == normalizedWorkspace {
			result = append(result, &Session{
				ID:        e.ID,
				Title:     e.Title,
				CreatedAt: e.CreatedAt,
				UpdatedAt: e.UpdatedAt,
				Workspace: e.Workspace,
				Vendor:    e.Vendor,
				Endpoint:  e.Endpoint,
				Model:     e.Model,
			})
		}
	}
	return result, nil
}

// ExportMarkdown renders a session as a markdown document.
func (s *JSONLStore) ExportMarkdown(id string) (string, error) {
	ses, err := s.Load(id)
	if err != nil {
		return "", err
	}
	return ExportSessionMarkdown(ses), nil
}

// ExportSessionMarkdown renders a Session to markdown.
func ExportSessionMarkdown(ses *Session) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", ses.Title))
	sb.WriteString(fmt.Sprintf("**Session:** %s\n", ses.ID))
	sb.WriteString(fmt.Sprintf("**Created:** %s\n", ses.CreatedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**Updated:** %s\n", ses.UpdatedAt.Format(time.RFC3339)))
	if ses.Vendor != "" {
		sb.WriteString(fmt.Sprintf("**Vendor:** %s", ses.Vendor))
		if ses.Endpoint != "" {
			sb.WriteString(fmt.Sprintf(" / %s", ses.Endpoint))
		}
		if ses.Model != "" {
			sb.WriteString(fmt.Sprintf(" / %s", ses.Model))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("**Messages:** %d\n\n---\n\n", len(ses.Messages)))

	for _, msg := range ses.Messages {
		switch msg.Role {
		case "user":
			sb.WriteString("## User\n\n")
		case "assistant":
			sb.WriteString("## Assistant\n\n")
		case "system":
			sb.WriteString("## System\n\n")
		default:
			sb.WriteString(fmt.Sprintf("## %s\n\n", msg.Role))
		}
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				sb.WriteString(block.Text)
				sb.WriteString("\n\n")
			case "tool_use":
				sb.WriteString(fmt.Sprintf("**Tool Call:** `%s`\n", block.ToolName))
				if block.Input != nil {
					sb.WriteString(fmt.Sprintf("```json\n%s\n```\n", string(block.Input)))
				}
				sb.WriteString("\n")
			case "tool_result":
				sb.WriteString(fmt.Sprintf("**Tool Result** (error=%v):\n", block.IsError))
				sb.WriteString("```\n")
				sb.WriteString(block.Output)
				sb.WriteString("\n```\n\n")
			}
		}
		sb.WriteString("---\n\n")
	}
	return sb.String()
}

// CleanupOlderThan removes sessions older than the given time. Returns count removed.
func (s *JSONLStore) CleanupOlderThan(before time.Time) (int, error) {
	sessions, err := s.List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, ses := range sessions {
		if ses.UpdatedAt.Before(before) {
			if err := s.Delete(ses.ID); err != nil {
				return removed, fmt.Errorf("deleting session %s: %w", ses.ID, err)
			}
			removed++
		}
	}
	return removed, nil
}

// AppendMessage atomically appends a single message to the session's JSONL file.
// This is more efficient than Save() for incremental updates.
// AppendMessage persists a message to the session's JSONL file and updates
// the Session object in place (Messages, UpdatedAt, Title). The caller must
// ensure no concurrent access to the Session object.
func (s *JSONLStore) AppendMessage(ses *Session, msg provider.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg}
	if err := appendRecordLine(path, rec); err != nil {
		return err
	}

	ses.Messages = append(ses.Messages, msg)
	ses.UpdatedAt = time.Now()

	// Auto-generate title from first user message
	if ses.Title == "" {
		for _, m := range ses.Messages {
			if m.Role == "user" {
				for _, b := range m.Content {
					if b.Type == "text" && b.Text != "" {
						ses.Title = util.Truncate(b.Text, 60)
						break
					}
				}
				break
			}
		}
	}

	return s.updateIndex(ses)
}

// AppendMessageToDisk persists a message to the session's JSONL file and
// updates the index, but does NOT modify the Session object. Use this when
// the caller manages Session mutations under its own lock (e.g. sessionMutex
// in the TUI), and only needs the disk write to happen outside that lock.
func (s *JSONLStore) AppendMessageToDisk(ses *Session, msg provider.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg}
	if err := appendRecordLine(path, rec); err != nil {
		return err
	}

	return s.updateIndex(ses)
}

// AppendTunnelEventToDisk persists a canonical tunnel event to the session's
// JSONL file and updates the index, but does NOT mutate the Session object.
func (s *JSONLStore) AppendTunnelEventToDisk(ses *Session, ev TunnelEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{Type: "tunnel_event", SessionID: ses.ID, TunnelEvent: &ev}
	if err := appendRecordLine(path, rec); err != nil {
		return err
	}

	return s.updateIndex(ses)
}

// AppendMetaToDisk persists the latest session metadata as an additional meta
// record. Load applies the last meta record, so this updates fields like title,
// model, and token usage without rewriting the full session file.
func (s *JSONLStore) AppendMetaToDisk(ses *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ses.HasUserInteraction() {
		return nil
	}
	path := s.sessionPath(ses.ID)
	rec := jsonlRecord{
		Type:                 "meta",
		SessionID:            ses.ID,
		Title:                ses.Title,
		Workspace:            ses.Workspace,
		Vendor:               ses.Vendor,
		Endpoint:             ses.Endpoint,
		Model:                ses.Model,
		TokenUsage:           ses.TokenUsage,
		CreatedAt:            ses.CreatedAt,
		UpdatedAt:            ses.UpdatedAt,
		TunnelEventsComplete: ses.TunnelEventsComplete,
	}
	if err := appendRecordLine(path, rec); err != nil {
		return err
	}
	return s.updateIndex(ses)
}

// AppendUsageEntry persists a per-turn usage record to the session's JSONL file.
// Each record captures the token consumption of a single LLM API call.
func (s *JSONLStore) AppendUsageEntry(ses *Session, entry UsageEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ses.HasUserInteraction() {
		return nil
	}
	path := s.sessionPath(ses.ID)
	rec := jsonlRecord{
		Type:       "usage",
		SessionID:  ses.ID,
		UsageEntry: &entry,
	}
	return appendRecordLine(path, rec)
}

// AppendMetric persists a performance metric record to the session's JSONL file.
func (s *JSONLStore) AppendMetric(ses *Session, m metrics.MetricEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ses.HasUserInteraction() {
		return nil
	}
	path := s.sessionPath(ses.ID)
	rec := jsonlRecord{
		Type:        "metric",
		SessionID:   ses.ID,
		MetricEvent: &m,
	}
	return appendRecordLine(path, rec)
}

// EnsureMeta writes the meta record if the session file doesn't exist yet.
// If the session has no user interaction, no file is created.
func (s *JSONLStore) EnsureMeta(ses *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}

	// Don't create a meta file for sessions with no user interaction.
	if !ses.HasUserInteraction() {
		return nil
	}

	// Write meta record as the first line
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	meta := jsonlRecord{
		Type:                 "meta",
		SessionID:            ses.ID,
		Title:                ses.Title,
		Workspace:            ses.Workspace,
		Vendor:               ses.Vendor,
		Endpoint:             ses.Endpoint,
		Model:                ses.Model,
		TokenUsage:           ses.TokenUsage,
		CreatedAt:            ses.CreatedAt,
		UpdatedAt:            ses.UpdatedAt,
		TunnelEventsComplete: ses.TunnelEventsComplete,
	}
	if err := enc.Encode(meta); err != nil {
		os.Remove(path)
		return err
	}

	return s.updateIndex(ses)
}

// NewSession creates a new Session with a generated ID.
func NewSession(vendor, endpoint, model string) *Session {
	now := time.Now()
	return &Session{
		ID:              generateID(),
		CreatedAt:       now,
		UpdatedAt:       now,
		Workspace:       CurrentWorkspacePath(),
		Vendor:          vendor,
		Endpoint:        endpoint,
		Model:           model,
		EndpointUsage:   make(map[string]provider.TokenUsage),
		EndpointMetrics: make(map[string][]metrics.MetricEvent),
		Title:           "New session",
	}
}

func LastTurnIndex(ses *Session) int {
	if ses == nil {
		return 0
	}
	last := 0
	if n := len(ses.UsageHistory); n > 0 && ses.UsageHistory[n-1].TurnIndex > last {
		last = ses.UsageHistory[n-1].TurnIndex
	}
	if n := len(ses.Metrics); n > 0 && ses.Metrics[n-1].TurnIndex > last {
		last = ses.Metrics[n-1].TurnIndex
	}
	return last
}

func CurrentWorkspacePath() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return NormalizeWorkspacePath(wd)
}

func NormalizeWorkspacePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405"), time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(b))
}

// Dir returns the store's directory path.
func (s *JSONLStore) Dir() string {
	return s.dir
}

// AppendCheckpoint appends a checkpoint record to the session JSONL file.
// The checkpoint captures the compacted messages state after a summarize operation,
// so that future --resume operations can skip re-compacting old history.
// AppendCheckpoint persists a checkpoint (compaction snapshot) to the session's
// JSONL file and updates the Session object in place. The caller must ensure
// no concurrent access to the Session object.
func (s *JSONLStore) AppendCheckpoint(ses *Session, compactedMessages []provider.Message, tokenCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	// Shallow copy to avoid slice aliasing with the caller's slice.
	// JSON serialization below breaks any interior reference sharing.
	msgs := make([]provider.Message, len(compactedMessages))
	copy(msgs, compactedMessages)

	rec := jsonlRecord{
		Type:               "checkpoint",
		SessionID:          ses.ID,
		CheckpointMessages: msgs,
		CheckpointTokens:   tokenCount,
	}
	if err := appendRecordLine(path, rec); err != nil {
		return fmt.Errorf("encoding checkpoint: %w", err)
	}

	ses.UpdatedAt = time.Now()
	return s.updateIndex(ses)
}

// AppendCheckpointToDisk persists a checkpoint to the session's JSONL file and
// updates the index, but does NOT modify the Session object. Use this when the
// caller manages Session mutations under its own lock and only needs the disk
// write to happen outside that lock.
func (s *JSONLStore) AppendCheckpointToDisk(ses *Session, compactedMessages []provider.Message, tokenCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	msgs := make([]provider.Message, len(compactedMessages))
	copy(msgs, compactedMessages)

	rec := jsonlRecord{
		Type:               "checkpoint",
		SessionID:          ses.ID,
		CheckpointMessages: msgs,
		CheckpointTokens:   tokenCount,
	}
	if err := appendRecordLine(path, rec); err != nil {
		return fmt.Errorf("encoding checkpoint: %w", err)
	}

	return s.updateIndex(ses)
}

// appendRecordLine encodes rec to a single buffer then writes it in one
// os.File.Write call. Combined with the store mutex, this guarantees no JSONL
// line interleaving even for records larger than PIPE_BUF.
func appendRecordLine(path string, rec jsonlRecord) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
