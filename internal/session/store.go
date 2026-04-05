package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// Session represents a single conversation session.
type Session struct {
	ID        string             `json:"id"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Title     string             `json:"title"`
	Vendor    string             `json:"vendor"`
	Endpoint  string             `json:"endpoint"`
	Model     string             `json:"model"`
	Messages  []provider.Message `json:"messages,omitempty"`
	// Cost data stored as opaque JSON to avoid circular dependency with cost package.
	CostJSON []byte `json:"cost,omitempty"`
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
}

// indexEntry is a lightweight record for fast session listing.
type indexEntry struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	MsgCount  int       `json:"msg_count"`
	Vendor    string    `json:"vendor,omitempty"`
	Endpoint  string    `json:"endpoint,omitempty"`
	Model     string    `json:"model,omitempty"`
}

// JSONLStore implements Store using JSONL files.
type JSONLStore struct {
	dir string // ~/.ggcode/sessions/
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
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
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
		return nil, err
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
	return s.saveIndex(idx)
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
		MsgCount:  len(s.Messages),
		Vendor:    s.Vendor,
		Endpoint:  s.Endpoint,
		Model:     s.Model,
	}
}

// --- JSONL helpers ---

// jsonlRecord is written one-per-line in the session file.
type jsonlRecord struct {
	Type      string            `json:"type"` // "meta" or "message"
	SessionID string            `json:"session_id,omitempty"`
	Title     string            `json:"title,omitempty"`
	Vendor    string            `json:"vendor,omitempty"`
	Endpoint  string            `json:"endpoint,omitempty"`
	Model     string            `json:"model,omitempty"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Message   *provider.Message `json:"message,omitempty"`
	CostJSON  json.RawMessage   `json:"cost,omitempty"`
}

func (s *JSONLStore) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".jsonl")
}

// Save writes the full session as a JSONL file (atomic).
func (s *JSONLStore) Save(ses *Session) error {
	ses.UpdatedAt = time.Now()

	tmp := s.sessionPath(ses.ID) + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	// Meta record
	meta := jsonlRecord{
		Type:      "meta",
		SessionID: ses.ID,
		Title:     ses.Title,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Model:     ses.Model,
		CreatedAt: ses.CreatedAt,
		UpdatedAt: ses.UpdatedAt,
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

	// Cost record (if present)
	if len(ses.CostJSON) > 0 {
		costRec := jsonlRecord{Type: "cost", SessionID: ses.ID, CostJSON: ses.CostJSON}
		if err := enc.Encode(costRec); err != nil {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("encoding cost: %w", err)
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
			ses.Title = rec.Title
			ses.Vendor = rec.Vendor
			ses.Endpoint = rec.Endpoint
			ses.Model = rec.Model
			ses.CreatedAt = rec.CreatedAt
			ses.UpdatedAt = rec.UpdatedAt
		case "message":
			if rec.Message != nil {
				ses.Messages = append(ses.Messages, *rec.Message)
			}
		case "cost":
			if rec.CostJSON != nil {
				ses.CostJSON = []byte(rec.CostJSON)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading session %s: %w", id, err)
	}
	if ses.CreatedAt.IsZero() {
		ses.CreatedAt = time.Now()
	}
	if ses.UpdatedAt.IsZero() {
		ses.UpdatedAt = ses.CreatedAt
	}
	return ses, nil
}

// List returns all sessions sorted by UpdatedAt descending (uses index for speed).
func (s *JSONLStore) List() ([]*Session, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}

	changed, err := s.repairIndex(idx)
	if err != nil {
		return nil, err
	}
	if changed {
		// Reload repaired index
		idx, err = s.loadIndex()
		if err != nil {
			return nil, err
		}
	}

	// Sort by UpdatedAt descending
	sort.Slice(idx, func(i, j int) bool {
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})

	result := make([]*Session, 0, len(idx))
	for _, e := range idx {
		result = append(result, &Session{
			ID:        e.ID,
			Title:     e.Title,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
			Vendor:    e.Vendor,
			Endpoint:  e.Endpoint,
			Model:     e.Model,
		})
	}
	return result, nil
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

	changed := false
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
			ses, loadErr := s.Load(id)
			if loadErr == nil {
				newIdx = append(newIdx, sessionToIndexEntry(ses))
				changed = true
			}
		}
	}

	if changed {
		if err := s.saveIndex(newIdx); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// Delete removes a session file and its index entry.
func (s *JSONLStore) Delete(id string) error {
	path := s.sessionPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return s.removeFromIndex(id)
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
func (s *JSONLStore) AppendMessage(ses *Session, msg provider.Message) error {
	path := s.sessionPath(ses.ID)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	rec := jsonlRecord{Type: "message", SessionID: ses.ID, Message: &msg}
	if err := enc.Encode(rec); err != nil {
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
						if len(b.Text) > 60 {
							ses.Title = b.Text[:57] + "..."
						} else {
							ses.Title = b.Text
						}
						break
					}
				}
				break
			}
		}
	}

	return s.updateIndex(ses)
}

// EnsureMeta writes the meta record if the session file doesn't exist yet.
func (s *JSONLStore) EnsureMeta(ses *Session) error {
	path := s.sessionPath(ses.ID)
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
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
		Type:      "meta",
		SessionID: ses.ID,
		Title:     ses.Title,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Model:     ses.Model,
		CreatedAt: ses.CreatedAt,
		UpdatedAt: ses.UpdatedAt,
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
		ID:        generateID(),
		CreatedAt: now,
		UpdatedAt: now,
		Vendor:    vendor,
		Endpoint:  endpoint,
		Model:     model,
		Title:     "New session",
	}
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
