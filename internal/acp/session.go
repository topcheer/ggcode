package acp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session represents an active ACP session.
type Session struct {
	ID         string
	CWD        string
	MCPServers []MCPServer
	CreatedAt  time.Time
	Cancel     func()
	mcpManager *MCPManager // MCP server lifecycle manager
	saveDir    string      // directory for session persistence

	conversation []Message
	mu           sync.Mutex

	runActive bool // prompt busy guard (#1033): one in-flight prompt per session
}

// Message represents a single conversation message within a session.
type Message struct {
	Role    string // "user" or "assistant"
	Content []ContentBlock
}

// NewSession creates a new session with a unique ID.
func NewSession(cwd string, mcpServers []MCPServer) *Session {
	id := generateSessionID()
	return &Session{
		ID:         id,
		CWD:        cwd,
		MCPServers: mcpServers,
		CreatedAt:  time.Now(),
	}
}

// SetSaveDir sets the directory where session files are persisted.
func (s *Session) SetSaveDir(dir string) {
	s.saveDir = dir
}

// SaveDir returns the session save directory.
func (s *Session) SaveDir() string {
	return s.saveDir
}

// AddMessage appends a message to the session conversation.
func (s *Session) AddMessage(role string, content []ContentBlock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversation = append(s.conversation, Message{
		Role:    role,
		Content: content,
	})
}

// Messages returns a copy of the conversation history.
func (s *Session) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.conversation))
	copy(out, s.conversation)
	return out
}

// ReplaceConversation replaces the entire conversation history.
// Used after context compaction to store the compressed messages.
func (s *Session) ReplaceConversation(msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversation = make([]Message, len(msgs))
	copy(s.conversation, msgs)
}

// SetCancel sets the cancellation function for this session.
func (s *Session) SetCancel(cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cancel = cancel
}

// DoCancel cancels the current operation for this session.
func (s *Session) DoCancel() {
	s.mu.Lock()
	cancel := s.Cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// TryBeginRun attempts to mark the session as having a prompt in flight.
// Returns false if another prompt is already running (#1033): the handler
// must reject the new prompt instead of concurrently driving the same loop.
func (s *Session) TryBeginRun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runActive {
		return false
	}
	s.runActive = true
	return true
}

// EndRun releases the prompt busy guard set by TryBeginRun.
func (s *Session) EndRun() {
	s.mu.Lock()
	s.runActive = false
	s.mu.Unlock()
}

// validSessionID reports whether id matches the format produced by
// generateSessionID: 32 lowercase hex chars. Session IDs are used to build
// filesystem paths ("<dir>/<id>.json"); rejecting any other form blocks path
// traversal via client-supplied session/resume IDs and hostile "id" fields
// stored inside session files (which would otherwise redirect Save).
func validSessionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// generateSessionID creates a random session identifier.
func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SessionData is the JSON-serializable form of a session for persistence.
type SessionData struct {
	ID         string      `json:"id"`
	CWD        string      `json:"cwd"`
	CreatedAt  time.Time   `json:"createdAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
	Messages   []Message   `json:"messages"`
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
}

// HasMessages returns true if the session has any conversation history.
func (s *Session) HasMessages() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conversation) > 0
}

// Save persists the session to disk.
// If the session has no conversation history, the file is deleted (or not created).
func (s *Session) Save(dir string) error {
	if !validSessionID(s.ID) {
		return fmt.Errorf("invalid session id %q: refusing to write outside the session store", s.ID)
	}
	s.mu.Lock()
	hasMessages := len(s.conversation) > 0
	s.mu.Unlock()

	path := filepath.Join(dir, s.ID+".json")

	// No conversation history — remove the file if it exists (e.g. from a previous save).
	if !hasMessages {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing empty session file: %w", err)
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data := SessionData{
		ID:         s.ID,
		CWD:        s.CWD,
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  time.Now(),
		Messages:   s.conversation,
		MCPServers: s.MCPServers,
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating session file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encoding session: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("closing session file: %w", err)
	}
	return os.Rename(tmp, path)
}

// LoadSession loads a session from disk.
func LoadSession(dir, id string) (*Session, error) {
	// id is client-supplied on the session/resume path; validate it so
	// "../../etc/foo" cannot escape dir via filepath.Join cleaning.
	if !validSessionID(id) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	path := filepath.Join(dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}
	var sd SessionData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil, fmt.Errorf("decoding session: %w", err)
	}
	// The id field inside the file feeds Save()'s path on later turns; a
	// hand-edited/hostile file must not smuggle a traversal payload in.
	if !validSessionID(sd.ID) {
		return nil, fmt.Errorf("session file contains invalid id %q", sd.ID)
	}
	return &Session{
		ID:           sd.ID,
		CWD:          sd.CWD,
		CreatedAt:    sd.CreatedAt,
		MCPServers:   sd.MCPServers,
		conversation: sd.Messages,
	}, nil
}
