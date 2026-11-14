package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`
	Workspace string    `json:"workspace,omitempty"`
	Vendor    string    `json:"vendor"`
	Endpoint  string    `json:"endpoint"`
	Model     string    `json:"model"`

	Preview         string                           `json:"preview,omitempty"`
	TokenUsage      provider.TokenUsage              `json:"token_usage,omitempty"`
	EndpointUsage   map[string]provider.TokenUsage   `json:"endpoint_usage,omitempty"`
	UsageHistory    []UsageEntry                     `json:"usage_history,omitempty"`
	Metrics         []metrics.MetricEvent            `json:"metrics,omitempty"`
	EndpointMetrics map[string][]metrics.MetricEvent `json:"endpoint_metrics,omitempty"`

	Messages []provider.Message `json:"messages,omitempty"`

	ContextMessages        []provider.Message `json:"-"`
	CheckpointTokens       int                `json:"-"`
	CheckpointMessageCount int                `json:"-"`
	TunnelEvents           []TunnelEvent      `json:"tunnel_events,omitempty"`
	TunnelEventsComplete   bool               `json:"tunnel_events_complete,omitempty"`

	TunnelEventsMu sync.Mutex `json:"-"`

	CostJSON []byte `json:"cost,omitempty"`

	PermissionMode string `json:"permission_mode,omitempty"`

	SidebarVisible *bool `json:"sidebar_visible,omitempty"`

	ParentSessionID string `json:"parent_session_id,omitempty"`
	ForkPoint       int    `json:"fork_point,omitempty"`

	Pinned bool `json:"pinned,omitempty"`

	Tags []string `json:"tags,omitempty"`

	ContextWindow int `json:"context_window,omitempty"`

	MaxTokens int `json:"max_tokens,omitempty"`

	TasksJSON []byte `json:"tasks,omitempty"`

	TasksEnvJSON []byte `json:"tasks_env,omitempty"`

	endpointStatsMu sync.RWMutex
}

type TunnelEvent struct {
	EventID  string          `json:"event_id"`
	StreamID string          `json:"stream_id,omitempty"`
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type UsageEntry struct {
	Timestamp time.Time           `json:"timestamp"`
	TurnIndex int                 `json:"turn_index"`
	Model     string              `json:"model,omitempty"`
	Vendor    string              `json:"vendor,omitempty"`
	Endpoint  string              `json:"endpoint,omitempty"`
	Source    string              `json:"source,omitempty"`
	Usage     provider.TokenUsage `json:"usage"`
}

type Store interface {
	Save(s *Session) error

	Load(id string) (*Session, error)

	LoadWithOptions(id string, fullLoad bool) (*Session, error)

	List() ([]*Session, error)

	Delete(id string) error

	ExportMarkdown(id string) (string, error)

	CleanupOlderThan(before time.Time) (int, error)

	LatestForWorkspace(workspace string) (*Session, error)

	ListForWorkspace(workspace string) ([]*Session, error)

	AppendCheckpoint(s *Session, summaryMsgID, lastMsgID string, tokenCount int) error

	AppendMetaToDisk(s *Session) error

	SetPinned(s *Session, pinned bool) error

	SetTags(s *Session, tags []string) error

	WithSessionLocked(s *Session, fn func())
}

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
	Pinned    bool      `json:"pinned,omitempty"`
	Tags      []string  `json:"tags,omitempty"`

	ParentSessionID string `json:"parent_session_id,omitempty"`
	ForkPoint       int    `json:"fork_point,omitempty"`
}

type JSONLStore struct {
	dir string

	mu sync.Mutex

	indexDirtyMu       sync.Mutex
	indexDirty         bool
	maintenanceRunning bool
	lastMaintenance    time.Time

	createdPending map[string]time.Time

	createdPendingMu sync.Mutex

	lastIndexUpdate map[string]time.Time

	fullLoad bool
}

const indexUpdateDebounce = 5 * time.Second

const sessionMaintenanceInterval = 30 * time.Second

func (s *Session) SnapshotTunnelEvents() []TunnelEvent {
	if s == nil {
		return nil
	}
	s.TunnelEventsMu.Lock()
	defer s.TunnelEventsMu.Unlock()
	if s.TunnelEvents == nil {
		return nil
	}
	return append([]TunnelEvent(nil), s.TunnelEvents...)
}

const MaxTunnelEvents = 2000

const MaxContextMessages = 200

const RecentMessageWindow = 24 * time.Hour

const recentMessageThreshold = 500

func quickExtractTimestamp(line []byte) time.Time {

	start, end := topLevelStringField(line, "timestamp")
	if start < 0 {
		return time.Time{}
	}
	if end-start < 20 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339, string(line[start:end]))
	if err != nil {
		return time.Time{}
	}
	return ts
}

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
				return -1, -1
			}
			tokenEnd := j + 1

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

// lastDialogueIndex returns the index of the most recent user-role message,
// or -1 when the slice contains none. Used by the MaxContextMessages
// truncation (#607) to guarantee the agent context window keeps real
// dialogue even when a system-note tail fills the message quota.
func lastDialogueIndex(msgs []provider.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return i
		}
	}
	return -1
}

// ComputeBranchCutoff returns the exclusive end index for a fork that drops
// the last dropRounds complete user turns. dropRounds <= 0 keeps everything
// (tail fork). The boolean is false when the request would drop every user
// turn (or the message list has no user turns at all), i.e. the fork point
// does not exist.
func ComputeBranchCutoff(msgs []provider.Message, dropRounds int) (int, bool) {
	if dropRounds <= 0 {
		return len(msgs), true
	}
	var userIdx []int
	for i, m := range msgs {
		if m.Role == "user" {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= dropRounds {
		return 0, false
	}
	return userIdx[len(userIdx)-dropRounds], true
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

func findMessageCutoff(path string) (int64, int, time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, time.Time{}
	}
	defer f.Close()

	type msgOff struct {
		offset     int64
		ts         time.Time
		isDialogue bool
	}
	var offsets []msgOff
	var pos int64

	br := bufio.NewReader(f)
	const maxLine = 10 * 1024 * 1024
	for {
		raw, consumed, rerr := readLineLimitedCounted(br, maxLine)
		if rerr == errLineTooLong {
			pos += consumed
			continue
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {

			debug.Log("session", "findMessageCutoff %s: I/O error at offset %d after %d message records: cutoff computed from PARTIAL file", path, pos, len(offsets))
		}
		if line := bytes.TrimRight(raw, "\r\n"); len(line) > 0 {

			if rt := quickRecordType(line); rt == "message" {
				ts := quickExtractTimestamp(line)
				offsets = append(offsets, msgOff{offset: pos, ts: ts, isDialogue: quickIsDialogueRole(line)})
			}
		}
		pos += consumed
		if rerr != nil {
			break
		}
	}

	totalMsgs := len(offsets)
	if totalMsgs < recentMessageThreshold {
		return 0, totalMsgs, time.Time{}
	}

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

		return 0, totalMsgs, time.Time{}
	}

	cutoff := last.Add(-RecentMessageWindow)

	lo, hi := 0, totalMsgs
	for lo < hi {
		mid := (lo + hi) / 2

		if !offsets[mid].ts.IsZero() && offsets[mid].ts.Before(cutoff) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	if lo == 0 {
		return 0, totalMsgs, last
	}

	return offsets[lo].offset, totalMsgs, last
}

func messageHasToolResult(msg provider.Message) bool {
	for _, b := range msg.Content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

func messageHasToolUse(msg provider.Message) bool {
	for _, b := range msg.Content {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}

func isOrphanToolMessage(msg provider.Message) bool {
	if msg.Role == "user" && messageHasToolResult(msg) {
		return true
	}
	if msg.Role == "assistant" && messageHasToolUse(msg) {
		return true
	}
	return false
}

func NewJSONLStore(dir string) (*JSONLStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating session directory %s: %w", dir, err)
	}
	return &JSONLStore{dir: dir}, nil
}

func DefaultDir() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "sessions"), nil
}

func NewDefaultStore() (*JSONLStore, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return NewJSONLStore(dir)
}

func (s *JSONLStore) LoadWithOptions(id string, fullLoad bool) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.fullLoad
	s.fullLoad = fullLoad
	defer func() { s.fullLoad = prev }()
	return s.loadSession(id)
}

func (s *JSONLStore) SetFullLoad(full bool) {
	s.mu.Lock()
	s.fullLoad = full
	s.mu.Unlock()
}

func (s *JSONLStore) indexPath() string {
	return filepath.Join(s.dir, "index.json")
}

func (s *JSONLStore) loadIndex() ([]indexEntry, error) {
	return s.loadIndexImpl(true)
}

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

		debug.Log("session", "loadIndex: corrupt session index, rebuilding from disk: %v", err)
		s.setIndexDirty(true)
		if !canRepair {

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

func lockWithBackoff(lockFn func() (func(), error), op string, kind string) (func(), error) {
	var unlock func()
	var lockErr error
	for i := 0; i < 3; i++ {
		unlock, lockErr = lockFn()
		if lockErr == nil {
			return unlock, nil
		}
		if i < 2 {

			time.Sleep(time.Duration(10*(1<<i)) * time.Millisecond)
		}
	}
	debug.Log("session", "%s: failed to acquire %s lock after 3 retries: %v", op, kind, lockErr)
	return nil, lockErr
}

func (s *JSONLStore) updateIndex(ses *Session) error {
	unlock, lockErr := lockWithBackoff(func() (func(), error) { return lockIndexFile(s.indexPath()) }, "updateIndex", "index")
	if lockErr != nil {
		s.setIndexDirty(true)
		return lockErr
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	idx, err := s.loadIndexNoRepair()
	if err != nil {
		s.setIndexDirty(true)
		return err
	}
	if idx == nil && s.getIndexDirty() {

		return nil
	}
	found := false
	for i, e := range idx {
		if e.ID == ses.ID {
			newEntry := sessionToIndexEntry(ses)

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
		s.setIndexDirty(true)
		return err
	}
	s.setIndexDirty(false)
	return nil
}

func (s *JSONLStore) removeFromIndex(id string) error {

	unlock, lockErr := lockWithBackoff(func() (func(), error) { return lockIndexFile(s.indexPath()) }, "removeFromIndex", "index")
	if lockErr != nil {
		s.setIndexDirty(true)
		return lockErr
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	idx, err := s.loadIndexNoRepair()
	if err != nil {
		s.setIndexDirty(true)
		return err
	}
	if idx == nil && s.getIndexDirty() {

		return nil
	}
	filtered := make([]indexEntry, 0, len(idx))
	for _, e := range idx {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}

	// #4769 (defect 3): match updateIndex - a failed save must mark the
	// index dirty so runMaintenance's early reconciliation rebuilds it;
	// otherwise the on-disk index keeps the deleted session forever (the
	// self-healing loop never fires).

	if err := s.saveIndex(filtered); err != nil {
		s.setIndexDirty(true)
		return err
	}
	return nil
}

func sessionToIndexEntry(s *Session) indexEntry {
	return indexEntry{
		ID:              s.ID,
		Title:           s.Title,
		Preview:         s.Preview,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		Workspace:       s.Workspace,
		MsgCount:        len(s.Messages),
		Vendor:          s.Vendor,
		Endpoint:        s.Endpoint,
		Model:           s.Model,
		Pinned:          s.Pinned,
		Tags:            s.Tags,
		ParentSessionID: s.ParentSessionID,
		ForkPoint:       s.ForkPoint,
	}
}

type jsonlRecord struct {
	Type                 string              `json:"type"`
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
	Timestamp            time.Time           `json:"timestamp,omitempty"`
	TunnelEvent          *TunnelEvent        `json:"tunnel_event,omitempty"`
	CostJSON             json.RawMessage     `json:"cost,omitempty"`

	UsageEntry *UsageEntry `json:"usage_entry,omitempty"`

	MetricEvent *metrics.MetricEvent `json:"metric_event,omitempty"`

	PermissionMode string   `json:"permission_mode,omitempty"`
	SidebarVisible *bool    `json:"sidebar_visible,omitempty"`
	Pinned         bool     `json:"pinned,omitempty"`
	Tags           []string `json:"tags,omitempty"`

	ParentSessionID string `json:"parent_session_id,omitempty"`
	ForkPoint       int    `json:"fork_point,omitempty"`
	ContextWindow   int    `json:"context_window,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`

	TasksJSON []byte `json:"tasks,omitempty"`

	TasksEnvJSON []byte `json:"tasks_env,omitempty"`

	CheckpointSummaryMsgID string `json:"checkpoint_summary_msg_id,omitempty"`
	CheckpointLastMsgID    string `json:"checkpoint_last_msg_id,omitempty"`
	CheckpointTokens       int    `json:"checkpoint_tokens,omitempty"`

	CheckpointMessages []provider.Message `json:"checkpoint_messages,omitempty"`
}

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
	return !strings.HasPrefix(id, "-")
}

func (s *JSONLStore) sessionPath(id string) string {
	if !validSessionID(id) {
		return filepath.Join(s.dir, "_invalid_id_.jsonl")
	}
	return filepath.Join(s.dir, id+".jsonl")
}

// RecentMessage is one conversation turn exposed by the introspection read
// port (#5575 phase 3): role plus concatenated text content.
type RecentMessage struct {
	Role    string
	Content string
}

// RecentMessages returns the tail n messages (role+text) of session id's
// jsonl. Each jsonl line is a full Session snapshot appended on save, so
// the LAST decodable line wins and its Messages provide the tail. Missing
// file or invalid id yields an empty slice and nil error — an absent source
// is an empty face, not an error (introspection convention). Read-only.
func (s *JSONLStore) RecentMessages(id string, n int) []RecentMessage {
	if n <= 0 {
		return nil
	}
	path := s.sessionPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var last *Session
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ses Session
		if json.Unmarshal([]byte(line), &ses) != nil {
			continue
		}
		last = &ses
	}
	if last == nil {
		return nil
	}
	msgs := last.Messages
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	out := make([]RecentMessage, 0, len(msgs))
	for _, m := range msgs {
		var b strings.Builder
		for _, blk := range m.Content {
			if blk.Type == "text" && blk.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(blk.Text)
			}
		}
		out = append(out, RecentMessage{Role: m.Role, Content: b.String()})
	}
	return out
}

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

func (s *JSONLStore) Save(ses *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ses.UpdatedAt = time.Now()

	s.createdPendingMu.Lock()
	defer s.createdPendingMu.Unlock()
	if s.createdPending == nil {
		s.createdPending = make(map[string]time.Time)
	}
	s.createdPending[ses.ID] = time.Now()

	path := s.sessionPath(ses.ID)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("creating session file: %w", err)
	}
	f.Close()

	debug.Log("session", "Save: ensured file exists for session %s (no index update)", ses.ID)
	return nil
}

func (s *JSONLStore) Load(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadSession(id)
}

type localLightweightEntry struct {
	recType string
	record  jsonlRecord
}

func dedupeUserMsgs(msgs []provider.Message) []provider.Message {
	userKey := func(m provider.Message) (string, bool) {
		if m.Role != "user" {
			return "", false
		}

		if m.Origin == "human" {
			return "", false
		}
		var sb strings.Builder
		for _, b := range m.Content {
			if b.Type == "tool_result" {
				return "", false
			}
			if b.Type == "text" {
				sb.WriteString(b.Text)
				continue
			}
			if b.Type == "image" {
				prefix := b.ImageData
				if len(prefix) > 32 {
					prefix = prefix[:32]
				}
				sb.WriteString(" img{" + b.ImageMIME + "|" + strconv.Itoa(len(b.ImageData)) + "|" + prefix + "}")
			}
		}
		return sb.String(), true
	}
	seen := make(map[string]bool)
	out := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		key, isUser := userKey(m)
		if isUser {
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, m)
	}
	return out
}

func capContextTail(msgs []provider.Message) []provider.Message {
	if len(msgs) <= MaxContextMessages {
		return msgs
	}
	omitted := len(msgs) - MaxContextMessages
	start := len(msgs) - MaxContextMessages
	for start > 0 && isOrphanToolMessage(msgs[start]) {
		start--
		omitted--
	}
	if lastUser := lastDialogueIndex(msgs); lastUser >= 0 && start > lastUser {
		tailLen := len(msgs) - lastUser
		back := MaxContextMessages - tailLen
		if back < 0 {
			back = 0
		}
		if lastUser-back > 0 {
			start = lastUser - back
		} else {
			start = 0
		}
		for start > 0 && isOrphanToolMessage(msgs[start]) {
			start--
		}
		omitted = start
	}
	return append([]provider.Message{{
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("[Note: %d earlier messages were truncated to fit the context window. The conversation starts mid-way. Re-read relevant files if you need earlier context.]", omitted),
		}},
	}}, msgs[start:]...)
}

func appendPostCheckpointExtras(ses *Session, entries []localLightweightEntry, summaryMsgID string) {
	for _, entry := range entries {
		if entry.recType != "message" || entry.record.Message == nil {
			continue
		}
		if summaryMsgID != "" && (entry.record.Message.ID == summaryMsgID || isSummaryNoteMessage(entry.record.Message)) {
			continue
		}
		ses.ContextMessages = append(ses.ContextMessages, *entry.record.Message)
	}
}

func (s *JSONLStore) loadSession(id string) (*Session, error) {
	path := s.sessionPath(id)

	var msgCutoff int64
	var totalMsgCount int
	if !s.fullLoad {
		msgCutoff, totalMsgCount, _ = findMessageCutoff(path)
		if msgCutoff > 0 {
			debug.Log("session", "loadSession %s: time-windowed load, cutoff at offset %d (%d total messages)", id, msgCutoff, totalMsgCount)
		}
	}

	if migrated, err := s.migrateMessageIDs(id); err != nil {
		debug.Log("session", "loadSession: migration failed for %s: %v (continuing with original file)", id, err)
	} else if migrated > 0 {
		debug.Log("session", "loadSession: migrated %d message IDs in session %s", migrated, id)

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

	br := bufio.NewReader(f)
	const maxLine = 10 * 1024 * 1024
	skippedLong := 0

	type lightweightEntry = localLightweightEntry
	var (
		metaRecords        []jsonlRecord
		lastCpMessages     []provider.Message
		lastCpSummaryMsgID string
		lastCpLastMsgID    string
		lastCpTokens       int
		allMessages        []jsonlRecord
		postCPEntries      []lightweightEntry
		allUsage           []jsonlRecord
		allMetrics         []jsonlRecord
		haveCheckpoint     bool
	)

	var byteOffset int64
	var ioErr error
	for {
		raw, consumed, rerr := readLineLimitedCounted(br, maxLine)
		byteOffset += consumed
		if rerr == errLineTooLong {
			skippedLong++
			continue
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {

			ioErr = rerr
			debug.Log("session", "loadSession %s: I/O error at offset %d after %d message records: %v", id, byteOffset, len(allMessages), rerr)
		}
		lineLen := consumed
		line := strings.TrimSpace(string(raw))
		if line == "" {
			if rerr != nil {
				break
			}
			continue
		}
		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {

			debug.Log("session", "loadSession %s: skipping malformed JSONL line at offset %d: %v", id, byteOffset-lineLen, err)
			continue
		}

		lineStart := byteOffset - lineLen

		switch rec.Type {
		case "meta":
			metaRecords = append(metaRecords, rec)
		case "checkpoint":

			if rec.CheckpointSummaryMsgID != "" {
				lastCpSummaryMsgID = rec.CheckpointSummaryMsgID
				lastCpLastMsgID = rec.CheckpointLastMsgID
				lastCpTokens = rec.CheckpointTokens
				haveCheckpoint = true

				lastCpMessages = nil

			} else if len(rec.CheckpointMessages) > 0 {

				lastCpMessages = rec.CheckpointMessages
				lastCpLastMsgID = ""
				lastCpTokens = rec.CheckpointTokens
				postCPEntries = nil

				lastCpSummaryMsgID = ""
				haveCheckpoint = true
			}
		case "usage":

			allUsage = append(allUsage, rec)
		case "metric":

			allMetrics = append(allMetrics, rec)
		case "message":

			if msgCutoff > 0 && lineStart < msgCutoff {

				postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
				continue
			}

			allMessages = append(allMessages, rec)

			postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
		case "cost":
			postCPEntries = append(postCPEntries, lightweightEntry{recType: rec.Type, record: rec})
		case "message_remove":

			allMessages = popLastUserRecord(allMessages)
			postCPEntries = popLastUserLightweightEntry(postCPEntries)
		case "tunnel_event":

		}
		if rerr != nil {

			break
		}
	}
	if skippedLong > 0 {
		debug.Log("session", "loadSession %s: skipped %d over-long line(s) >10MB — session remains loadable (#656)", id, skippedLong)
	}
	f.Close()
	if ioErr != nil {

		return nil, fmt.Errorf("loadSession %s: I/O error reading session file (partial read of %d messages, refusing to return truncated state): %w", id, len(allMessages), ioErr)
	}

	anyMissing := false
	for _, m := range allMessages {
		if m.Timestamp.IsZero() {
			anyMissing = true
			break
		}
	}
	if len(allMessages) > 0 && anyMissing {
		s.backfillTimestamps(id)
	}

	for _, rec := range metaRecords {
		ses.Title = rec.Title
		ses.Preview = rec.Preview

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

		ses.Pinned = rec.Pinned
		ses.Tags = rec.Tags
		if rec.SidebarVisible != nil {
			ses.SidebarVisible = rec.SidebarVisible
		}
		if rec.ContextWindow > 0 {
			ses.ContextWindow = rec.ContextWindow
		}
		if rec.MaxTokens > 0 {
			ses.MaxTokens = rec.MaxTokens
		}

		ses.TasksJSON = rec.TasksJSON
		ses.TasksEnvJSON = rec.TasksEnvJSON

		if rec.ParentSessionID != "" {
			ses.ParentSessionID = rec.ParentSessionID
			ses.ForkPoint = rec.ForkPoint
		}
	}

	if ses.Workspace == "" {
		ses.Workspace = CurrentWorkspacePath()
		debug.Log("session", "loadSession %s: workspace was empty, falling back to %s", id, ses.Workspace)
	}

	allMessages = dedupMessageRecords(allMessages)
	postCPEntries = dedupLightweightEntries(postCPEntries)

	for _, rec := range allMessages {
		if rec.Message != nil {
			ses.Messages = append(ses.Messages, *rec.Message)
		}
	}

	ses.Messages = dedupeUserMsgs(ses.Messages)

	if lastCpSummaryMsgID != "" {

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

			ses.ContextMessages = append(ses.ContextMessages, *summaryMsg)
			if lastCpLastMsgID != "" {

				extraStart := -1
				for i := 0; i < len(postCPEntries); i++ {
					entry := postCPEntries[i]
					if entry.recType == "message" && entry.record.Message != nil && entry.record.Message.ID == lastCpLastMsgID {
						extraStart = i + 1
						break
					}
				}
				if extraStart >= 0 {

					appendPostCheckpointExtras(ses, postCPEntries[extraStart:], lastCpSummaryMsgID)
				} else {

					afterSummary := len(postCPEntries) - summaryMsgIdx - 1
					debug.Log("session", "loadSession %s: checkpoint last_msg_id %q not found in postCPEntries, using post-summary fallback (%d entries after summary)", id, lastCpLastMsgID, afterSummary)
					appendPostCheckpointExtras(ses, postCPEntries[summaryMsgIdx+1:], "")
				}
			} else {

				appendPostCheckpointExtras(ses, postCPEntries[summaryMsgIdx+1:], "")
			}
			ses.CheckpointTokens = lastCpTokens
			ses.CheckpointMessageCount = len(ses.ContextMessages)
		}

	}
	if len(ses.ContextMessages) == 0 && haveCheckpoint && len(lastCpMessages) > 0 {

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

	if len(ses.ContextMessages) == 0 {
		var unwindowed []provider.Message
		for _, entry := range postCPEntries {
			if entry.recType == "message" && entry.record.Message != nil {
				unwindowed = append(unwindowed, *entry.record.Message)
			}
		}
		ses.ContextMessages = capContextTail(unwindowed)
	}

	ses.ContextMessages = dedupeUserMsgs(ses.ContextMessages)

	ses.ContextMessages = capContextTail(ses.ContextMessages)

	cpInfo := "no-checkpoint"
	if lastCpSummaryMsgID != "" {
		cpInfo = fmt.Sprintf("checkpoint summary=%s last_msg=%s tokens=%d", lastCpSummaryMsgID, lastCpLastMsgID, lastCpTokens)
	}
	debug.Log("session", "loadSession %s: ContextMessages=%d (total=%d) [%s]",
		id, len(ses.ContextMessages), len(ses.Messages), cpInfo)

	if len(ses.ContextMessages) > 0 {
		needsID := false
		for i := range ses.ContextMessages {
			if ses.ContextMessages[i].ID == "" {
				needsID = true
				break
			}
		}
		if needsID {

			updates := make(map[string]provider.Message)
			for i := range ses.ContextMessages {
				if ses.ContextMessages[i].ID == "" {
					ses.ContextMessages[i].ID = newSessionMessageID()
					fp := messageFingerprint(&ses.ContextMessages[i])
					updates[fp] = ses.ContextMessages[i]
				}
			}

			for i := range ses.Messages {
				if ses.Messages[i].ID == "" {
					fp := messageFingerprint(&ses.Messages[i])
					if updated, ok := updates[fp]; ok {
						ses.Messages[i].ID = updated.ID
					}
				}
			}

			s.backfillIDs(ses.ID, updates)
		}

	}

	for _, rec := range allUsage {
		if rec.UsageEntry != nil {
			ses.UsageHistory = append(ses.UsageHistory, *rec.UsageEntry)
		}
	}

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

func (s *JSONLStore) repairIndexOutsideLock(idx []indexEntry) ([]indexEntry, error) {
	s.mu.Unlock()
	changed, err := s.repairIndex(idx)
	s.mu.Lock()
	if err != nil {
		return idx, err
	}
	if changed {
		return s.loadIndex()
	}
	return idx, nil
}

func (s *JSONLStore) List() ([]*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	if len(idx) == 0 {

		idx, err = s.repairIndexOutsideLock(idx)
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(idx, func(i, j int) bool {
		if idx[i].Pinned != idx[j].Pinned {
			return idx[i].Pinned
		}
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})
	s.scheduleMaintenanceLocked()

	result := make([]*Session, 0, len(idx))
	for _, e := range idx {
		result = append(result, &Session{
			ID:              e.ID,
			Title:           e.Title,
			Preview:         e.Preview,
			CreatedAt:       e.CreatedAt,
			UpdatedAt:       e.UpdatedAt,
			Workspace:       e.Workspace,
			Vendor:          e.Vendor,
			Endpoint:        e.Endpoint,
			Model:           e.Model,
			Pinned:          e.Pinned,
			Tags:            e.Tags,
			ParentSessionID: e.ParentSessionID,
			ForkPoint:       e.ForkPoint,
		})
	}
	return result, nil
}

func (s *JSONLStore) ListChildren(parentID string) ([]*Session, error) {
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	children := make([]*Session, 0)
	for _, ses := range all {
		if ses.ParentSessionID == parentID {
			children = append(children, ses)
		}
	}
	return children, nil
}

func (s *JSONLStore) setIndexDirty(v bool) {
	s.indexDirtyMu.Lock()
	s.indexDirty = v
	s.indexDirtyMu.Unlock()
}

func (s *JSONLStore) getIndexDirty() bool {
	s.indexDirtyMu.Lock()
	defer s.indexDirtyMu.Unlock()
	return s.indexDirty
}

func (s *JSONLStore) scheduleMaintenanceLocked() {
	if s.maintenanceRunning {
		return
	}
	if !s.getIndexDirty() && !s.lastMaintenance.IsZero() && time.Since(s.lastMaintenance) < sessionMaintenanceInterval {
		return
	}

	s.maintenanceRunning = true
	safego.Go("session.runMaintenance", func() { s.runMaintenance() })
}

func (s *JSONLStore) runMaintenance() {

	defer func() {
		s.mu.Lock()
		s.lastMaintenance = time.Now()
		s.maintenanceRunning = false
		s.mu.Unlock()
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
		s.setIndexDirty(false)
		return
	}

	unlock, lockErr := lockIndexFile(s.indexPath())
	if lockErr != nil {
		debug.Log("session", "runMaintenance: failed to acquire index lock: %v", lockErr)
		s.setIndexDirty(true)
		return
	}
	defer unlock()

	currentIdx, err := s.loadIndexNoRepair()
	if err != nil {
		debug.Log("session", "runMaintenance: failed to reload index under lock: %v", err)
		s.setIndexDirty(true)
		return
	}

	merged := s.mergeMaintainedIndex(idx, validIdx, currentIdx)
	if err := s.saveIndex(merged); err != nil {
		s.setIndexDirty(true)
		return
	}
	s.setIndexDirty(false)
}

func (s *JSONLStore) mergeMaintainedIndex(idx, validIdx, currentIdx []indexEntry) []indexEntry {
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

	seen := make(map[string]bool, len(currentIdx)+len(validIdx))
	merged := make([]indexEntry, 0, len(currentIdx))
	emit := func(e indexEntry) {
		if seen[e.ID] {
			return
		}
		seen[e.ID] = true
		merged = append(merged, e)
	}
	for _, e := range currentIdx {
		if validSet[e.ID] || !originalSet[e.ID] {
			emit(e)
		}
	}
	for _, e := range validIdx {
		if !currentSet[e.ID] {
			if _, statErr := os.Stat(s.sessionPath(e.ID)); statErr != nil {
				debug.Log("session", "runMaintenance: dropping %s - absent from locked index reload and file missing (deleted elsewhere)", e.ID)
				continue
			}
		}
		emit(e)
	}
	return merged
}

var sessionLockProbe = IsSessionLocked

func (s *JSONLStore) sessionUnlockedNow(sessionID string) bool {
	return !sessionLockProbe(s.dir, sessionID)
}

const sessionCreationWindow = 1 * time.Hour

func (s *JSONLStore) inCreationWindow(id string) bool {
	s.createdPendingMu.Lock()
	defer s.createdPendingMu.Unlock()
	ts, ok := s.createdPending[id]
	if !ok {
		return false
	}
	if time.Since(ts) > sessionCreationWindow {
		delete(s.createdPending, id)
		return false
	}
	return true
}

func (s *JSONLStore) pruneInvalidIndexEntries(idx []indexEntry) ([]indexEntry, bool) {
	cleaned := false
	validIdx := make([]indexEntry, 0, len(idx))
	for _, e := range idx {

		if _, statErr := os.Stat(s.sessionPath(e.ID)); statErr != nil {
			debug.Log("session", "pruneInvalidIndexEntries: keeping %s despite stat error: %v", e.ID, statErr)
			validIdx = append(validIdx, e)
			continue
		}
		interacted, hasErr := s.HasUserInteractionOnDisk(e.ID)
		if hasErr != nil {

			debug.Log("session", "pruneInvalidIndexEntries: keeping %s despite on-disk check error: %v", e.ID, hasErr)
			validIdx = append(validIdx, e)
			continue
		}
		if !interacted && s.inCreationWindow(e.ID) {

			validIdx = append(validIdx, e)
			continue
		}
		if !interacted {

			if !s.sessionUnlockedNow(e.ID) {
				debug.Log("session", "pruneInvalidIndexEntries: %s is flock-held by another process, keeping", e.ID)
				validIdx = append(validIdx, e)
				continue
			}

			if rmErr := os.Remove(s.sessionPath(e.ID)); rmErr != nil && !os.IsNotExist(rmErr) {
				debug.Log("session", "pruneInvalidIndexEntries: remove failed for %s, keeping index entry: %v", e.ID, rmErr)
				validIdx = append(validIdx, e)
				continue
			}
			cleaned = true
			continue
		}
		validIdx = append(validIdx, e)
	}
	return validIdx, cleaned
}

func (s *JSONLStore) RepairIndex() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.loadIndexFromDisk()
	if err != nil {
		return false, err
	}
	return s.repairIndex(idx)
}

func (s *JSONLStore) repairIndex(idx []indexEntry) (bool, error) {
	unlock, lockErr := lockIndexFile(s.indexPath())
	if lockErr != nil {

		return false, fmt.Errorf("repairIndex: failed to acquire index lock: %w", lockErr)
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

	diskIDs := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		diskIDs[id] = true
	}

	changed := s.getIndexDirty()
	newIdx := make([]indexEntry, 0, len(idx))

	for _, e := range idx {
		if !diskIDs[e.ID] {

			changed = true
			continue
		}
		newIdx = append(newIdx, e)
	}

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

				interacted, hasErr := s.HasUserInteractionOnDisk(id)
				if hasErr != nil {
					debug.Log("session", "repairIndex: keeping orphan session %s — on-disk check unknown: %v", id, hasErr)
					newIdx = append(newIdx, sessionToIndexEntry(ses))
				} else if !interacted {

					if s.inCreationWindow(id) {
						debug.Log("session", "repairIndex: %s is inside the creation window, keeping un-indexed for next pass", id)
						continue
					}

					if !s.sessionUnlockedNow(id) {
						debug.Log("session", "repairIndex: orphan %s is flock-held by another process, keeping un-indexed for next pass", id)
						continue
					}

					if rmErr := os.Remove(s.sessionPath(id)); rmErr != nil && !os.IsNotExist(rmErr) {
						debug.Log("session", "repairIndex: remove failed for orphan %s, keeping it un-indexed for the next pass: %v", id, rmErr)
						continue
					}
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
		s.setIndexDirty(false)
	}
	return changed, nil
}

func (s *JSONLStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(id)

	if err := s.removeFromIndex(id); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {

		return fmt.Errorf("removing session file after index update: %w", err)
	}

	if err := os.Remove(path + ".flock"); err != nil && !os.IsNotExist(err) {
		debug.Log("session", "removing flock sidecar for %s: %v", id, err)
	}
	return nil
}

func (s *JSONLStore) HasUserInteractionOnDisk(id string) (bool, error) {
	path := s.sessionPath(id)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {

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

		if errors.Is(scanErr, bufio.ErrTooLong) {
			debug.Log("session", "HasUserInteractionOnDisk %s: line exceeds scanner buffer, conservatively keeping", id)
			return true, nil
		}
		return false, fmt.Errorf("scanning session file %s: %w", path, scanErr)
	}
	return false, nil
}

func (s *JSONLStore) CleanupIfEmpty(ses *Session) error {
	if !s.WillCleanupIfEmpty(ses) {
		if !ses.HasUserInteraction() {
			debug.Log("session", "CleanupIfEmpty: keeping session %s — user messages found on disk outside load window", ses.ID)
		}
		return nil
	}
	return s.Delete(ses.ID)
}

func (s *JSONLStore) WillCleanupIfEmpty(ses *Session) bool {
	if ses.HasUserInteraction() {
		return false
	}

	interacted, err := s.HasUserInteractionOnDisk(ses.ID)
	if err != nil {
		debug.Log("session", "WillCleanupIfEmpty: keeping session %s — on-disk check unknown: %v", ses.ID, err)
		return false
	}
	return !interacted
}

func (s *JSONLStore) persistEligible(ses *Session) bool {
	if ses.HasUserInteraction() {
		return true
	}
	interacted, err := s.HasUserInteractionOnDisk(ses.ID)
	if err != nil {
		debug.Log("session", "persistEligible: on-disk check unknown for %s, persisting — %v", ses.ID, err)
		return true
	}
	return interacted
}

func (s *JSONLStore) LatestForWorkspace(workspace string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	normalizedWorkspace := NormalizeWorkspacePath(workspace)

	hasWorkspace := false
	for _, e := range idx {
		if NormalizeWorkspacePath(e.Workspace) == normalizedWorkspace {
			hasWorkspace = true
			break
		}
	}
	if !hasWorkspace {

		repaired, repairErr := s.repairIndexOutsideLock(idx)
		if repairErr != nil {
			debug.Log("session", "LatestForWorkspace: repairIndex error: %v", repairErr)
		} else {
			idx = repaired
		}
	}

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

func (s *JSONLStore) ListForWorkspace(workspace string) ([]*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	normalizedWorkspace := NormalizeWorkspacePath(workspace)
	hasWorkspace := false
	for _, e := range idx {
		if NormalizeWorkspacePath(e.Workspace) == normalizedWorkspace {
			hasWorkspace = true
			break
		}
	}
	if !hasWorkspace {

		repaired, repairErr := s.repairIndexOutsideLock(idx)
		if repairErr != nil {
			debug.Log("session", "ListForWorkspace: repairIndex error: %v", repairErr)
		} else {
			idx = repaired
		}
	}

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
				Pinned:    e.Pinned,
				Tags:      e.Tags,
			})
		}
	}
	return result, nil
}

func (s *JSONLStore) ExportMarkdown(id string) (string, error) {

	ses, err := s.LoadWithOptions(id, true)
	if err != nil {
		return "", err
	}
	return ExportSessionMarkdown(ses), nil
}

func ExportSessionMarkdown(ses *Session) string {
	return ExportSessionMarkdownWithDisplay(ses, "", "")
}

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

func (s *JSONLStore) CleanupOlderThan(before time.Time) (int, error) {
	sessions, err := s.List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, ses := range sessions {
		if ses.Pinned {

			continue
		}
		if ses.UpdatedAt.Before(before) {
			if err := s.Delete(ses.ID); err != nil {
				return removed, fmt.Errorf("deleting session %s: %w", ses.ID, err)
			}
			removed++
		}
	}
	return removed, nil
}

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

func (s *JSONLStore) AppendMessageToDisk(ses *Session, msg provider.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg, Timestamp: time.Now()}
	if err := appendRecordLine(path, rec); err != nil {
		return err
	}

	if s.lastIndexUpdate == nil {
		s.lastIndexUpdate = make(map[string]time.Time)
	}
	if last, ok := s.lastIndexUpdate[ses.ID]; ok && time.Since(last) < indexUpdateDebounce {
		return nil
	}
	s.lastIndexUpdate[ses.ID] = time.Now()
	return s.updateIndex(ses)
}

func (s *JSONLStore) AppendUserMessageRemovalToDisk(ses *Session) error {
	if ses == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)
	rec := jsonlRecord{Type: "message_remove", SessionID: ses.ID, Timestamp: time.Now()}
	return appendRecordLine(path, rec)
}

func popLastUserRecord(recs []jsonlRecord) []jsonlRecord {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Message != nil && recs[i].Message.Role == "user" {
			return append(recs[:i:i], recs[i+1:]...)
		}
	}
	return recs
}

func popLastUserLightweightEntry(entries []localLightweightEntry) []localLightweightEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if e := entries[i].record; e.Message != nil && e.Message.Role == "user" {
			return append(entries[:i:i], entries[i+1:]...)
		}
	}
	return entries
}

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

	if s.lastIndexUpdate == nil {
		s.lastIndexUpdate = make(map[string]time.Time)
	}
	s.lastIndexUpdate[ses.ID] = time.Now()

	s.createdPendingMu.Lock()
	delete(s.createdPending, ses.ID)
	s.createdPendingMu.Unlock()
	return s.updateIndex(ses)
}

func (s *JSONLStore) AppendTunnelEventToDisk(ses *Session, ev TunnelEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	rec := jsonlRecord{Type: "tunnel_event", SessionID: ses.ID, TunnelEvent: &ev}
	return appendRecordLine(path, rec)
}

func (s *JSONLStore) AppendMetaToDisk(ses *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendMetaLocked(ses)
}

func (s *JSONLStore) appendMetaLocked(ses *Session) error {
	if !s.persistEligible(ses) {
		return nil
	}

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
		Pinned:               ses.Pinned,
		Tags:                 ses.Tags,
		ContextWindow:        ses.ContextWindow,
		MaxTokens:            ses.MaxTokens,
		TasksJSON:            ses.TasksJSON,
		TasksEnvJSON:         ses.TasksEnvJSON,
		ParentSessionID:      ses.ParentSessionID,
		ForkPoint:            ses.ForkPoint,
	}
	if err := appendRecordLine(path, rec); err != nil {
		return err
	}
	return s.updateIndex(ses)
}

func (s *JSONLStore) SetPinned(ses *Session, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ses.Pinned = pinned
	return s.appendMetaLocked(ses)
}

func (s *JSONLStore) SetTags(ses *Session, tags []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ses.Tags = normalizeTags(tags)
	return s.appendMetaLocked(ses)
}

func (s *JSONLStore) WithSessionLocked(ses *Session, fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

const (
	maxTagRunes       = 32
	maxTagsPerSession = 16
)

func normalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if r := []rune(t); len(r) > maxTagRunes {
			t = string(r[:maxTagRunes])
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
		if len(out) >= maxTagsPerSession {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *JSONLStore) AppendUsageEntry(ses *Session, entry UsageEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.sessionPath(ses.ID)
	if !s.persistEligible(ses) {
		return nil
	}
	rec := jsonlRecord{
		Type:       "usage",
		SessionID:  ses.ID,
		UsageEntry: &entry,
	}
	return appendRecordLine(path, rec)
}

func (s *JSONLStore) AppendMetric(ses *Session, m metrics.MetricEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.sessionPath(ses.ID)
	if !s.persistEligible(ses) {
		return nil
	}
	rec := jsonlRecord{
		Type:        "metric",
		SessionID:   ses.ID,
		MetricEvent: &m,
	}
	return appendRecordLine(path, rec)
}

func (s *JSONLStore) EnsureMeta(ses *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.sessionPath(ses.ID)

	if !ses.HasUserInteraction() {
		if _, statErr := os.Stat(path); statErr != nil {
			return nil
		}
		if !s.persistEligible(ses) {
			return nil
		}
	}

	unlock, lockErr := lockWithBackoff(func() (func(), error) { return lockSessionFile(path) }, "EnsureMeta", "session")
	if lockErr != nil {
		return lockErr
	}
	defer unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	meta := jsonlRecord{
		Type:      "meta",
		SessionID: ses.ID,
		Title:     ses.Title,

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
		Pinned:               ses.Pinned,
		Tags:                 ses.Tags,
		ContextWindow:        ses.ContextWindow,
		MaxTokens:            ses.MaxTokens,
		TasksJSON:            ses.TasksJSON,
		TasksEnvJSON:         ses.TasksEnvJSON,
		ParentSessionID:      ses.ParentSessionID,
		ForkPoint:            ses.ForkPoint,
	}
	if err := enc.Encode(meta); err != nil {

		os.Remove(path)
		return err
	}

	return s.updateIndex(ses)
}

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
	for _, u := range ses.UsageHistory {
		if u.TurnIndex > last {
			last = u.TurnIndex
		}
	}
	for _, m := range ses.Metrics {
		if m.TurnIndex > last {
			last = m.TurnIndex
		}
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

func newSessionMessageID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("msg_fallback_%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("msg_%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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

		if !strings.Contains(line, `"checkpoint"`) {
			continue
		}
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type == "checkpoint" && rec.CheckpointSummaryMsgID != "" {
			return true
		}
	}
	return false
}

func (s *JSONLStore) migrateMessageIDs(id string) (int, error) {
	path := s.sessionPath(id)

	unlock, lockErr := lockWithBackoff(func() (func(), error) { return lockSessionFile(path) }, "migrateMessageIDs", "session")
	if lockErr != nil {
		debug.Log("session", "migrateMessageIDs: skipping migration after lock retry exhaustion: %v", lockErr)
		return 0, nil //nolint:nilerr // migration is best-effort; skipping is safe, proceeding unlocked is not
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	if hasNewFormatCheckpoint(path) {
		return 0, nil
	}

	scan, err := scanMigratableSession(path)
	if err != nil {
		return 0, err
	}
	lines, lastOldCpIdx := scan.lines, scan.oldCpIdx
	if scan.newFormatCp {

		return 0, nil
	}

	if lastOldCpIdx < 0 {

		return 0, nil
	}
	lastOldCpSummary, lastOldCpLastMsg := scan.summary, scan.lastMsg

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

	summaryID := newSessionMessageID()
	if lastOldCpSummary.ID == "" {
		lastOldCpSummary.ID = summaryID
	}
	lastMsgID := ""
	if lastMsgLineIdx >= 0 {

		var rec jsonlRecord
		if json.Unmarshal([]byte(lines[lastMsgLineIdx]), &rec) == nil && rec.Message != nil {
			if rec.Message.ID == "" {
				rec.Message.ID = newSessionMessageID()
			}
			lastMsgID = rec.Message.ID

			if data, err := json.Marshal(rec); err == nil {
				lines[lastMsgLineIdx] = string(data)
			}
		}
	}

	backfillFrom := lastMsgLineIdx + 1
	if backfillFrom <= 0 {
		backfillFrom = lastOldCpIdx + 1
	}
	migrated := backfillMessageIDs(lines, backfillFrom)

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

	added, err := writeMigratedSession(path, lines, lastOldCpIdx, lastOldCpSummary)
	if err != nil {
		return 0, err
	}
	migrated += added

	debug.Log("session", "migrateMessageIDs: migrated session %s: summary_msg_id=%s last_msg_id=%s backfilled=%d",
		id, lastOldCpSummary.ID, lastMsgID, migrated)
	return migrated, nil
}

type migrationScan struct {
	lines       []string
	oldCpIdx    int
	summary     *provider.Message
	lastMsg     *provider.Message
	newFormatCp bool
}

func scanMigratableSession(path string) (migrationScan, error) {
	var out migrationScan
	out.oldCpIdx = -1

	srcF, err := os.Open(path)
	if err != nil {
		return out, err
	}
	sc := bufio.NewScanner(srcF)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		out.lines = append(out.lines, line)

		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type == "checkpoint" {
			if rec.CheckpointSummaryMsgID != "" {

				out.newFormatCp = true
			} else if len(rec.CheckpointMessages) > 0 {

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
					out.oldCpIdx = len(out.lines) - 1
					out.summary = summary
					out.lastMsg = &rec.CheckpointMessages[len(rec.CheckpointMessages)-1]
				}
			}
		}
	}
	srcF.Close()
	if sc.Err() != nil {
		return out, fmt.Errorf("migration scan: %w", sc.Err())
	}
	return out, nil
}

func backfillMessageIDs(lines []string, from int) int {
	migrated := 0
	for i := from; i < len(lines); i++ {
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
	return migrated
}

func writeMigratedSession(path string, lines []string, cpIdx int, summary *provider.Message) (int, error) {
	tmp := path + ".migrate.tmp"
	dstF, err := os.Create(tmp)
	if err != nil {
		return 0, fmt.Errorf("creating migration temp file: %w", err)
	}
	added := 0
	for i, line := range lines {
		if _, err := dstF.WriteString(line + "\n"); err != nil {
			dstF.Close()
			os.Remove(tmp)
			return 0, fmt.Errorf("migration write: %w", err)
		}

		if i == cpIdx {
			summaryRec := jsonlRecord{
				Type:    "message",
				Message: summary,
			}
			if data, err := json.Marshal(summaryRec); err == nil {
				if _, err := dstF.WriteString(string(data) + "\n"); err != nil {
					dstF.Close()
					os.Remove(tmp)
					return 0, fmt.Errorf("migration summary write: %w", err)
				}
				added++
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
	return added, nil
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405"), time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(b))
}

func (s *JSONLStore) Dir() string {
	return s.dir
}

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

	if s.lastIndexUpdate == nil {
		s.lastIndexUpdate = make(map[string]time.Time)
	}
	s.lastIndexUpdate[ses.ID] = time.Now()
	return s.updateIndex(ses)
}

func readLineLimitedCounted(br *bufio.Reader, limit int) (line []byte, consumed int64, err error) {
	var buf []byte
	for {
		chunk, rerr := br.ReadSlice('\n')
		consumed += int64(len(chunk))
		if rerr == bufio.ErrBufferFull {
			if len(buf)+len(chunk) > limit {

				for rerr == bufio.ErrBufferFull {
					chunk, rerr = br.ReadSlice('\n')
					consumed += int64(len(chunk))
				}
				return nil, consumed, errLineTooLong
			}
			buf = append(buf, chunk...)
			continue
		}
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, consumed, errLineTooLong
		}
		return buf, consumed, rerr
	}
}

func terminateTornTail(f *os.File) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() == 0 {
		return nil
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], st.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	if _, err := f.Write([]byte{'\n'}); err != nil {
		return err
	}
	debug.Log("session", "appendRecordLines: terminated torn trailing line (crash residue) in %s", f.Name())
	return nil
}

func appendRecordLine(path string, rec jsonlRecord) error {
	return appendRecordLines(path, []jsonlRecord{rec})
}

func lockSessionFile(path string) (func(), error) {
	return lockIndexFile(path)
}

func appendRecordLines(path string, recs []jsonlRecord) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, rec := range recs {
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}

	unlock, lockErr := lockWithBackoff(func() (func(), error) { return lockSessionFile(path) }, "appendRecordLines", "session")
	if lockErr != nil {
		return lockErr
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return err
	}

	if err := terminateTornTail(f); err != nil {

		f.Close()
		return fmt.Errorf("torn-tail check for %s: %w", path, err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

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

func (s *JSONLStore) backfillIDs(sessionID string, updates map[string]provider.Message) {
	path := s.sessionPath(sessionID)

	unlock, lockErr := lockSessionFile(path)
	if lockErr != nil {

		debug.Log("session", "backfillIDs: session lock unavailable, skipping: %v", lockErr)
		return
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

func firstMessageHasTimestamp(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
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

	unlock, lockErr := lockSessionFile(path)
	if lockErr != nil {

		debug.Log("session", "backfillTimestamps: session lock unavailable, skipping: %v", lockErr)
		return
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

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

	backfillBase := time.Now().Add(-6 * time.Hour)
	if !firstRealTimestamp.IsZero() && firstRealTimestamp.Before(backfillBase) {
		backfillBase = firstRealTimestamp
	}

	if !firstRealTimestamp.IsZero() {
		prefixZero := 0
		for _, line := range lines {
			var rec jsonlRecord
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			if rec.Type != "message" || isSummaryNoteMessage(rec.Message) {
				continue
			}
			if rec.Timestamp.IsZero() {
				prefixZero++
				continue
			}
			break
		}
		if prefixZero > 0 {
			backfillBase = firstRealTimestamp.Add(-time.Duration(prefixZero+1) * time.Millisecond)
		}
	}
	cursor := backfillBase
	changed := false
	for i, line := range lines {
		var rec jsonlRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type != "message" {
			continue
		}

		if isSummaryNoteMessage(rec.Message) {
			continue
		}
		if !rec.Timestamp.IsZero() {
			if rec.Timestamp.After(cursor) {
				cursor = rec.Timestamp
			}
			continue
		}
		cursor = cursor.Add(time.Millisecond)
		rec.Timestamp = cursor
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

func isSummaryNoteMessage(m *provider.Message) bool {
	if m == nil || m.Role != "system" {
		return false
	}
	for _, b := range m.Content {
		if b.Type == "text" && strings.HasPrefix(b.Text, "[Previous conversation summary]") {
			return true
		}
	}
	return false
}

func dedupMessageRecords(records []jsonlRecord) []jsonlRecord {
	if len(records) <= 1 {
		return records
	}

	seenIDs := make(map[string]bool, len(records))

	var prevFP string
	out := records[:0]
	for _, rec := range records {
		if rec.Message == nil {
			prevFP = ""
			out = append(out, rec)
			continue
		}
		if id := rec.Message.ID; id != "" {
			if seenIDs[id] {
				continue
			}
			seenIDs[id] = true
			prevFP = ""
			out = append(out, rec)
			continue
		}
		fp := messageFingerprint(rec.Message)
		if fp == prevFP {
			continue
		}
		prevFP = fp
		out = append(out, rec)
	}
	return out
}

func dedupLightweightEntries(entries []localLightweightEntry) []localLightweightEntry {
	if len(entries) <= 1 {
		return entries
	}

	seenIDs := make(map[string]bool, len(entries))
	var prevFP string
	out := entries[:0]
	for _, e := range entries {
		if e.recType != "message" || e.record.Message == nil {
			prevFP = ""
			out = append(out, e)
			continue
		}
		if id := e.record.Message.ID; id != "" {
			if seenIDs[id] {
				continue
			}
			seenIDs[id] = true
			prevFP = ""
			out = append(out, e)
			continue
		}
		fp := messageFingerprint(e.record.Message)
		if fp == prevFP {
			continue
		}
		prevFP = fp
		out = append(out, e)
	}
	return out
}
