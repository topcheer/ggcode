package im

import (
	"sync"
	"time"
)

const (
	pcDefaultSessionTTLMS = 7 * 24 * 60 * 60 * 1000 // 7 days
	pcStateAwaitingHello  = "awaiting_hello"
	pcStateActive         = "active"
)

type pcParticipant struct {
	AppID       string
	DisplayName string
	DeviceLabel string
	JoinedAt    string
	LastSeenAt  string
}

type pcConversationTurn struct {
	MessageID string
	Role      string // "user", "assistant", "system", "thinking"
	Text      string
	SentAt    string
	AppID     string
	ReplyTo   string
}

type pcSession struct {
	mu           sync.RWMutex
	Invite       PCInvite
	Label        string
	State        string // "awaiting_hello" | "active"
	GroupMode    bool
	BotMuted     bool
	History      []pcConversationTurn
	Participants map[string]*pcParticipant
	RemovedApps  map[string]bool
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

func newPCSession(invite PCInvite, label string, groupMode bool, expiresAt time.Time) *pcSession {
	return &pcSession{
		Invite:       invite,
		Label:        label,
		State:        pcStateAwaitingHello,
		GroupMode:    groupMode,
		Participants: make(map[string]*pcParticipant),
		RemovedApps:  make(map[string]bool),
		ExpiresAt:    expiresAt,
		CreatedAt:    time.Now(),
	}
}

func (s *pcSession) IsExpired() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Now().After(s.ExpiresAt)
}

func (s *pcSession) IsActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State == pcStateActive && !time.Now().After(s.ExpiresAt)
}

func (s *pcSession) UpsertParticipant(appID, displayName, deviceLabel string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := pcNowISO()
	if p, ok := s.Participants[appID]; ok {
		p.LastSeenAt = now
		if displayName != "" {
			p.DisplayName = displayName
		}
		if deviceLabel != "" {
			p.DeviceLabel = deviceLabel
		}
		return
	}
	s.Participants[appID] = &pcParticipant{
		AppID:       appID,
		DisplayName: displayName,
		DeviceLabel: deviceLabel,
		JoinedAt:    now,
		LastSeenAt:  now,
	}
}

func (s *pcSession) IsAppRemoved(appID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.RemovedApps[appID]
}

func (s *pcSession) MarkAppRemoved(appID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RemovedApps[appID] = true
	delete(s.Participants, appID)
}

func (s *pcSession) SetActive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = pcStateActive
}

func (s *pcSession) ParticipantCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Participants)
}

func (s *pcSession) AppendHistory(turn pcConversationTurn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append(s.History, turn)
	// Keep last 200 turns
	if len(s.History) > 200 {
		s.History = s.History[len(s.History)-200:]
	}
}

func (s *pcSession) RecentHistory(limit int) []pcConversationTurn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.History) {
		limit = len(s.History)
	}
	result := make([]pcConversationTurn, limit)
	copy(result, s.History[len(s.History)-limit:])
	return result
}
