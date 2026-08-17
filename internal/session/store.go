package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`
	Workspace string    `json:"workspace,omitempty"`
	Vendor    string    `json:"vendor"`
	Endpoint  string    `json:"endpoint"`
	Model     string    `json:"model"`
	// Preview is a short snippet of the last user message, shown in the
	// resume picker and session list so users can quickly identify sessions.
	// Populated automatically on each user message; persisted to index and meta.
	Preview         string                           `json:"preview,omitempty"`
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
	Source    string              `json:"source,omitempty"` // "agent", "strategist", "verify", "ratchet", "subagent", "compaction"
	Usage     provider.TokenUsage `json:"usage"`
}

// Store is the interface for session persistence.
type Store interface {
	// Save creates the session file if it doesn't exist and updates the
	// session index. It does NOT write messages — use AppendMessageToDisk
	// or AppendMessagesBatchToDisk for that.
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
	Preview   string    `json:"preview,omitempty"`
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
	// lastIndexUpdate tracks per-session index flush times for debouncing.
	// updateIndex does a full read + JSON parse + JSON marshal + fsync of the
	// entire index file. Without debouncing, every AppendMessageToDisk call
	// (one per message = 10-20 per agent iteration) triggers an fsync, adding
	// 10-200ms of blocked I/O per iteration. With debouncing, the index is
	// updated at most once per 5 seconds per session during active messaging.
	lastIndexUpdate map[string]time.Time
	// fullLoad disables time-windowed message loading. When false (default),
	// only messages within RecentMessageWindow are loaded into ses.Messages
	// for rendering. ContextMessages (agent LLM context) is always fully loaded.
	fullLoad bool
}

// indexUpdateDebounce is the minimum interval between index updates triggered
// by AppendMessageToDisk for the same session. Other callers (AppendMetaToDisk,
// AppendCheckpointToDisk, AppendMessagesBatchToDisk) always update the index.
const indexUpdateDebounce = 5 * time.Second

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

// RecentMessageWindow is the time window of messages loaded for rendering
// when a session is very large. Only the most recent messages within this
// duration from the last message are loaded into ses.Messages; older messages
// remain on disk but are not deserialized. Override with --full.
const RecentMessageWindow = 24 * time.Hour

// recentMessageThreshold is the minimum number of message records that
// triggers time-windowed loading. Below this, all messages load normally.
const recentMessageThreshold = 500

// quickExtractTimestamp does a fast substring search for the JSON
// "timestamp":"..." field without fully deserializing the record.
// Returns zero time if not found.
func quickExtractTimestamp(line []byte) time.Time {
	// #558 C: only read the TOP-LEVEL "timestamp" key. The Message (and any
	// tool_use Input inside it) is serialized BEFORE the record-level Timestamp,
	// so a naive bytes.Index picks up a pseudo-timestamp embedded in message
	// content (e.g. {"timestamp":"2099-..."} in a tool_use input) and breaks
	// findMessageCutoff's monotonicity assumption. topLevelStringField walks
	// JSON structure (string tokens + depth), so keys nested inside message
	// content are never confused with the record's own fields.
	start, end := topLevelStringField(line, "timestamp")
	if start < 0 {
		return time.Time{}
	}
	if end-start < 20 { // RFC3339 needs at least 20 chars: 2006-01-02T15:04:05Z
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339, string(line[start:end]))
	if err != nil {
		return time.Time{}
	}
	return ts
}

// topLevelStringField locates the string value of a top-level (depth-1) key
// in a flat JSON object line, returning the value's content byte range
// [start, end) excluding quotes. It scans complete string tokens (escape
// aware) so occurrences of the key name inside nested objects, arrays, or
// string values never match. Returns (-1, -1) if the field is absent at
// depth 1. This stays a single-pass byte scan — no unmarshaling.
func topLevelStringField(line []byte, name string) (int, int) {
	n := len(line)
	depth := 0
	i := 0
	for i < n {
		c := line[i]
		switch c {
		case '"':
			// Scan the full string token [i, j] (closing quote at j).
			j := i + 1
			for j < n {
				if line[j] == '\\' {
					j += 2
					continue
				}
				if line[j] == '"' {
					break
				}
				j++
			}
			if j >= n {
				return -1, -1 // unterminated string
			}
			tokenEnd := j + 1
			// Key candidate: token at depth 1 whose content is the field name.
			if depth == 1 && string(line[i+1:j]) == name {
				k := tokenEnd
				for k < n && (line[k] == ' ' || line[k] == '\t') {
					k++
				}
				if k < n && line[k] == ':' {
					k++
					for k < n && (line[k] == ' ' || line[k] == '\t') {
						k++
					}
					if k < n && line[k] == '"' {
						v := k + 1
						m := v
						for m < n {
							if line[m] == '\\' {
								m += 2
								continue
							}
							if line[m] == '"' {
								break
							}
							m++
						}
						if m >= n {
							return -1, -1
						}
						return v, m
					}
				}
			}
			i = tokenEnd
		case '{', '[':
			depth++
			i++
		case '}', ']':
			depth--
			i++
		default:
			i++
		}
	}
	return -1, -1
}

// quickRecordType extracts the "type" field value via fast substring search.
func quickRecordType(line []byte) string {
	idx := bytes.Index(line, []byte(`"type":"`))
	if idx < 0 {
		return ""
	}
	start := idx + len(`"type":"`)
	end := bytes.IndexByte(line[start:], '"')
	if end < 0 {
		return ""
	}
	return string(line[start : start+end])
}

// quickIsDialogueRole reports whether a raw message record line carries a
// user or assistant role. Used by findMessageCutoff to anchor the recent
// window on real conversation turns rather than system-note tails.
func quickIsDialogueRole(line []byte) bool {
	i := bytes.Index(line, []byte(`"role":"`))
	if i < 0 {
		return false
	}
	j := i + len(`"role":"`)
	k := bytes.IndexByte(line[j:], '"')
	if k < 0 {
		return false
	}
	role := line[j : j+k]
	return bytes.Equal(role, []byte("user")) || bytes.Equal(role, []byte("assistant"))
}

// findMessageCutoff does a fast first-pass scan of the JSONL file to
// determine the byte offset after which all "message" records fall within
// the recent time window. Returns (offset, totalMessageCount, lastTimestamp).
// If the file has fewer than recentMessageThreshold messages or the last
// message has no timestamp, returns (0, count, zeroTime) meaning "load all".
//
// The window is anchored on the last USER or ASSISTANT message, not the last
// message of any role. Long-running sessions routinely end with a tail of
// system messages (checkpoint notes, resume markers, compaction summaries)
// appended hours or days after the last real exchange. Anchoring on that tail
// put the entire 24h window over system-only records, so the TUI rendered
// zero visible history even though the file had thousands of messages.
// Anchoring on the last user/assistant message guarantees at least that
// message (and its surrounding conversation) falls inside the window.
func findMessageCutoff(path string) (int64, int, time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, time.Time{}
	}
	defer f.Close()

	// First: count messages and find last timestamp via fast scan.
	// We track byte offsets of each message line in a slice.
	type msgOff struct {
		offset     int64
		ts         time.Time
		isDialogue bool // role is user or assistant
	}
	var offsets []msgOff
	var pos int64

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		lineLen := int64(len(sc.Bytes())) + 1 // +1 for newline

		// Fast check: is this a message record?
		if rt := quickRecordType(sc.Bytes()); rt == "message" {
			ts := quickExtractTimestamp(sc.Bytes())
			offsets = append(offsets, msgOff{offset: pos, ts: ts, isDialogue: quickIsDialogueRole(sc.Bytes())})
		}
		pos += lineLen
	}

	totalMsgs := len(offsets)
	if totalMsgs < recentMessageThreshold {
		return 0, totalMsgs, time.Time{}
	}

	// Find the anchor: the last message whose role is user or assistant.
	// If none exists (system-only file), fall back to the last message.
	last := time.Time{}
	for i := totalMsgs - 1; i >= 0; i-- {
		if offsets[i].isDialogue {
			last = offsets[i].ts
			break
		}
	}
	if last.IsZero() {
		last = offsets[totalMsgs-1].ts
	}
	if last.IsZero() {
		// No timestamps — can't filter, load all.
		return 0, totalMsgs, time.Time{}
	}

	cutoff := last.Add(-RecentMessageWindow)

	// Binary search for the first message at or after cutoff.
	// offsets is sorted by time (file order = chronological).
	lo, hi := 0, totalMsgs
	for lo < hi {
		mid := (lo + hi) / 2
		if offsets[mid].ts.Before(cutoff) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	if lo == 0 {
		return 0, totalMsgs, last // all messages are within window
	}

	return offsets[lo].offset, totalMsgs, last
}

// messageHasToolResult reports whether msg contains a tool_result block.
func messageHasToolResult(msg provider.Message) bool {
	for _, b := range msg.Content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// messageHasToolUse reports whether msg contains a tool_use block.
func messageHasToolUse(msg provider.Message) bool {
	for _, b := range msg.Content {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

// isOrphanToolMessage reports whether msg would be an invalid first message
// for the LLM context because it is part of a tool_use/tool_result pair whose
// other half was truncated away. Extending the context window backward over
// such messages keeps the paired tool_use and tool_result together.
func isOrphanToolMessage(msg provider.Message) bool {
	if msg.Role == "user" && messageHasToolResult(msg) {
		return true
	}
	if msg.Role == "assistant" && messageHasToolUse(msg) {
		return true
	}
	return false
}

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

// LoadWithOptions loads a session, optionally forcing all messages to be
// loaded regardless of the time window. Use fullLoad=true for --full.
func (s *JSONLStore) LoadWithOptions(id string, fullLoad bool) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.fullLoad
	s.fullLoad = fullLoad
	defer func() { s.fullLoad = prev }()
	return s.loadSession(id)
}

// SetFullLoad sets the default loading mode for subsequent Load() calls.
func (s *JSONLStore) SetFullLoad(full bool) {
	s.mu.Lock()
	s.fullLoad = full
	s.mu.Unlock()
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
	// Retry flock acquisition with exponential backoff to handle transient
	// lock contention from other processes (desktop + TUI).
	var unlock func()
	var lockErr error
	for i := 0; i < 3; i++ {
		unlock, lockErr = lockIndexFile(s.indexPath())
		if lockErr == nil {
			break
		}
		if i < 2 {
			// Exponential backoff: 10ms, 20ms, 40ms
			time.Sleep(time.Duration(10*(1<<i)) * time.Millisecond)
		}
	}
	if lockErr != nil {
		debug.Log("session", "updateIndex: failed to acquire index lock after 3 retries: %v", lockErr)
		s.indexDirty = true
		return lockErr
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
	if idx == nil && s.indexDirty {
		// Index is corrupt (not just empty — loadIndexNoRepair set the
		// dirty flag). Don't write a single-entry index that overwrites
		// real entries. Keep dirty flag for runMaintenance to rebuild.
		return nil
	}
	found := false
	for i, e := range idx {
		if e.ID == ses.ID {
			newEntry := sessionToIndexEntry(ses)
			// #558 F: under time-windowed loading ses.Messages holds only the
			// recent window, so len(s.Messages) is a lower bound, not the real
			// on-disk count. Message records are never removed from a session
			// file, so taking the max with the previous index entry prevents the
			// count from regressing on every windowed load.
			if newEntry.MsgCount < e.MsgCount {
				newEntry.MsgCount = e.MsgCount
			}
			idx[i] = newEntry
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
	// Retry flock acquisition with exponential backoff to handle transient
	// lock contention from other processes (desktop + TUI). Matches updateIndex.
	var unlock func()
	var lockErr error
	for i := 0; i < 3; i++ {
		unlock, lockErr = lockIndexFile(s.indexPath())
		if lockErr == nil {
			break
		}
		if i < 2 {
			// Exponential backoff: 10ms, 20ms, 40ms
			time.Sleep(time.Duration(10*(1<<i)) * time.Millisecond)
		}
	}
	if lockErr != nil {
		debug.Log("session", "removeFromIndex: failed to acquire index lock after 3 retries: %v", lockErr)
		s.indexDirty = true
		return lockErr
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
	if idx == nil && s.indexDirty {
		// Index is corrupt (not just empty — loadIndexNoRepair set the
		// dirty flag). Don't write an empty index that would hide
		// real entries from List(). Keep dirty flag for runMaintenance to rebuild.
		return nil
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
		Preview:   s.Preview,
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
	Preview              string              `json:"preview,omitempty"`
	Workspace            string              `json:"workspace,omitempty"`
	Vendor               string              `json:"vendor,omitempty"`
	Endpoint             string              `json:"endpoint,omitempty"`
	Model                string              `json:"model,omitempty"`
	TokenUsage           provider.TokenUsage `json:"token_usage,omitempty"`
	CreatedAt            time.Time           `json:"created_at,omitempty"`
	UpdatedAt            time.Time           `json:"updated_at,omitempty"`
	TunnelEventsComplete bool                `json:"tunnel_events_complete,omitempty"`
	Message              *provider.Message   `json:"message,omitempty"`
	Timestamp            time.Time           `json:"timestamp,omitempty"` // per-message timestamp (for message records)
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

// validSessionID reports whether id is a plain generated identifier.
// Only [A-Za-z0-9_-] are allowed: an unvalidated id used to flow straight
// into filepath.Join, so --resume ../../foo escaped the store directory
// for both reads AND (via empty-session cleanup) deletions (#401).
func validSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return !strings.HasPrefix(id, "-") // no leading dash: not a flag-like path piece
}

// sessionPath returns the JSONL path for a session ID. Invalid IDs fall back
// to a safe, non-escaping placeholder so callers that ignore the error still
// cannot touch files outside s.dir.
func (s *JSONLStore) sessionPath(id string) string {
	if !validSessionID(id) {
		return filepath.Join(s.dir, "_invalid_id_.jsonl")
	}
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
// Save creates the JSONL file if it doesn't exist. It does NOT write messages
// or update the session index — the index is populated on the first
// AppendMessageToDisk or AppendMetaToDisk call, when the title and message
// count are meaningful.
//
// This method is intentionally a safe no-op for existing sessions to prevent
// data loss from the full-rewrite pattern. Messages are persisted incrementally
// via AppendMessageToDisk (called by PersistHandler on every contextManager.Add).
//
// To persist messages explicitly (e.g. for /branch), use AppendMessagesBatchToDisk.
// To update metadata, use AppendMetaToDisk.
func (s *JSONLStore) Save(ses *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ses.UpdatedAt = time.Now()

	path := s.sessionPath(ses.ID)

	// Create the file if it doesn't exist (touch). The session index is NOT
	// updated here — a new session with title "New session" and 0 messages
	// would pollute the index. The index is populated on the first
	// AppendMessageToDisk call, which also carries the auto-generated title.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("creating session file: %w", err)
	}
	f.Close()

	debug.Log("session", "Save: ensured file exists for session %s (no index update)", ses.ID)
	return nil
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

	// Determine if we should apply time-windowed loading for rendering.
	// When fullLoad is false and the session has many messages, only load
	// the most recent messages (within RecentMessageWindow) for ses.Messages.
	// ContextMessages (agent LLM context) is unaffected — it has its own
	// checkpoint-based compaction.
	var msgCutoff int64 // byte offset: skip message records before this offset
	var totalMsgCount int
	if !s.fullLoad {
		msgCutoff, totalMsgCount, _ = findMessageCutoff(path)
		if msgCutoff > 0 {
			debug.Log("session", "loadSession %s: time-windowed load, cutoff at offset %d (%d total messages)", id, msgCutoff, totalMsgCount)
		}
	}

	// Migrate legacy JSONL records: backfill missing message IDs and convert
	// old checkpoint format to new summary_msg_id format. This is a no-op
	// if the file is already fully migrated.
	if migrated, err := s.migrateMessageIDs(id); err != nil {
		debug.Log("session", "loadSession: migration failed for %s: %v (continuing with original file)", id, err)
	} else if migrated > 0 {
		debug.Log("session", "loadSession: migrated %d message IDs in session %s", migrated, id)
		// #558 A: migration rewrote the entire file (legacy checkpoint snapshot
		// removed, summary message inserted) — every byte offset computed from
		// the pre-migration file is stale. Recompute the cutoff against the
		// rewritten file, otherwise outdated messages leak past the window on
		// the first resume after migration.
		if !s.fullLoad {
			msgCutoff, totalMsgCount, _ = findMessageCutoff(path)
		}
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
		allUsage           []jsonlRecord      // ALL usage records (never cleared by checkpoint)
		allMetrics         []jsonlRecord      // ALL metric records (never cleared by checkpoint)
		haveCheckpoint     bool
	)

	var byteOffset int64
	for sc.Scan() {
		lineLen := int64(len(sc.Bytes())) + 1
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			byteOffset += lineLen
			continue
		}
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			byteOffset += lineLen
			continue // skip malformed lines
		}

		lineStart := byteOffset
		byteOffset += lineLen

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
				haveCheckpoint = true
			}
		case "usage":
			// Usage records are cumulative token history — never discard.
			allUsage = append(allUsage, rec)
		case "metric":
			// Metric records are cumulative performance history — never discard.
			allMetrics = append(allMetrics, rec)
		case "message":
			// Apply time-windowed loading: skip old messages for rendering.
			// Still track them for checkpoint context restoration.
			if msgCutoff > 0 && lineStart < msgCutoff {
				// Old message within cutoff — only keep for context restoration,
				// not for ses.Messages (rendering). postCPEntries needs it for
				// checkpoint logic, but allMessages (rendering) skips it.
				postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
				continue
			}
			// Recent message (or fullLoad): keep for full rendering.
			allMessages = append(allMessages, rec)
			// Also track for ContextMessages (checkpoint + post-checkpoint).
			postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
		case "cost":
			postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
		case "tunnel_event":
			// Tunnel events are no longer stored in session JSONL.
			// Old files may still contain these lines — skip them silently.
			// The projection store is the sole source of tunnel event history.
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading session %s: %w", id, err)
	}
	f.Close() // close read handle before potential backfill rewrite

	// Backfill timestamps for historical sessions (created before timestamp feature).
	// If the first message has no timestamp, all messages get set to 6 hours ago.
	// If the first message already has a timestamp, the session is skipped.
	if len(allMessages) > 0 && allMessages[0].Timestamp.IsZero() {
		s.backfillTimestamps(id)
	}

	// Apply metadata from meta records (always the latest meta wins)
	for _, rec := range metaRecords {
		ses.Title = rec.Title
		ses.Preview = rec.Preview
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

	// ── ses.Messages: message records from the file (for rendering) ──
	// Under time-windowed loading (fullLoad=false, >recentMessageThreshold
	// messages), only messages within RecentMessageWindow of the last message
	// are kept here; older records are skipped above. With fullLoad=true this
	// is the full conversation history. The TUI renders from this slice on
	// reload.
	//
	// ⚠️ Never filter or truncate this by checkpoint — that would silently
	// destroy conversation history the user expects to see. Note that the
	// time-window filter above means ses.Messages is NOT reliable for
	// deletion decisions; use HasUserInteractionOnDisk for those.
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
		//
		// Search postCPEntries (which includes all messages regardless of the
		// 24h time-window cutoff) so that checkpoint summaries written >24h ago
		// are still found and used for context restoration.
		var summaryMsg *provider.Message
		var summaryMsgIdx int = -1
		for i, entry := range postCPEntries {
			if entry.recType == "message" && entry.record.Message != nil && entry.record.Message.ID == lastCpSummaryMsgID {
				summaryMsg = entry.record.Message
				summaryMsgIdx = i
				break
			}
		}
		if summaryMsg != nil && summaryMsgIdx >= 0 {
			// Start with the summary message itself.
			ses.ContextMessages = append(ses.ContextMessages, *summaryMsg)
			if lastCpLastMsgID != "" {
				// Find extra messages: everything after lastCpLastMsgID.
				// #452: search the UNWINDOWED postCPEntries (the same list
				// the summary lookup above uses), not the 24h-window-filtered
				// allMessages. On long sessions (>500 msgs, >24h span) the
				// window filtered out last_msg_id itself, silently dropping
				// the messages between it and the summary via the fallback.
				extraStart := -1
				for i := summaryMsgIdx + 1; i < len(postCPEntries); i++ {
					entry := postCPEntries[i]
					if entry.recType == "message" && entry.record.Message != nil && entry.record.Message.ID == lastCpLastMsgID {
						extraStart = i + 1 // start AFTER last_msg_id
						break
					}
				}
				if extraStart >= 0 {
					// extraStart indexes postCPEntries — the same unwindowed
					// list the search above used. It must NOT be used to slice
					// allMessages: the two index spaces diverge as soon as
					// postCPEntries contains cost records or windowed-out old
					// messages (>500 msgs / >24h sessions), which made
					// allMessages[extraStart:] panic (bounds out of range) or
					// silently restore the wrong context slice.
					for _, entry := range postCPEntries[extraStart:] {
						if entry.recType == "message" && entry.record.Message != nil && entry.record.Message.ID != lastCpSummaryMsgID {
							ses.ContextMessages = append(ses.ContextMessages, *entry.record.Message)
						}
					}
				} else {
					// Fallback: checkpoint last_msg_id not found in allMessages.
					// This happens when dedup removed a duplicate message, or
					// the message ID wasn't persisted (older ggcode versions).
					// Without this fallback, ALL post-checkpoint messages are
					// lost and the agent sees only the summary on reload.
					// Load all messages after the summary as extra messages,
					// same as the no-last_msg_id path below.
					afterSummary := len(postCPEntries) - summaryMsgIdx - 1
					debug.Log("session", "loadSession %s: checkpoint last_msg_id %q not found in postCPEntries, using post-summary fallback (%d entries after summary)", id, lastCpLastMsgID, afterSummary)
					for _, entry := range postCPEntries[summaryMsgIdx+1:] {
						if entry.recType == "message" && entry.record.Message != nil {
							ses.ContextMessages = append(ses.ContextMessages, *entry.record.Message)
						}
					}
				}
			} else {
				// No last_msg_id (migrated checkpoint): load all messages
				// after the summary as extra messages.
				for _, entry := range postCPEntries[summaryMsgIdx+1:] {
					if entry.recType == "message" && entry.record.Message != nil {
						ses.ContextMessages = append(ses.ContextMessages, *entry.record.Message)
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
			start := len(ses.Messages) - MaxContextMessages
			// Avoid starting the context with an orphan tool_result or tool_use.
			// If the first message at the truncation boundary is a user
			// tool_result, extend backward to include its paired assistant
			// tool_use (and the user prompt that triggered it, if necessary).
			// LLM APIs require tool_use and tool_result to appear as a pair;
			// leaving half of the pair at the start of context causes validation
			// errors on the next agent turn.
			for start > 0 && isOrphanToolMessage(ses.Messages[start]) {
				start--
				omitted--
			}
			ses.ContextMessages = ses.Messages[start:]
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

	// Diagnostic summary: log the final ContextMessages state so that
	// context-loss issues can be diagnosed via debug_log without needing
	// to reproduce the problem. This single line captures:
	//   - session ID and message counts (context vs total)
	//   - whether a checkpoint was used and its key IDs
	//   - token baseline from the checkpoint
	cpInfo := "no-checkpoint"
	if lastCpSummaryMsgID != "" {
		cpInfo = fmt.Sprintf("checkpoint summary=%s last_msg=%s tokens=%d", lastCpSummaryMsgID, lastCpLastMsgID, lastCpTokens)
	}
	debug.Log("session", "loadSession %s: ContextMessages=%d (total=%d) [%s]",
		id, len(ses.ContextMessages), len(ses.Messages), cpInfo)

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

		// Tunnel events are no longer loaded from session JSONL.
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
			Preview:   e.Preview,
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
	// Acquire cross-process lock before saving — pruneInvalidIndexEntries
	// may have taken seconds to scan all sessions, and another process could
	// have appended to the index in the meantime. Without this lock, our
	// saveIndex would overwrite their entry, losing session data.
	unlock, lockErr := lockIndexFile(s.indexPath())
	if lockErr != nil {
		debug.Log("session", "runMaintenance: failed to acquire index lock: %v", lockErr)
		s.indexDirty = true
		return
	}
	defer unlock()
	// Reload index under lock to pick up any changes from other processes.
	currentIdx, err := s.loadIndexNoRepair()
	if err != nil {
		debug.Log("session", "runMaintenance: failed to reload index under lock: %v", err)
		s.indexDirty = true
		return
	}
	// Merge: keep entries that survived pruning (validIdx) plus entries
	// added by other processes after our initial load (in currentIdx but
	// not in our original idx). Drop entries we pruned (in original idx
	// but not in validIdx).
	originalSet := make(map[string]bool, len(idx))
	for _, e := range idx {
		originalSet[e.ID] = true
	}
	validSet := make(map[string]bool, len(validIdx))
	for _, e := range validIdx {
		validSet[e.ID] = true
	}
	currentSet := make(map[string]bool, len(currentIdx))
	for _, e := range currentIdx {
		currentSet[e.ID] = true
	}
	merged := make([]indexEntry, 0, len(currentIdx))
	// From currentIdx: keep if it survived pruning OR was added by another process.
	for _, e := range currentIdx {
		if validSet[e.ID] || !originalSet[e.ID] {
			merged = append(merged, e)
		}
	}
	// From validIdx: add any not already in currentIdx (deleted by other process).
	for _, e := range validIdx {
		if !currentSet[e.ID] {
			merged = append(merged, e)
		}
	}
	if err := s.saveIndex(merged); err != nil {
		s.indexDirty = true
		return
	}
	s.indexDirty = false
}

func (s *JSONLStore) pruneInvalidIndexEntries(idx []indexEntry) ([]indexEntry, bool) {
	cleaned := false
	validIdx := make([]indexEntry, 0, len(idx))
	for _, e := range idx {
		ses, loadErr := s.loadSessionFull(e.ID)
		if loadErr != nil {
			// Transient I/O errors (network filesystem, permission, lock) must
			// NOT cause permanent file deletion. Skip the index entry but keep
			// the file on disk so it can be loaded on a subsequent attempt.
			debug.Log("session", "pruneInvalidIndexEntries: skipping %s due to load error: %v", e.ID, loadErr)
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

// loadSessionFull loads a session with fullLoad=true, bypassing time-windowed
// message loading. This is used for validation before deletion (e.g., in
// pruneInvalidIndexEntries) to ensure we don't delete sessions that have user
// interaction merely because their tail messages don't include a user message.
// Uses a lightweight clone to avoid racing on s.fullLoad.
func (s *JSONLStore) loadSessionFull(id string) (*Session, error) {
	clone := &JSONLStore{dir: s.dir, fullLoad: true}
	return clone.loadSession(id)
}

// RepairIndex scans the sessions directory and reconciles with the index.
// It is safe to call from a goroutine (fire-and-forget). The caller must
// NOT hold the index flock — this function acquires it.
func (s *JSONLStore) RepairIndex() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Load current index so repairIndex only loads files NOT already in it.
	// Passing nil would treat ALL disk files as orphans and reload every one.
	idx, err := s.loadIndexFromDisk()
	if err != nil {
		return false, err
	}
	return s.repairIndex(idx)
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
				// Same full-file check as CleanupIfEmpty: time-windowed loading must
				// not cause deletion of sessions whose user messages are all older
				// than RecentMessageWindow (#254). Unknown on-disk state keeps the
				// session (#291).
				interacted, hasErr := s.HasUserInteractionOnDisk(id)
				if hasErr != nil {
					debug.Log("session", "repairIndex: keeping orphan session %s — on-disk check unknown: %v", id, hasErr)
					newIdx = append(newIdx, sessionToIndexEntry(ses))
				} else if !interacted {
					_ = os.Remove(s.sessionPath(id))
				} else {
					newIdx = append(newIdx, sessionToIndexEntry(ses))
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

// HasUserInteractionOnDisk streams the session's JSONL file and reports
// whether it contains at least one user message with actual text content.
// Unlike Session.HasUserInteraction, this is NOT affected by time-windowed
// loading — it always reflects the full file on disk.
//
// This is a three-state check (fix #291): (false, nil) means the file was
// fully scanned and definitely has no user interaction; (false, err) means
// the answer is UNKNOWN (file unreadable, or a line exceeded the scanner
// buffer) — callers MUST treat that as "keep" and avoid destructive actions.
func (s *JSONLStore) HasUserInteractionOnDisk(id string) (bool, error) {
	path := s.sessionPath(id)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File already gone — nothing to protect.
			return false, nil
		}
		return false, fmt.Errorf("opening session file %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "message" || rec.Message == nil || rec.Message.Role != "user" {
			continue
		}
		for _, b := range rec.Message.Content {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return true, nil
			}
		}
	}
	if scanErr := sc.Err(); scanErr != nil {
		// A line exceeded the 10MB scanner buffer (e.g. the user pasted a huge
		// blob into a single JSONL message). Scan() stops early even though
		// user messages may already have been seen — be conservative and keep
		// the session (fix #291: never delete on uncertain input).
		if errors.Is(scanErr, bufio.ErrTooLong) {
			debug.Log("session", "HasUserInteractionOnDisk %s: line exceeds scanner buffer, conservatively keeping", id)
			return true, nil
		}
		return false, fmt.Errorf("scanning session file %s: %w", path, scanErr)
	}
	return false, nil
}

// CleanupIfEmpty deletes the session file if it has no user interaction.
// Called on exit to avoid leaving empty session files on disk.
//
// The check uses HasUserInteractionOnDisk (full-file scan), NOT the
// in-memory ses.Messages: time-windowed loading drops messages older than
// RecentMessageWindow from ses.Messages, which previously caused this
// method to silently delete sessions whose user messages were all >24h old
// (#254). The full scan is acceptable here because this is a low-frequency
// exit-path call.
func (s *JSONLStore) CleanupIfEmpty(ses *Session) error {
	if !s.WillCleanupIfEmpty(ses) {
		if !ses.HasUserInteraction() {
			debug.Log("session", "CleanupIfEmpty: keeping session %s — user messages found on disk outside load window", ses.ID)
		}
		return nil
	}
	return s.Delete(ses.ID)
}

// WillCleanupIfEmpty reports whether CleanupIfEmpty would delete this session,
// using the same decision logic (in-memory check first, then full-file scan).
// Callers that react to the deletion (e.g. clearing IM bindings) should use
// this instead of !ses.HasUserInteraction(), which can disagree with
// CleanupIfEmpty under time-windowed loading (#254).
func (s *JSONLStore) WillCleanupIfEmpty(ses *Session) bool {
	if ses.HasUserInteraction() {
		return false
	}
	// Windowed load may have dropped user messages — verify against the file.
	// Unknown (read error / oversized line) keeps the session (#291).
	interacted, err := s.HasUserInteractionOnDisk(ses.ID)
	if err != nil {
		debug.Log("session", "WillCleanupIfEmpty: keeping session %s — on-disk check unknown: %v", ses.ID, err)
		return false
	}
	return !interacted
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
	return ExportSessionMarkdownWithDisplay(ses, "", "")
}

// ExportSessionMarkdownWithDisplay renders a Session to markdown with
// optional display names for vendor and endpoint (instead of raw config keys).
func ExportSessionMarkdownWithDisplay(ses *Session, vendorDisplay, endpointDisplay string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", ses.Title))
	sb.WriteString(fmt.Sprintf("**Session:** %s\n", ses.ID))
	sb.WriteString(fmt.Sprintf("**Created:** %s\n", ses.CreatedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**Updated:** %s\n", ses.UpdatedAt.Format(time.RFC3339)))
	if ses.Vendor != "" {
		vendor := ses.Vendor
		if vendorDisplay != "" {
			vendor = vendorDisplay
		}
		sb.WriteString(fmt.Sprintf("**Vendor:** %s", vendor))
		if ses.Endpoint != "" {
			endpoint := ses.Endpoint
			if endpointDisplay != "" {
				endpoint = endpointDisplay
			}
			sb.WriteString(fmt.Sprintf(" / %s", endpoint))
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

	rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg, Timestamp: time.Now()}
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

	rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg, Timestamp: time.Now()}
	if err := appendRecordLine(path, rec); err != nil {
		return err
	}

	// Debounce index updates: only rewrite the index at most once per
	// indexUpdateDebounce per session. The index is a display cache — it's
	// also updated by AppendMetaToDisk (model changes, session switches),
	// AppendCheckpointToDisk (compaction), and AppendMessagesBatchToDisk
	// (session save). Those callers bypass the debounce.
	if s.lastIndexUpdate == nil {
		s.lastIndexUpdate = make(map[string]time.Time)
	}
	if last, ok := s.lastIndexUpdate[ses.ID]; ok && time.Since(last) < indexUpdateDebounce {
		return nil
	}
	s.lastIndexUpdate[ses.ID] = time.Now()
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
		recs[i] = jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg, Timestamp: time.Now()}
	}
	if err := appendRecordLines(path, recs); err != nil {
		return err
	}

	// Batch appends always update the index (they're infrequent — session
	// save, restore — and the caller expects the index to be current).
	if s.lastIndexUpdate == nil {
		s.lastIndexUpdate = make(map[string]time.Time)
	}
	s.lastIndexUpdate[ses.ID] = time.Now()
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
	// Meta writes always update the index — reset the debounce timer.
	if s.lastIndexUpdate == nil {
		s.lastIndexUpdate = make(map[string]time.Time)
	}
	s.lastIndexUpdate[ses.ID] = time.Now()
	path := s.sessionPath(ses.ID)
	rec := jsonlRecord{
		Type:                 "meta",
		SessionID:            ses.ID,
		Title:                ses.Title,
		Preview:              ses.Preview,
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

	// Don't create a meta file for sessions with no user interaction.
	if !ses.HasUserInteraction() {
		return nil
	}

	// O_EXCL guards against a cross-process TOCTOU (#531): the sessions
	// directory is shared by multiple ggcode processes (CLI + desktop), and
	// s.mu only serializes within this process. With Stat-then-Create(O_TRUNC)
	// another process could create the file and append messages between our
	// Stat and Create, and the truncate would wipe its session down to a
	// single meta line. O_EXCL loses that race cleanly instead (backfillIDs
	// uses the same pattern).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil // already exists (possibly created concurrently)
		}
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
// hasNewFormatCheckpoint streams through the JSONL file and returns true as
// soon as it finds any checkpoint record with checkpoint_summary_msg_id.
// Since migration is a one-time operation (once migrated, all subsequent
// checkpoints use the new format), finding one new-format checkpoint proves
// no migration is needed.
//
// Unlike the full migrateMessageIDs scan, this function:
//   - Does NOT load all lines into memory (uses bufio.Scanner)
//   - Only JSON-parses lines containing "checkpoint" (fast string filter)
//   - Returns immediately on first match (early exit)
//
// For a 50MB session with a checkpoint in the first 10MB, this exits after
// reading ~10MB instead of 50MB, and does ~5 JSON parses instead of ~10,000.
func hasNewFormatCheckpoint(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Fast filter: skip lines that can't be checkpoint records.
		// Most lines are messages/usage/metrics — this avoids JSON parse
		// for >99% of lines.
		if !strings.Contains(line, `"checkpoint"`) {
			continue
		}
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type == "checkpoint" && rec.CheckpointSummaryMsgID != "" {
			return true // Found new-format checkpoint — no migration needed
		}
	}
	return false // No new-format checkpoint found
}

func (s *JSONLStore) migrateMessageIDs(id string) (int, error) {
	path := s.sessionPath(id)

	// #558 D: hold the cross-process session lock across the entire
	// read -> tmp -> rename cycle so a concurrent O_APPEND writer in another
	// process cannot slip an append into the gap that the rename would drop.
	unlock, lockErr := lockSessionFile(path)
	if lockErr != nil {
		debug.Log("session", "migrateMessageIDs: session lock unavailable: %v", lockErr)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	// Fast path: check if the file has already been migrated by scanning
	// only the tail of the file for the last checkpoint record. If it's
	// new format (has summary_msg_id), skip the expensive full-file scan.
	if hasNewFormatCheckpoint(path) {
		return 0, nil
	}

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
	var lastCpIsNewFormat bool // true if the last checkpoint has summary_msg_id

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
		if rec.Type == "checkpoint" {
			if rec.CheckpointSummaryMsgID != "" {
				// New format checkpoint — restore will use this one.
				lastCpIsNewFormat = true
			} else if len(rec.CheckpointMessages) > 0 {
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
	}
	srcF.Close()
	if sc.Err() != nil {
		return 0, fmt.Errorf("migration scan: %w", sc.Err())
	}

	if lastCpIsNewFormat {
		// The last checkpoint is already in new format (summary_msg_id).
		// Restore will use it, not any older checkpoint. No migration needed.
		return 0, nil
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
		if _, err := dstF.WriteString(line + "\n"); err != nil {
			dstF.Close()
			os.Remove(tmp)
			return 0, fmt.Errorf("migration write: %w", err)
		}
		// Insert summary message right after the checkpoint record.
		if i == lastOldCpIdx {
			summaryRec := jsonlRecord{
				Type:    "message",
				Message: lastOldCpSummary,
			}
			if data, err := json.Marshal(summaryRec); err == nil {
				if _, err := dstF.WriteString(string(data) + "\n"); err != nil {
					dstF.Close()
					os.Remove(tmp)
					return 0, fmt.Errorf("migration summary write: %w", err)
				}
				migrated++
			}
		}
	}
	if err := dstF.Sync(); err != nil {
		dstF.Close()
		os.Remove(tmp)
		return 0, fmt.Errorf("migration sync: %w", err)
	}
	if err := dstF.Close(); err != nil {
		os.Remove(tmp)
		return 0, fmt.Errorf("migration close: %w", err)
	}

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

	// Checkpoints always update the index — reset the debounce timer.
	if s.lastIndexUpdate == nil {
		s.lastIndexUpdate = make(map[string]time.Time)
	}
	s.lastIndexUpdate[ses.ID] = time.Now()
	return s.updateIndex(ses)
}

// appendRecordLine encodes rec to a single buffer then writes it in one
// os.File.Write call. Combined with the store mutex, this guarantees no JSONL
// line interleaving even for records larger than PIPE_BUF.
func appendRecordLine(path string, rec jsonlRecord) error {
	return appendRecordLines(path, []jsonlRecord{rec})
}

// lockSessionFile acquires a cross-process exclusive lock on a per-session
// flock sidecar (path + ".flock"), reusing the lockIndexFile implementation.
// It serializes full-file rewrites (backfill/migrate: read -> tmp -> rename)
// against O_APPEND writers in OTHER processes — the store mutex only guards
// the current process. The sidecar is never renamed or replaced, so the lock
// stays valid across the atomic rename of the data file (#558 D).
func lockSessionFile(path string) (func(), error) {
	return lockIndexFile(path)
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
	// #558 D: hold the cross-process session lock for the duration of the
	// write so a concurrent full-file rewrite (backfill/migrate read -> tmp ->
	// rename) in another process cannot drop this append between its read
	// and its rename. Within one process the store mutex already serializes.
	//
	// Retry flock acquisition with exponential backoff to handle transient
	// lock contention. If all retries fail, abort the append rather than
	// continuing with unlocked O_APPEND writes that can be lost to concurrent
	// renames (60/60 appends lost in probes without this guard).
	var unlock func()
	var lockErr error
	for i := 0; i < 3; i++ {
		unlock, lockErr = lockSessionFile(path)
		if lockErr == nil {
			break
		}
		if i < 2 {
			// Exponential backoff: 10ms, 20ms, 40ms
			time.Sleep(time.Duration(10*(1<<i)) * time.Millisecond)
		}
	}
	if lockErr != nil {
		debug.Log("session", "appendRecordLines: failed to acquire session lock after 3 retries: %v", lockErr)
		return lockErr
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	// We intentionally skip f.Sync() (fsync) here for performance:
	//   - Save() only does O_CREATE touch, no fsync on the atomic rename path.
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
			sb.WriteString(c.Output)
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

	// #558 D: lock across read -> tmp -> rename (cross-process lost-update
	// window vs O_APPEND writers, same as migrateMessageIDs).
	unlock, lockErr := lockSessionFile(path)
	if lockErr != nil {
		debug.Log("session", "backfillIDs: session lock unavailable: %v", lockErr)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

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
	writeErr := false
	for _, line := range lines {
		if _, err := dstF.WriteString(line + "\n"); err != nil {
			debug.Log("session", "backfillIDsAsync: write failed: %v", err)
			writeErr = true
			break
		}
	}
	if err := dstF.Sync(); err != nil {
		debug.Log("session", "backfillIDsAsync: sync failed: %v", err)
		writeErr = true
	}
	dstF.Close()
	if writeErr {
		os.Remove(tmp)
		return
	}

	if err := os.Rename(tmp, path); err != nil {
		debug.Log("session", "backfillIDsAsync: rename failed: %v", err)
		os.Remove(tmp)
		return
	}
	debug.Log("session", "backfillIDsAsync: backfilled IDs in session %s", sessionID)
}

// backfillTimestamps adds timestamps to message records that don't have one.
// All backfilled messages get the same timestamp: 6 hours ago.
// If the first message already has a timestamp, the entire session is skipped.
// This is called from loadSession for historical sessions created before
// the timestamp feature was added.
// firstMessageHasTimestamp reads the file until it finds the first "message"
// record and returns true if it has a non-zero timestamp. This is used by
// backfillTimestamps to skip sessions that don't need backfilling without
// reading the entire file.
func firstMessageHasTimestamp(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false // can't determine — let backfillTimestamps handle it
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, `"message"`) {
			continue
		}
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type == "message" {
			return !rec.Timestamp.IsZero()
		}
	}
	return false
}

func (s *JSONLStore) backfillTimestamps(sessionID string) {
	path := s.sessionPath(sessionID)

	// #558 D: lock across read -> tmp -> rename (cross-process lost-update
	// window vs O_APPEND writers, same as migrateMessageIDs).
	unlock, lockErr := lockSessionFile(path)
	if lockErr != nil {
		debug.Log("session", "backfillTimestamps: session lock unavailable: %v", lockErr)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	// Fast path: stream-scan only until the first message record.
	// If it already has a timestamp, the entire session is skipped without
	// reading the rest of the file. This avoids full-file I/O + memory
	// allocation for sessions that don't need backfilling (the common case).
	if firstMessageHasTimestamp(path) {
		return
	}

	f, err := os.Open(path)
	if err != nil {
		debug.Log("session", "backfillTimestamps: open failed: %v", err)
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
		debug.Log("session", "backfillTimestamps: scan failed: %v", err)
		return
	}

	// Find the first real timestamp (if any) to use as backfill base.
	var firstRealTimestamp time.Time
	for _, line := range lines {
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type == "message" && !rec.Timestamp.IsZero() {
			firstRealTimestamp = rec.Timestamp
			break
		}
	}

	// Check first message record — if it already has a timestamp, skip entirely.
	firstMsgHasTimestamp := !firstRealTimestamp.IsZero()
	if firstMsgHasTimestamp {
		return
	}

	// Assign strictly increasing timestamps (+1ms per backfilled line) so
	// mixed sessions (backfilled prefix + real-timestamped suffix) stay
	// monotonic: a single shared timestamp broke findMessageCutoff's
	// binary search, which assumes file order == chronological order (#198).
	// Use min(firstRealTimestamp, now-6h) to ensure monotonicity:
	// - If first real timestamp is older than 6h, use it as base.
	// - Otherwise use now-6h to avoid polluting recent sessions.
	backfillBase := time.Now().Add(-6 * time.Hour)
	if !firstRealTimestamp.IsZero() && firstRealTimestamp.Before(backfillBase) {
		backfillBase = firstRealTimestamp
	}
	backfillTime := backfillBase
	changed := false
	n := 0
	for i, line := range lines {
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type != "message" || !rec.Timestamp.IsZero() {
			continue
		}
		rec.Timestamp = backfillTime.Add(time.Duration(n) * time.Millisecond)
		n++
		if data, err := json.Marshal(rec); err == nil {
			lines[i] = string(data)
			changed = true
		}
	}

	if !changed {
		return
	}

	tmp := path + ".tsbackfill.tmp"
	dstF, err := os.Create(tmp)
	if err != nil {
		debug.Log("session", "backfillTimestamps: create temp failed: %v", err)
		return
	}
	writeErr := false
	for _, line := range lines {
		if _, err := dstF.WriteString(line + "\n"); err != nil {
			debug.Log("session", "backfillTimestamps: write failed: %v", err)
			writeErr = true
			break
		}
	}
	if err := dstF.Sync(); err != nil {
		debug.Log("session", "backfillTimestamps: sync failed: %v", err)
		writeErr = true
	}
	dstF.Close()
	if writeErr {
		os.Remove(tmp)
		return
	}

	if err := os.Rename(tmp, path); err != nil {
		debug.Log("session", "backfillTimestamps: rename failed: %v", err)
		os.Remove(tmp)
		return
	}
	debug.Log("session", "backfillTimestamps: backfilled timestamps in session %s", sessionID)
}

// dedupMessageRecords removes duplicate message records, keeping only the
// first occurrence of each unique message. Non-message records are passed
// through unchanged.
//
// Dedup key: if the message has an ID, use the ID (exact match). This is
// the common case — the StartRunTracking bug produced byte-identical
// copies with the same ID. For messages without an ID (very old sessions
// pre-dating the ID feature), fall back to a content fingerprint.
// Using content fingerprint for ALL messages would incorrectly merge
// distinct messages that happen to have identical content (e.g. a user
// sending "continue" twice, or two identical build outputs).
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
		key := dedupKey(rec.Message)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, rec)
	}
	return out
}

// dedupKey returns a deduplication key for a message. Messages with a
// non-empty ID are deduped by ID; messages without an ID fall back to a
// content fingerprint.
func dedupKey(msg *provider.Message) string {
	if msg.ID != "" {
		return "id:" + msg.ID
	}
	return "fp:" + messageFingerprint(msg)
}

// dedupLightweightEntries removes duplicate message-type entries from a
// localLightweightEntry slice. Non-message entries (cost, etc.) are kept as-is.
// Uses the same ID-first strategy as dedupMessageRecords.
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
		key := dedupKey(e.record.Message)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}
