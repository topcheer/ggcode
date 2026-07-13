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
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// Session represents a single conversation session.
type Session struct {
	ID              string                           `json:"id"`
	CreatedAt       time.Time                        `json:"created_at"`
	UpdatedAt       time.Time                        `json:"updated_at"`
	Title           string                           `json:"title"`
	Workspace       string                           `json:"workspace,omitempty"`
	Vendor          string                           `json:"vendor"`
	Endpoint        string                           `json:"endpoint"`
	Model           string                           `json:"model"`
	TokenUsage      provider.TokenUsage              `json:"token_usage,omitempty"`
	EndpointUsage   map[string]provider.TokenUsage   `json:"endpoint_usage,omitempty"`
	UsageHistory    []UsageEntry                     `json:"usage_history,omitempty"`
	Metrics         []metrics.MetricEvent            `json:"metrics,omitempty"`
	EndpointMetrics map[string][]metrics.MetricEvent `json:"endpoint_metrics,omitempty"`
	// Messages holds ALL message records from the JSONL file — the full
	// conversation history from the very first user message to the latest.
	// This is what the TUI renders. It is NEVER overwritten by compaction.
	//
	// ⚠️ DO NOT assign agent.Messages() (compacted) to this field.
	// ⚠️ DO NOT call Save() to persist this — Save() rewrites the entire
	//    file and will destroy pre-compaction message records.
	// Use AppendMessageToDisk() for incremental writes only.
	Messages []provider.Message `json:"messages,omitempty"`

	// ContextMessages holds the compacted messages for LLM context
	// restoration: last checkpoint + post-checkpoint messages. This is
	// what gets fed to the agent on session reload so the LLM sees the
	// summarized history, not the full log. Computed by loadSession(),
	// not persisted to JSONL.
	//
	// ⚠️ Only RestoreSessionIntoAgent() should read this field.
	// ⚠️ TUI rendering must use Messages, NOT ContextMessages.
	ContextMessages        []provider.Message `json:"-"`
	CheckpointTokens       int                `json:"-"`
	CheckpointMessageCount int                `json:"-"`
	TunnelEvents           []TunnelEvent      `json:"tunnel_events,omitempty"`
	TunnelEventsComplete   bool               `json:"tunnel_events_complete,omitempty"`
	// Cost data stored as opaque JSON to avoid circular dependency with cost package.
	CostJSON []byte `json:"cost,omitempty"`
	// PermissionMode stores the session-scoped permission mode (e.g. "auto", "bypass").
	// When non-empty, this overrides the global default_mode on session resume.
	// It is never written to the config file — only persisted with the session.
	PermissionMode string `json:"permission_mode,omitempty"`
	// SidebarVisible stores the session-scoped sidebar visibility preference.
	// When non-nil, this overrides the global sidebar_visible on session resume.
	// It is never written to the config file — only persisted with the session.
	// Uses *bool to distinguish "never set" (nil) from "explicitly hidden" (false).
	SidebarVisible *bool `json:"sidebar_visible,omitempty"`
	// ContextWindow stores the session-scoped context window size.
	// When > 0, this overrides the endpoint/per-model config on session resume.
	ContextWindow int `json:"context_window,omitempty"`
	// MaxTokens stores the session-scoped max output token limit.
	// When > 0, this overrides the endpoint/per-model config on session resume.
	MaxTokens int `json:"max_tokens,omitempty"`
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

	// AppendCheckpoint persists a checkpoint after compaction.
	// summaryMsgID is the ID of the summary message in JSONL — restore
	// scans from this message forward to rebuild context.
	// lastMsgID is the ID of the last message in the snapshot before compaction —
	// restore uses this to find "extra" messages (post-compaction additions).
	AppendCheckpoint(s *Session, summaryMsgID, lastMsgID string, tokenCount int) error

	// AppendMetaToDisk appends a single meta record to the session's JSONL file
	// without rewriting the entire file. Use this for lightweight metadata updates
	// (e.g., permission_mode, sidebar_visible) instead of Save.
	AppendMetaToDisk(s *Session) error
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

// MaxTunnelEvents caps the number of tunnel events kept in memory and
// rewritten by Save(). Tunnel events are ephemeral streaming records for
// mobile relay replay; the relay server has its own SQLite persistence
// for full replay history. Keeping only the most recent events bounds
// memory usage and prevents session files from growing unboundedly
// (a long session can accumulate 200K+ events = 100MB+).
const MaxTunnelEvents = 2000

// MaxContextMessages caps the number of messages loaded into the agent's LLM
// context when a session has no checkpoint (compaction summary). Without this
// cap, a long session with 10K+ messages and no compaction would load all of
// them into context on restore, potentially consuming 2M+ tokens and exceeding
// any model's context window. The full message history is preserved in
// ses.Messages for TUI rendering; only ContextMessages (what the agent sees)
// is truncated. When the cap is applied, a synthetic system note is prepended
// to inform the agent that earlier context was truncated.
const MaxContextMessages = 200

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
	return s.loadIndexImpl(true)
}

// loadIndexNoRepair is like loadIndex but skips the automatic repairIndex call
// on corruption. Use this from contexts that already hold the index flock
// (updateIndex, removeFromIndex) to avoid a deadlock: repairIndex itself
// calls lockIndexFile, which would block forever waiting for the lock we
// already hold.
func (s *JSONLStore) loadIndexNoRepair() ([]indexEntry, error) {
	return s.loadIndexImpl(false)
}

func (s *JSONLStore) loadIndexImpl(canRepair bool) ([]indexEntry, error) {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var idx []indexEntry
	if err := json.Unmarshal(data, &idx); err != nil {
		// Corrupt index — rebuild from disk to avoid losing entries.
		debug.Log("session", "loadIndex: corrupt session index, rebuilding from disk: %v", err)
		s.indexDirty = true
		if !canRepair {
			// Caller holds the flock — cannot repair here (would deadlock).
			// Return nil; the next unlocked loadIndex call will repair.
			return nil, nil
		}
		repaired, repairErr := s.repairIndex(nil)
		if repairErr != nil {
			debug.Log("session", "loadIndex: repairIndex failed: %v", repairErr)
			return nil, nil
		}
		if repaired {
			return s.loadIndexFromDisk()
		}
		return nil, nil
	}
	return idx, nil
}

// loadIndexFromDisk reads and parses the index file without corruption recovery.
func (s *JSONLStore) loadIndexFromDisk() ([]indexEntry, error) {
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var idx []indexEntry
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, nil
	}
	return idx, nil
}

func (s *JSONLStore) saveIndex(idx []indexEntry) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	// atomic write with fsync for crash durability (matches Save() pattern)
	tmp := s.indexPath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating index temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("writing index temp file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("syncing index temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing index temp file: %w", err)
	}
	return os.Rename(tmp, s.indexPath())
}

func (s *JSONLStore) updateIndex(ses *Session) error {
	unlock, lockErr := lockIndexFile(s.indexPath())
	if lockErr != nil {
		debug.Log("session", "updateIndex: failed to acquire index lock: %v", lockErr)
		s.indexDirty = true
		// Proceed without lock — better to try than skip entirely.
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	idx, err := s.loadIndexNoRepair()
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
	unlock, lockErr := lockIndexFile(s.indexPath())
	if lockErr != nil {
		debug.Log("session", "removeFromIndex: failed to acquire index lock: %v", lockErr)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	idx, err := s.loadIndexNoRepair()
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
	// Session-scoped preferences.
	PermissionMode string `json:"permission_mode,omitempty"`
	SidebarVisible *bool  `json:"sidebar_visible,omitempty"`
	ContextWindow  int    `json:"context_window,omitempty"`
	MaxTokens      int    `json:"max_tokens,omitempty"`
	// Checkpoint fields: after compaction, only the summary message ID is stored.
	// The summary message itself is written to JSONL as a type:"message" record.
	// Restore scans from summary_msg_id forward to rebuild context.
	CheckpointSummaryMsgID string `json:"checkpoint_summary_msg_id,omitempty"`
	CheckpointLastMsgID    string `json:"checkpoint_last_msg_id,omitempty"`
	CheckpointTokens       int    `json:"checkpoint_tokens,omitempty"`
	// Legacy field — kept for migration only. New code uses CheckpointSummaryMsgID.
	CheckpointMessages []provider.Message `json:"checkpoint_messages,omitempty"`
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
//
// ⚠️ WARNING: Save() REWRITES the entire JSONL file from scratch using
// ses.Messages. If ses.Messages has been replaced by compacted messages
// (e.g. via agent.Messages()), all pre-compaction message records will
// be PERMANENTLY LOST.
//
// For incremental message persistence after each agent turn, use
// AppendMessageToDisk() instead. Save() should only be used for:
//   - Initial session creation
//   - Full metadata refresh
//   - Desktop non-compaction path (with explicit guard)
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
		PermissionMode:       ses.PermissionMode,
		SidebarVisible:       ses.SidebarVisible,
		ContextWindow:        ses.ContextWindow,
		MaxTokens:            ses.MaxTokens,
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

	// Sync before Close to ensure data reaches disk before the atomic rename.
	// Without this, a crash after Close but before the OS flushes dirty pages
	// could leave the renamed file empty or partially written.
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		debug.Log("session", "Save: failed to sync temp file for session %s: %v", ses.ID, err)
		return fmt.Errorf("syncing temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		debug.Log("session", "Save: failed to close temp file for session %s: %v", ses.ID, err)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmp, s.sessionPath(ses.ID)); err != nil {
		debug.Log("session", "Save: failed to rename session file for %s: %v", ses.ID, err)
		return fmt.Errorf("renaming session file: %w", err)
	}

	debug.Log("session", "Save: wrote session %s (%d messages)", ses.ID, len(ses.Messages))
	return s.updateIndex(ses)
}

// Load reads a session from its JSONL file.
func (s *JSONLStore) Load(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSession(id)
}

// localLightweightEntry pairs a record type tag with its raw JSONL record.
// Used internally by loadSession to track post-checkpoint entries.
type localLightweightEntry struct {
	recType string
	record  jsonlRecord
}

// loadSession is the lock-free internal version of Load.
func (s *JSONLStore) loadSession(id string) (*Session, error) {
	path := s.sessionPath(id)

	// Migrate legacy JSONL records: backfill missing message IDs and convert
	// old checkpoint format to new summary_msg_id format. This is a no-op
	// if the file is already fully migrated.
	if migrated, err := s.migrateMessageIDs(id); err != nil {
		debug.Log("session", "loadSession: migration failed for %s: %v (continuing with original file)", id, err)
	} else if migrated > 0 {
		debug.Log("session", "loadSession: migrated %d message IDs in session %s", migrated, id)
	}

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
	// IMPORTANT: usage and metric records are cumulative history — they must
	// NOT be cleared when a checkpoint is encountered. Only message/tunnel/cost
	// entries follow checkpoint semantics (avoid replaying old messages).
	type lightweightEntry = localLightweightEntry
	var (
		metaRecords        []jsonlRecord // always accumulate meta for metadata
		lastCpMessages     []provider.Message
		lastCpSummaryMsgID string
		lastCpLastMsgID    string
		lastCpTokens       int
		allMessages        []jsonlRecord      // ALL message records (never discarded by checkpoint)
		postCPEntries      []lightweightEntry // cost entries after last checkpoint
		postCPTunnelEvs    []jsonlRecord      // tunnel events after last checkpoint (bounded)
		allUsage           []jsonlRecord      // ALL usage records (never cleared by checkpoint)
		allMetrics         []jsonlRecord      // ALL metric records (never cleared by checkpoint)
		haveCheckpoint     bool
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
			// New format: checkpoint_summary_msg_id points to a message in JSONL.
			// Old format: checkpoint_messages contains full message snapshot.
			if rec.CheckpointSummaryMsgID != "" {
				lastCpSummaryMsgID = rec.CheckpointSummaryMsgID
				lastCpLastMsgID = rec.CheckpointLastMsgID
				lastCpTokens = rec.CheckpointTokens
				haveCheckpoint = true
				// postCPEntries are NOT cleared — we'll filter by ID later.
			} else if len(rec.CheckpointMessages) > 0 {
				// Legacy checkpoint: migrate — find summary message, write to JSONL.
				// For now, use the old format directly during migration period.
				lastCpMessages = rec.CheckpointMessages
				lastCpLastMsgID = "" // legacy checkpoints don't have last_msg_id
				lastCpTokens = rec.CheckpointTokens
				postCPEntries = nil
				postCPTunnelEvs = nil
				haveCheckpoint = true
			}
		case "usage":
			// Usage records are cumulative token history — never discard.
			allUsage = append(allUsage, rec)
		case "metric":
			// Metric records are cumulative performance history — never discard.
			allMetrics = append(allMetrics, rec)
		case "message":
			// ALL message records are kept for full history rendering.
			allMessages = append(allMessages, rec)
			// Also track for ContextMessages (checkpoint + post-checkpoint).
			postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
		case "cost":
			postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
		case "tunnel_event":
			postCPTunnelEvs = append(postCPTunnelEvs, rec)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading session %s: %w", id, err)
	}

	// Apply metadata from meta records (always the latest meta wins)
	for _, rec := range metaRecords {
		ses.Title = rec.Title
		// Only overwrite workspace if the meta record has one. Older sessions
		// (or sessions created before the workspace field existed) may have
		// empty workspace in their meta records. Overwriting with "" would
		// make the session unfindable by ListForWorkspace/LatestForWorkspace.
		if rec.Workspace != "" {
			ses.Workspace = rec.Workspace
		}
		ses.Vendor = rec.Vendor
		ses.Endpoint = rec.Endpoint
		ses.Model = rec.Model
		ses.TokenUsage = rec.TokenUsage
		ses.CreatedAt = rec.CreatedAt
		ses.UpdatedAt = rec.UpdatedAt
		ses.TunnelEventsComplete = rec.TunnelEventsComplete
		if rec.PermissionMode != "" {
			ses.PermissionMode = rec.PermissionMode
		}
		if rec.SidebarVisible != nil {
			ses.SidebarVisible = rec.SidebarVisible
		}
		if rec.ContextWindow > 0 {
			ses.ContextWindow = rec.ContextWindow
		}
		if rec.MaxTokens > 0 {
			ses.MaxTokens = rec.MaxTokens
		}
	}

	// Fallback: if no meta record contained a workspace (sessions created
	// before the workspace field was tracked), assign the current workspace.
	// Without this, the session is invisible to ListForWorkspace and can
	// never be auto-loaded on restart.
	if ses.Workspace == "" {
		ses.Workspace = CurrentWorkspacePath()
		debug.Log("session", "loadSession %s: workspace was empty, falling back to %s", id, ses.Workspace)
	}

	// Deduplicate message records in corrupted JSONL files.
	// A now-fixed bug (StartRunTracking after restore) caused all restored
	// messages to be re-appended on every agent run, doubling the file on
	// each restart. This defensive dedup ensures clean in-memory state even
	// for files that were corrupted before the fix.
	allMessages = dedupMessageRecords(allMessages)
	postCPEntries = dedupLightweightEntries(postCPEntries)

	// ── ses.Messages: ALL message records from the file (for rendering) ──
	// This is the FULL conversation history. Every message record ever
	// appended to the JSONL file is loaded here, regardless of checkpoints.
	// The TUI uses this to render the complete conversation on reload.
	//
	// ⚠️ Never filter or truncate this by checkpoint — that would silently
	// destroy conversation history the user expects to see.
	for _, rec := range allMessages {
		if rec.Message != nil {
			ses.Messages = append(ses.Messages, *rec.Message)
		}
	}

	// ── ses.ContextMessages: compacted context for agent (for LLM) ──
	// Contains the LAST checkpoint (compaction summary) + messages appended
	// after that checkpoint. This is what RestoreSessionIntoAgent() feeds to
	// the agent so the LLM sees the summarized context, not the full log.
	//
	// ⚠️ This is SEPARATE from ses.Messages. Do not conflate the two:
	//   ses.Messages       → TUI rendering (full history)
	//   ses.ContextMessages → agent LLM context (compacted)
	// Build ContextMessages:
	// 1. New checkpoint format: find summary_msg_id in allMessages, load from there
	// 2. Legacy checkpoint: use lastCpMessages + postCPEntries
	// 3. No checkpoint: last MaxContextMessages from allMessages
	if lastCpSummaryMsgID != "" {
		// New format: ContextMessages = [summary message] + [all messages after last_msg_id]
		// The summary message may appear anywhere in JSONL (async pre-compact timing).
		// The extra messages (post-compaction) are identified by lastCpLastMsgID:
		// everything AFTER that ID in the file is an "extra" message.
		summaryIdx := -1
		for i, mr := range allMessages {
			if mr.Message != nil && mr.Message.ID == lastCpSummaryMsgID {
				summaryIdx = i
				break
			}
		}
		if summaryIdx >= 0 {
			// Start with the summary message itself.
			ses.ContextMessages = append(ses.ContextMessages, *allMessages[summaryIdx].Message)
			if lastCpLastMsgID != "" {
				// Find extra messages: everything after lastCpLastMsgID.
				extraStart := -1
				for i, mr := range allMessages {
					if mr.Message != nil && mr.Message.ID == lastCpLastMsgID {
						extraStart = i + 1 // start AFTER last_msg_id
						break
					}
				}
				if extraStart >= 0 {
					for _, mr := range allMessages[extraStart:] {
						if mr.Message != nil && mr.Message.ID != lastCpSummaryMsgID {
							ses.ContextMessages = append(ses.ContextMessages, *mr.Message)
						}
					}
				}
			} else {
				// No last_msg_id (migrated checkpoint): load all messages
				// after the summary as extra messages.
				for _, mr := range allMessages[summaryIdx+1:] {
					if mr.Message != nil {
						ses.ContextMessages = append(ses.ContextMessages, *mr.Message)
					}
				}
			}
			ses.CheckpointTokens = lastCpTokens
			ses.CheckpointMessageCount = len(ses.ContextMessages)
		}
		// If summary_msg_id not found, fall through to no-checkpoint path
	}
	if len(ses.ContextMessages) == 0 && haveCheckpoint && len(lastCpMessages) > 0 {
		// Legacy format: checkpoint messages + post-CP entries
		ses.ContextMessages = make([]provider.Message, len(lastCpMessages))
		copy(ses.ContextMessages, lastCpMessages)
		ses.CheckpointTokens = lastCpTokens
		ses.CheckpointMessageCount = len(lastCpMessages)
		for _, e := range postCPEntries {
			if e.recType == "message" && e.record.Message != nil {
				ses.ContextMessages = append(ses.ContextMessages, *e.record.Message)
			}
			if e.recType == "cost" && e.record.CostJSON != nil {
				ses.CostJSON = []byte(e.record.CostJSON)
			}
		}
	}
	for _, e := range postCPEntries {
		if e.recType == "cost" && e.record.CostJSON != nil {
			ses.CostJSON = []byte(e.record.CostJSON)
		}
	}
	// If no checkpoint, ContextMessages = Messages (all messages go to agent).
	// Cap at MaxContextMessages to prevent loading tens of thousands of messages
	// (which can be 2M+ tokens) into the LLM context on restore. The full message
	// history remains in ses.Messages for TUI rendering; only the agent context
	// is truncated to the most recent messages.
	if len(ses.ContextMessages) == 0 {
		if len(ses.Messages) > MaxContextMessages {
			omitted := len(ses.Messages) - MaxContextMessages
			ses.ContextMessages = ses.Messages[len(ses.Messages)-MaxContextMessages:]
			// Prepend a system note so the agent knows earlier context was truncated,
			// rather than silently losing the conversation beginning.
			ses.ContextMessages = append([]provider.Message{{
				Role: "system",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fmt.Sprintf("[Note: %d earlier messages were truncated to fit the context window. The conversation starts mid-way. Re-read relevant files if you need earlier context.]", omitted),
				}},
			}}, ses.ContextMessages...)
		} else {
			ses.ContextMessages = ses.Messages
		}
	}

	// Backfill missing IDs for ContextMessages and persist to JSONL.
	// This ensures checkpoint restore can find messages by ID even for
	// sessions that were created before the msgID feature.
	if len(ses.ContextMessages) > 0 {
		needsID := false
		for i := range ses.ContextMessages {
			if ses.ContextMessages[i].ID == "" {
				needsID = true
				break
			}
		}
		if needsID {
			// Build a set of IDs to update in JSONL.
			updates := make(map[string]provider.Message) // fingerprint -> msg with ID
			for i := range ses.ContextMessages {
				if ses.ContextMessages[i].ID == "" {
					ses.ContextMessages[i].ID = newSessionMessageID()
					fp := messageFingerprint(&ses.ContextMessages[i])
					updates[fp] = ses.ContextMessages[i]
				}
			}
			// Also update ses.Messages so rendering shows the same IDs.
			for i := range ses.Messages {
				if ses.Messages[i].ID == "" {
					fp := messageFingerprint(&ses.Messages[i])
					if updated, ok := updates[fp]; ok {
						ses.Messages[i].ID = updated.ID
					}
				}
			}
			// Rewrite JSONL with backfilled IDs for matching messages.
			s.backfillIDs(ses.ID, updates)
		}

		// Apply tunnel events with a cap to bound memory and file size.
	}
	if len(postCPTunnelEvs) > MaxTunnelEvents {
		postCPTunnelEvs = postCPTunnelEvs[len(postCPTunnelEvs)-MaxTunnelEvents:]
	}
	for _, rec := range postCPTunnelEvs {
		if rec.TunnelEvent != nil {
			ses.TunnelEvents = append(ses.TunnelEvents, *rec.TunnelEvent)
		}
	}

	// Apply ALL usage records (preserved across checkpoints)
	for _, rec := range allUsage {
		if rec.UsageEntry != nil {
			ses.UsageHistory = append(ses.UsageHistory, *rec.UsageEntry)
		}
	}

	// Apply ALL metric records (preserved across checkpoints)
	for _, rec := range allMetrics {
		if rec.MetricEvent != nil {
			ses.Metrics = append(ses.Metrics, *rec.MetricEvent)
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
// The caller must NOT hold the index flock — this function acquires it.
func (s *JSONLStore) repairIndex(idx []indexEntry) (bool, error) {
	unlock, lockErr := lockIndexFile(s.indexPath())
	if lockErr != nil {
		debug.Log("session", "repairIndex: failed to acquire index lock: %v", lockErr)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
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

	// Check if the index has any entries for this workspace. If not,
	// the index may be stale — rebuild from disk before giving up.
	hasWorkspace := false
	for _, e := range idx {
		if NormalizeWorkspacePath(e.Workspace) == normalizedWorkspace {
			hasWorkspace = true
			break
		}
	}
	if !hasWorkspace {
		changed, repairErr := s.repairIndex(idx)
		if repairErr != nil {
			debug.Log("session", "LatestForWorkspace: repairIndex error: %v", repairErr)
		}
		if changed {
			idx, err = s.loadIndex()
			if err != nil {
				return nil, err
			}
		}
	}

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

	// If the index is empty or doesn't contain the requested workspace,
	// repair it by scanning disk. This handles stale indexes where session
	// files exist on disk but aren't in the index (e.g. after index corruption).
	normalizedWorkspace := NormalizeWorkspacePath(workspace)
	hasWorkspace := false
	for _, e := range idx {
		if NormalizeWorkspacePath(e.Workspace) == normalizedWorkspace {
			hasWorkspace = true
			break
		}
	}
	if !hasWorkspace {
		changed, repairErr := s.repairIndex(idx)
		if repairErr != nil {
			debug.Log("session", "ListForWorkspace: repairIndex error: %v", repairErr)
		}
		if changed {
			idx, err = s.loadIndex()
			if err != nil {
				return nil, err
			}
		}
	}

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

// AppendMessagesBatchToDisk persists multiple messages to the session's JSONL
// file in a single file write, then updates the index once. This is much more
// efficient than calling AppendMessageToDisk in a loop, which does N separate
// file opens and N index read-writes.
//
// Like AppendMessageToDisk, this does NOT modify the Session object.
func (s *JSONLStore) AppendMessagesBatchToDisk(ses *Session, msgs []provider.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	recs := make([]jsonlRecord, len(msgs))
	for i, msg := range msgs {
		recs[i] = jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg}
	}
	if err := appendRecordLines(path, recs); err != nil {
		return err
	}

	return s.updateIndex(ses)
}

// AppendTunnelEventToDisk persists a canonical tunnel event to the session's
// JSONL file. Does NOT call updateIndex — tunnel events don't change session
// metadata (title, model, workspace) that appears in the session index.
// This avoids 222K+ unnecessary index reads+writes per session.
func (s *JSONLStore) AppendTunnelEventToDisk(ses *Session, ev TunnelEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{Type: "tunnel_event", SessionID: ses.ID, TunnelEvent: &ev}
	return appendRecordLine(path, rec)
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
		PermissionMode:       ses.PermissionMode,
		SidebarVisible:       ses.SidebarVisible,
		ContextWindow:        ses.ContextWindow,
		MaxTokens:            ses.MaxTokens,
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
// Does NOT call updateIndex — metrics don't change session index data.
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
		PermissionMode:       ses.PermissionMode,
		SidebarVisible:       ses.SidebarVisible,
		ContextWindow:        ses.ContextWindow,
		MaxTokens:            ses.MaxTokens,
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

// newSessionMessageID generates a unique message identifier: "msg_" + UUID v4.
// This matches the format used by context.Manager.Add() so that IDs are
// consistent across in-memory and persisted messages.
func newSessionMessageID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("msg_fallback_%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("msg_%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// migrateMessageIDs migrates legacy session JSONL to the new checkpoint format.
//
// Old format: checkpoint records embed checkpoint_messages []Message.
// New format: checkpoint records store checkpoint_summary_msg_id +
// checkpoint_last_msg_id, pointing to message records in the JSONL.
//
// Migration strategy (minimal, targeted):
//  1. Find the LAST old-format checkpoint (with checkpoint_messages).
//  2. Extract summary message + last message from it.
//  3. Append summary message to end of file with a generated ID.
//  4. Scan backwards from file end to find a message matching the checkpoint's
//     last message content — that's the last_msg_id. Generate ID for it.
//  5. Rewrite the checkpoint record to new format.
//  6. Backfill IDs only for messages AFTER the last_msg_id position (these
//     are the "extra" messages that restore needs to find by ID).
//
// Messages before the checkpoint are never loaded by restore, so they don't
// need IDs. This keeps migration fast even for very large session files.
//
// Must be called while holding the store mutex (same as loadSession).
func (s *JSONLStore) migrateMessageIDs(id string) (int, error) {
	path := s.sessionPath(id)

	// Phase 1: read all lines, find last old-format checkpoint.
	srcF, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(srcF)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var lines []string        // all lines (trimmed)
	var lastOldCpIdx int = -1 // index in lines
	var lastOldCpSummary *provider.Message
	var lastOldCpLastMsg *provider.Message

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)

		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type == "checkpoint" && len(rec.CheckpointMessages) > 0 {
			// Find summary and last message in this checkpoint.
			var summary *provider.Message
			for i := range rec.CheckpointMessages {
				msg := &rec.CheckpointMessages[i]
				if summary == nil && msg.Role == "system" && len(msg.Content) > 0 {
					for _, blk := range msg.Content {
						if blk.Type == "text" && strings.HasPrefix(blk.Text, "[Previous conversation summary]") {
							summary = msg
							break
						}
					}
				}
			}
			if summary != nil {
				lastOldCpIdx = len(lines) - 1
				lastOldCpSummary = summary
				lastOldCpLastMsg = &rec.CheckpointMessages[len(rec.CheckpointMessages)-1]
			}
		}
	}
	srcF.Close()
	if sc.Err() != nil {
		return 0, fmt.Errorf("migration scan: %w", sc.Err())
	}

	if lastOldCpIdx < 0 {
		// No old-format checkpoint found. ContextMessages for sessions without
		// a checkpoint is built by taking the last MaxContextMessages from
		// ses.Messages — this doesn't require message IDs. So no migration
		// is needed.
		return 0, nil
	}

	// Phase 2: find last_msg_id — scan backwards from file end to find a
	// message matching the checkpoint's last message content.
	lastMsgFingerprint := messageFingerprint(lastOldCpLastMsg)
	lastMsgLineIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		var rec jsonlRecord
		if json.Unmarshal([]byte(lines[i]), &rec) != nil {
			continue
		}
		if rec.Type == "message" && rec.Message != nil {
			if messageFingerprint(rec.Message) == lastMsgFingerprint {
				lastMsgLineIdx = i
				break
			}
		}
	}

	// Phase 3: generate IDs and rewrite file.
	summaryID := newSessionMessageID()
	if lastOldCpSummary.ID == "" {
		lastOldCpSummary.ID = summaryID
	}
	lastMsgID := ""
	if lastMsgLineIdx >= 0 {
		// Generate ID for the matched message if it doesn't have one.
		var rec jsonlRecord
		if json.Unmarshal([]byte(lines[lastMsgLineIdx]), &rec) == nil && rec.Message != nil {
			if rec.Message.ID == "" {
				rec.Message.ID = newSessionMessageID()
			}
			lastMsgID = rec.Message.ID
			// Re-serialize the line with the new ID.
			if data, err := json.Marshal(rec); err == nil {
				lines[lastMsgLineIdx] = string(data)
			}
		}
	}

	// Phase 4: backfill IDs for messages AFTER lastMsgLineIdx (extra messages).
	// If lastMsgLineIdx is -1 (no match found), backfill all messages after
	// the checkpoint record instead.
	migrated := 0
	backfillFrom := lastMsgLineIdx + 1
	if backfillFrom <= 0 {
		backfillFrom = lastOldCpIdx + 1 // after checkpoint record
	}
	for i := backfillFrom; i < len(lines); i++ {
		var rec jsonlRecord
		if json.Unmarshal([]byte(lines[i]), &rec) != nil {
			continue
		}
		if rec.Type == "message" && rec.Message != nil && rec.Message.ID == "" {
			rec.Message.ID = newSessionMessageID()
			migrated++
			if data, err := json.Marshal(rec); err == nil {
				lines[i] = string(data)
			}
		}
	}

	// Phase 5: rewrite the checkpoint record to new format.
	if lastOldCpIdx >= 0 {
		var cpRec jsonlRecord
		if json.Unmarshal([]byte(lines[lastOldCpIdx]), &cpRec) == nil {
			cpRec.CheckpointSummaryMsgID = lastOldCpSummary.ID
			cpRec.CheckpointLastMsgID = lastMsgID
			cpRec.CheckpointMessages = nil
			if data, err := json.Marshal(cpRec); err == nil {
				lines[lastOldCpIdx] = string(data)
			}
		}
	}

	// Phase 6: write file — original lines (modified in-place) with summary
	// message inserted right after the checkpoint record.
	tmp := path + ".migrate.tmp"
	dstF, err := os.Create(tmp)
	if err != nil {
		return 0, fmt.Errorf("creating migration temp file: %w", err)
	}
	for i, line := range lines {
		dstF.WriteString(line + "\n")
		// Insert summary message right after the checkpoint record.
		if i == lastOldCpIdx {
			summaryRec := jsonlRecord{
				Type:    "message",
				Message: lastOldCpSummary,
			}
			if data, err := json.Marshal(summaryRec); err == nil {
				dstF.WriteString(string(data) + "\n")
				migrated++
			}
		}
	}
	if err := dstF.Sync(); err != nil {
		dstF.Close()
		os.Remove(tmp)
		return 0, fmt.Errorf("migration sync: %w", err)
	}
	dstF.Close()

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("migration rename: %w", err)
	}

	debug.Log("session", "migrateMessageIDs: migrated session %s: summary_msg_id=%s last_msg_id=%s backfilled=%d",
		id, lastOldCpSummary.ID, lastMsgID, migrated)
	return migrated, nil
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405"), time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(b))
}

// GenerateID creates a new unique session ID. Exported for callers outside
// the session package (e.g., /branch command creating a new session ID).
func GenerateID() string { return generateID() }

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
func (s *JSONLStore) AppendCheckpoint(ses *Session, summaryMsgID, lastMsgID string, tokenCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{
		Type:                   "checkpoint",
		SessionID:              ses.ID,
		CheckpointSummaryMsgID: summaryMsgID,
		CheckpointLastMsgID:    lastMsgID,
		CheckpointTokens:       tokenCount,
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
func (s *JSONLStore) AppendCheckpointToDisk(ses *Session, summaryMsgID, lastMsgID string, tokenCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{
		Type:                   "checkpoint",
		SessionID:              ses.ID,
		CheckpointSummaryMsgID: summaryMsgID,
		CheckpointLastMsgID:    lastMsgID,
		CheckpointTokens:       tokenCount,
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
	return appendRecordLines(path, []jsonlRecord{rec})
}

// appendRecordLines encodes multiple records and writes them all in a single
// file open+write. This is significantly faster than calling appendRecordLine
// in a loop because it avoids repeated open/close syscalls.
func appendRecordLines(path string, recs []jsonlRecord) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, rec := range recs {
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	// O_APPEND guarantees atomic appends at the OS level on POSIX systems.
	// We intentionally skip f.Sync() (fsync) here for performance:
	//   - Save() (full session rewrite via atomic rename) does fsync for durability.
	//   - This append path trades fsync for speed since it's called frequently.
	//   - The data reaches disk via the OS buffer cache within seconds.
	//   - The only risk is power loss losing the last few buffered appends,
	//     which is acceptable for session event logs.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// messageFingerprint builds a content-based fingerprint for deduplication.
// Two messages with the same role and identical content blocks are considered
// duplicates regardless of their position in the JSONL file.
func messageFingerprint(msg *provider.Message) string {
	var sb strings.Builder
	sb.WriteString(msg.Role)
	sb.WriteByte('|')
	for _, c := range msg.Content {
		switch c.Type {
		case "text":
			sb.WriteString("t:")
			sb.WriteString(c.Text)
		case "tool_use":
			sb.WriteString("u:")
			sb.WriteString(c.ToolName)
			if raw, err := json.Marshal(c.Input); err == nil {
				sb.Write(raw)
			}
		case "tool_result":
			sb.WriteString("r:")
			sb.WriteString(c.ToolID)
			// Cap output in fingerprint to bound memory for large results
			out := c.Output
			if len(out) > 200 {
				out = out[:200]
			}
			sb.WriteString(out)
		default:
			sb.WriteString(c.Type)
			sb.WriteByte('?')
		}
		sb.WriteByte(';')
	}
	return sb.String()
}

// backfillIDs rewrites the JSONL file, adding IDs to message records
// whose fingerprint matches entries in the updates map.
func (s *JSONLStore) backfillIDs(sessionID string, updates map[string]provider.Message) {
	path := s.sessionPath(sessionID)

	f, err := os.Open(path)
	if err != nil {
		debug.Log("session", "backfillIDsAsync: open failed: %v", err)
		return
	}

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	f.Close()
	if err := sc.Err(); err != nil {
		debug.Log("session", "backfillIDsAsync: scan failed: %v", err)
		return
	}

	changed := false
	for i, line := range lines {
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type != "message" || rec.Message == nil || rec.Message.ID != "" {
			continue
		}
		fp := messageFingerprint(rec.Message)
		if updated, ok := updates[fp]; ok {
			rec.Message.ID = updated.ID
			if data, err := json.Marshal(rec); err == nil {
				lines[i] = string(data)
				changed = true
			}
		}
	}

	if !changed {
		return
	}

	tmp := path + ".backfill.tmp"
	dstF, err := os.Create(tmp)
	if err != nil {
		debug.Log("session", "backfillIDsAsync: create temp failed: %v", err)
		return
	}
	for _, line := range lines {
		dstF.WriteString(line + "\n")
	}
	dstF.Close()

	if err := os.Rename(tmp, path); err != nil {
		debug.Log("session", "backfillIDsAsync: rename failed: %v", err)
		os.Remove(tmp)
		return
	}
	debug.Log("session", "backfillIDsAsync: backfilled IDs in session %s", sessionID)
}

// dedupMessageRecords removes duplicate message records, keeping only the
// first occurrence of each unique message. Non-message records are passed
// through unchanged.
func dedupMessageRecords(records []jsonlRecord) []jsonlRecord {
	if len(records) <= 1 {
		return records
	}
	seen := make(map[string]bool, len(records))
	out := records[:0]
	for _, rec := range records {
		if rec.Message == nil {
			out = append(out, rec)
			continue
		}
		fp := messageFingerprint(rec.Message)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		out = append(out, rec)
	}
	return out
}

// dedupLightweightEntries removes duplicate message-type entries from a
// localLightweightEntry slice. Non-message entries (cost, etc.) are kept as-is.
func dedupLightweightEntries(entries []localLightweightEntry) []localLightweightEntry {
	if len(entries) <= 1 {
		return entries
	}
	seen := make(map[string]bool, len(entries))
	out := entries[:0]
	for _, e := range entries {
		if e.recType != "message" || e.record.Message == nil {
			out = append(out, e)
			continue
		}
		fp := messageFingerprint(e.record.Message)
		if seen[fp] {
			continue
		}
		seen[fp] = true
		out = append(out, e)
	}
	return out
}
