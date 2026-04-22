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

	conversation []Message
	mu           sync.Mutex
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

// Save persists the session to disk.
func (s *Session) Save(dir string) error {
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

	path := filepath.Join(dir, s.ID+".json")
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
	path := filepath.Join(dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading session file: %w", err)
	}
	var sd SessionData
	if err := json.Unmarshal(data, &sd); err != nil {
		return nil, fmt.Errorf("decoding session: %w", err)
	}
	return &Session{
		ID:           sd.ID,
		CWD:          sd.CWD,
		CreatedAt:    sd.CreatedAt,
		MCPServers:   sd.MCPServers,
		conversation: sd.Messages,
	}, nil
}
