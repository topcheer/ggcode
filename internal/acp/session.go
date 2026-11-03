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

type Session struct {
	ID         string
	CWD        string
	MCPServers []MCPServer
	CreatedAt  time.Time
	Cancel     func()
	mcpManager *MCPManager
	saveDir    string

	conversation []Message
	mu           sync.Mutex

	runActive bool
}

type Message struct {
	Role    string
	Content []ContentBlock
}

func NewSession(cwd string, mcpServers []MCPServer) *Session {
	id := generateSessionID()
	return &Session{
		ID:         id,
		CWD:        cwd,
		MCPServers: mcpServers,
		CreatedAt:  time.Now(),
	}
}

func (s *Session) SetSaveDir(dir string) {
	s.saveDir = dir
}

func (s *Session) SaveDir() string {
	return s.saveDir
}

func (s *Session) AddMessage(role string, content []ContentBlock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversation = append(s.conversation, Message{
		Role:    role,
		Content: content,
	})
}

func (s *Session) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.conversation))
	copy(out, s.conversation)
	return out
}

func (s *Session) ReplaceConversation(msgs []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversation = make([]Message, len(msgs))
	copy(s.conversation, msgs)
}

func (s *Session) SetCancel(cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cancel = cancel
}

func (s *Session) DoCancel() {
	s.mu.Lock()
	cancel := s.Cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Session) TryBeginRun() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runActive {
		return false
	}
	s.runActive = true
	return true
}

func (s *Session) EndRun() {
	s.mu.Lock()
	s.runActive = false
	s.mu.Unlock()
}

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

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type SessionData struct {
	ID         string      `json:"id"`
	CWD        string      `json:"cwd"`
	CreatedAt  time.Time   `json:"createdAt"`
	UpdatedAt  time.Time   `json:"updatedAt"`
	Messages   []Message   `json:"messages"`
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
}

func (s *Session) HasMessages() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conversation) > 0
}

func (s *Session) Save(dir string) error {
	if !validSessionID(s.ID) {
		return fmt.Errorf("invalid session id %q: refusing to write outside the session store", s.ID)
	}
	s.mu.Lock()
	hasMessages := len(s.conversation) > 0
	s.mu.Unlock()

	path := filepath.Join(dir, s.ID+".json")

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

func LoadSession(dir, id string) (*Session, error) {
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
