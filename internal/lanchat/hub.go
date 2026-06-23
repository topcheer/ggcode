package lanchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Hub is the core LAN chat coordinator. It manages participants, sends and
// receives messages, tracks receipts, and queues agent-direct messages for
// host approval.
type Hub struct {
	mu sync.RWMutex

	nodeID    string // this node's A2A instance ID
	humanNick string
	agentNick string
	mode      string // "tui", "gui", "daemon"
	endpoint  string // this node's HTTP endpoint (http://ip:port)
	apiKey    string // A2A API key for peer auth

	// peers discovered via A2A registry callbacks
	peers map[string]*Participant // key = NodeID

	// messages received (in-memory, for UI display)
	messages []Message

	// pending agent-direct messages awaiting host approval
	pendingApproval []PendingAgentMsg

	// attachments manager (nil = attachments disabled)
	attachments *AttachmentManager

	// receipts received, keyed by message ID
	receipts map[string]Receipt

	// store for per-session persistence
	store *Store

	// callbacks for UI integration
	onMessage        func(Message)
	onReceipt        func(Receipt)
	onParticipantAdd func(Participant)
	onParticipantRm  func(nodeID, humanNick string) // nodeID + nick for display
	onApprovalReq    func(PendingAgentMsg)

	// HTTP client for peer communication
	httpClient *http.Client
}

// PendingAgentMsg is an incoming @agent direct message awaiting host approval.
type PendingAgentMsg struct {
	Message  Message
	Received time.Time
}

// NewHub creates a new chat hub.
func NewHub(nodeID, mode, endpoint, apiKey string, store *Store) *Hub {
	nick := RandomNick()
	// Try to load persisted nickname.
	if persisted, err := LoadNick(store.dir); err == nil && persisted != "" {
		nick = persisted
	}
	return &Hub{
		nodeID:    nodeID,
		humanNick: nick,
		agentNick: AgentNick(nick),
		mode:      mode,
		endpoint:  endpoint,
		apiKey:    apiKey,
		peers:     make(map[string]*Participant),
		receipts:  make(map[string]Receipt),
		store:     store,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SetAttachments enables attachment support on this hub.
func (h *Hub) SetAttachments(am *AttachmentManager) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attachments = am
}

// SetSessionID switches nick persistence to a per-session directory.
// This loads the nick from <baseDir>/sessions/<sessionID>/lanchat-nick
// if it exists, overriding the global nick. Subsequent SetNick calls persist
// to this session-scoped path. If sessionID is empty, the global path is used.
func (h *Hub) SetSessionID(baseDir, sessionID string) {
	if sessionID == "" || baseDir == "" {
		return
	}
	sessionDir := filepath.Join(baseDir, "sessions", sessionID)
	h.mu.Lock()
	h.store = NewStore(sessionDir)
	// Try to load session-specific nick
	if persisted, err := LoadNick(h.store.dir); err == nil && persisted != "" {
		h.humanNick = persisted
		h.agentNick = AgentNick(persisted)
	}
	h.mu.Unlock()
}

// Attachments returns the attachment manager (nil if not enabled).
func (h *Hub) Attachments() *AttachmentManager {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.attachments
}

// SetCallbacks registers UI event callbacks.
func (h *Hub) SetCallbacks(
	onMessage func(Message),
	onReceipt func(Receipt),
	onParticipantAdd func(Participant),
	onParticipantRm func(nodeID, humanNick string),
	onApprovalReq func(PendingAgentMsg),
) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onMessage = onMessage
	h.onReceipt = onReceipt
	h.onParticipantAdd = onParticipantAdd
	h.onParticipantRm = onParticipantRm
	h.onApprovalReq = onApprovalReq
}

// HumanNick returns this node's human nickname.
func (h *Hub) HumanNick() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.humanNick
}

// AgentNick returns this node's agent nickname.
func (h *Hub) AgentNick() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agentNick
}

// NodeID returns this node's ID.
func (h *Hub) NodeID() string {
	return h.nodeID
}

// SelfParticipant returns this node's own participant info.
func (h *Hub) SelfParticipant() Participant {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Participant{
		NodeID:    h.nodeID,
		HumanNick: h.humanNick,
		AgentNick: h.agentNick,
		Mode:      h.mode,
		Endpoint:  h.endpoint,
		Online:    true,
		LastSeen:  time.Now().Unix(),
	}
}

// Participants returns all known participants (self + peers).
func (h *Hub) Participants() []Participant {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := []Participant{{
		NodeID:    h.nodeID,
		HumanNick: h.humanNick,
		AgentNick: h.agentNick,
		Mode:      h.mode,
		Endpoint:  h.endpoint,
		Online:    true,
		LastSeen:  time.Now().Unix(),
	}}

	for _, p := range h.peers {
		result = append(result, *p)
	}
	return result
}

// Messages returns a copy of all received messages.
func (h *Hub) Messages() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Message, len(h.messages))
	copy(out, h.messages)
	return out
}

// PendingApprovals returns messages awaiting host approval.
func (h *Hub) PendingApprovals() []PendingAgentMsg {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]PendingAgentMsg, len(h.pendingApproval))
	copy(out, h.pendingApproval)
	return out
}

// SetNick changes the human nickname, updates agent nick, broadcasts to peers,
// and persists locally.
func (h *Hub) SetNick(nick string) error {
	h.mu.Lock()
	h.humanNick = nick
	h.agentNick = AgentNick(nick)
	h.mu.Unlock()

	// Persist
	if err := SaveNick(h.store.dir, nick); err != nil {
		log.Printf("[lanchat] failed to persist nick: %v", err)
	}

	// Broadcast nick change to peers
	go h.broadcastNickChange(nick)
	return nil
}

// UpdatePeers synchronizes the peer list from A2A registry discovery results.
// New peers trigger the onParticipantAdd callback; removed peers trigger onParticipantRm.
func (h *Hub) UpdatePeers(participants []Participant) {
	h.mu.Lock()

	seen := make(map[string]bool)
	var newPeers []Participant

	for _, p := range participants {
		if p.NodeID == h.nodeID {
			continue // skip self
		}
		seen[p.NodeID] = true
		existing, ok := h.peers[p.NodeID]
		if !ok {
			// New peer
			cp := p
			cp.Online = true
			cp.LastSeen = time.Now().Unix()
			h.peers[p.NodeID] = &cp
			newPeers = append(newPeers, cp)
		} else {
			// Update existing peer info — but DON'T overwrite nicks with
			// empty values. A2A discovery doesn't carry lanchat nicks, so
			// p.HumanNick/p.AgentNick are usually "". We must preserve the
			// nicks we learned from presence/messages.
			if p.HumanNick != "" {
				existing.HumanNick = p.HumanNick
			}
			if p.AgentNick != "" {
				existing.AgentNick = p.AgentNick
			}
			existing.Mode = p.Mode
			existing.Endpoint = p.Endpoint
			existing.Online = true
			existing.LastSeen = time.Now().Unix()
		}
	}

	// Mark disappeared peers offline
	type leftPeer struct {
		nodeID, humanNick string
	}
	var leftPeers []leftPeer
	for id, p := range h.peers {
		if !seen[id] {
			if time.Since(time.Unix(p.LastSeen, 0)) > ageOffline {
				if p.Online {
					p.Online = false
					leftPeers = append(leftPeers, leftPeer{nodeID: id, humanNick: p.HumanNick})
				}
			}
		}
	}

	callbacks := struct {
		add func(Participant)
		rm  func(nodeID, humanNick string)
	}{h.onParticipantAdd, h.onParticipantRm}
	h.mu.Unlock()

	// Fire callbacks outside lock
	for _, np := range newPeers {
		if callbacks.add != nil {
			go callbacks.add(np)
		}
		// Proactively send our presence to the new peer
		go h.sendPresence(np)
	}
	for _, lp := range leftPeers {
		if callbacks.rm != nil {
			go callbacks.rm(lp.nodeID, lp.humanNick)
		}
	}
}

// HandlePresence processes an incoming presence announcement from a peer.
// This is called when a newly discovered peer sends us their participant info.
func (h *Hub) HandlePresence(p Participant) {
	h.mu.Lock()
	existing, ok := h.peers[p.NodeID]
	isNew := false
	if !ok {
		isNew = true
		cp := p
		cp.Online = true
		cp.LastSeen = time.Now().Unix()
		h.peers[p.NodeID] = &cp
	} else {
		existing.HumanNick = p.HumanNick
		existing.AgentNick = p.AgentNick
		existing.Mode = p.Mode
		existing.Endpoint = p.Endpoint
		existing.Online = true
		existing.LastSeen = time.Now().Unix()
	}
	callback := h.onParticipantAdd
	h.mu.Unlock()

	if isNew && callback != nil {
		go callback(p)
	}
}

// sendPresence POSTs our participant info to a peer so they learn our nick.
func (h *Hub) sendPresence(peer Participant) {
	self := h.SelfParticipant()
	url := strings.TrimRight(peer.Endpoint, "/") + "/lanchat/presence"

	data, err := json.Marshal(self)
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("X-API-Key", h.apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	// The response contains the peer's own presence — learn it too
	var peerInfo Participant
	if err := json.NewDecoder(resp.Body).Decode(&peerInfo); err == nil && peerInfo.NodeID != "" {
		h.mu.Lock()
		if existing, ok := h.peers[peerInfo.NodeID]; ok {
			existing.HumanNick = peerInfo.HumanNick
			existing.AgentNick = peerInfo.AgentNick
			existing.Mode = peerInfo.Mode
			existing.Online = true
			existing.LastSeen = time.Now().Unix()
		}
		h.mu.Unlock()
	}
}

// IsOnline returns whether a node is currently online.
func (h *Hub) IsOnline(nodeID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if nodeID == h.nodeID {
		return true
	}
	if p, ok := h.peers[nodeID]; ok {
		return p.Online
	}
	return false
}

// ---- Outbound ----

// SendBroadcast sends a message to all peers (human role only).
func (h *Hub) SendBroadcast(ctx context.Context, content string, attachments []Attachment) error {
	msg := h.newMessage(RoleHuman, h.humanNick, "", "", content, attachments)
	return h.deliverMessage(ctx, msg, true)
}

// SendDirect sends a targeted message to a specific node/role.
func (h *Hub) SendDirect(ctx context.Context, toNodeID, toRole, content string, attachments []Attachment) error {
	fromRole := RoleHuman
	fromNick := h.humanNick
	if toRole == RoleHuman && false {
		// Messages from agent role use agent nick
		fromRole = RoleAgent
		fromNick = h.agentNick
	}
	msg := h.newMessage(fromRole, fromNick, toNodeID, toRole, content, attachments)
	return h.deliverMessage(ctx, msg, false)
}

// SendAsAgent sends a message from the agent role (for agent responses to @agent messages).
func (h *Hub) SendAsAgent(ctx context.Context, toNodeID, toRole, content string) error {
	msg := h.newMessage(RoleAgent, h.agentNick, toNodeID, toRole, content, nil)
	return h.deliverMessage(ctx, msg, false)
}

func (h *Hub) newMessage(fromRole, fromNick, toNodeID, toRole, content string, attachments []Attachment) Message {
	// Populate attachment URLs from this node's endpoint
	if len(attachments) > 0 {
		h.mu.RLock()
		ep := h.endpoint
		h.mu.RUnlock()
		for i := range attachments {
			SetAttachmentURL(ep, &attachments[i])
		}
	}
	return Message{
		ID:          uuid.NewString(),
		FromNodeID:  h.nodeID,
		FromRole:    fromRole,
		FromNick:    fromNick,
		ToNodeID:    toNodeID,
		ToRole:      toRole,
		Content:     content,
		Attachments: attachments,
		Timestamp:   time.Now().UnixMilli(),
	}
}

func (h *Hub) deliverMessage(ctx context.Context, msg Message, broadcast bool) error {
	// Store locally
	h.mu.Lock()
	h.messages = append(h.messages, msg)
	if len(h.messages) > maxHistoryPerSession*2 {
		h.messages = h.messages[len(h.messages)-maxHistoryPerSession:]
	}
	h.mu.Unlock()

	if broadcast {
		// Send to all peers
		h.mu.RLock()
		peers := make([]Participant, 0, len(h.peers))
		for _, p := range h.peers {
			if p.Online {
				peers = append(peers, *p)
			}
		}
		h.mu.RUnlock()

		for _, peer := range peers {
			go h.postToPeer(ctx, peer.Endpoint, msg)
		}
		return nil
	}

	// Direct message — find the target peer
	h.mu.RLock()
	peer, ok := h.peers[msg.ToNodeID]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown peer: %s", msg.ToNodeID)
	}
	go h.postToPeer(ctx, peer.Endpoint, msg)
	return nil
}

func (h *Hub) postToPeer(ctx context.Context, endpoint string, msg Message) {
	url := strings.TrimRight(endpoint, "/") + "/lanchat/message"

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[lanchat] marshal error: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("X-API-Key", h.apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		log.Printf("[lanchat] POST to %s failed: %v", endpoint, err)
		return
	}
	resp.Body.Close()
}

func (h *Hub) broadcastNickChange(newNick string) {
	h.mu.RLock()
	change := NickChange{
		NodeID:    h.nodeID,
		HumanNick: newNick,
		AgentNick: AgentNick(newNick),
		Timestamp: time.Now().UnixMilli(),
	}
	peers := make([]Participant, 0, len(h.peers))
	for _, p := range h.peers {
		if p.Online {
			peers = append(peers, *p)
		}
	}
	h.mu.RUnlock()

	data, _ := json.Marshal(change)
	for _, peer := range peers {
		url := strings.TrimRight(peer.Endpoint, "/") + "/lanchat/nick"
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if h.apiKey != "" {
			req.Header.Set("X-API-Key", h.apiKey)
		}
		resp, err := h.httpClient.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
}

// ---- Inbound (called by HTTP handlers) ----

// HandleIncomingMessage processes a message received from a peer.
func (h *Hub) HandleIncomingMessage(msg Message) {
	h.mu.Lock()
	// Store message
	h.messages = append(h.messages, msg)
	if len(h.messages) > maxHistoryPerSession*2 {
		h.messages = h.messages[len(h.messages)-maxHistoryPerSession:]
	}

	// Check if this is an @agent direct message
	needsApproval := msg.IsDirectToAgent() && msg.ToNodeID == h.nodeID
	if needsApproval {
		pending := PendingAgentMsg{Message: msg, Received: time.Now()}
		h.pendingApproval = append(h.pendingApproval, pending)
	}

	callback := h.onMessage
	approvalCb := h.onApprovalReq
	h.mu.Unlock()

	if callback != nil {
		callback(msg)
	}

	// Send delivered receipt immediately
	go h.sendReceipt(msg, StatusDelivered, "")

	// If it's an @agent message, trigger approval callback
	if needsApproval && approvalCb != nil {
		approvalCb(PendingAgentMsg{Message: msg, Received: time.Now()})
	}
}

// HandleReceipt processes a receipt from a peer.
func (h *Hub) HandleReceipt(r Receipt) {
	h.mu.Lock()
	h.receipts[r.MessageID] = r
	callback := h.onReceipt
	h.mu.Unlock()

	if callback != nil {
		callback(r)
	}
}

// HandleNickChange updates a peer's nickname.
func (h *Hub) HandleNickChange(change NickChange) {
	h.mu.Lock()
	if peer, ok := h.peers[change.NodeID]; ok {
		peer.HumanNick = change.HumanNick
		peer.AgentNick = change.AgentNick
	}
	h.mu.Unlock()
}

// HandleParticipantQuery returns this node's participant info.
func (h *Hub) HandleParticipantQuery() Participant {
	return h.SelfParticipant()
}

// ---- Approval flow ----

// ApproveMessage removes a pending approval and returns the message for
// injection into the agent loop. Also sends "processing" receipt.
func (h *Hub) ApproveMessage(messageID string) (*Message, error) {
	h.mu.Lock()
	for i, pending := range h.pendingApproval {
		if pending.Message.ID == messageID {
			h.pendingApproval = append(h.pendingApproval[:i], h.pendingApproval[i+1:]...)
			msg := pending.Message
			h.mu.Unlock()

			// Send processing receipt
			go h.sendReceipt(msg, StatusProcessing, "")
			return &msg, nil
		}
	}
	h.mu.Unlock()
	return nil, fmt.Errorf("message %s not found in pending approvals", messageID)
}

// RejectMessage removes a pending approval and sends "rejected" receipt.
func (h *Hub) RejectMessage(messageID, reason string) error {
	h.mu.Lock()
	for i, pending := range h.pendingApproval {
		if pending.Message.ID == messageID {
			h.pendingApproval = append(h.pendingApproval[:i], h.pendingApproval[i+1:]...)
			msg := pending.Message
			h.mu.Unlock()

			go h.sendReceipt(msg, StatusRejected, reason)
			return nil
		}
	}
	h.mu.Unlock()
	return fmt.Errorf("message %s not found in pending approvals", messageID)
}

// GetReceipt returns the latest receipt for a message ID.
func (h *Hub) GetReceipt(messageID string) (Receipt, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.receipts[messageID]
	return r, ok
}

func (h *Hub) sendReceipt(originalMsg Message, status, reason string) {
	r := Receipt{
		MessageID:  originalMsg.ID,
		Status:     status,
		FromNodeID: h.nodeID,
		Timestamp:  time.Now().UnixMilli(),
		Reason:     reason,
	}

	// Find sender's endpoint
	h.mu.RLock()
	peer, ok := h.peers[originalMsg.FromNodeID]
	h.mu.RUnlock()
	if !ok {
		return
	}

	url := strings.TrimRight(peer.Endpoint, "/") + "/lanchat/receipt"
	data, _ := json.Marshal(r)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("X-API-Key", h.apiKey)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// LoadHistory loads recent messages for a session from the store.
func (h *Hub) LoadHistory(sessionID string) ([]Message, error) {
	return h.store.LoadRecent(sessionID, maxHistoryPerSession)
}

// PersistMessage saves a message to the per-session store.
func (h *Hub) PersistMessage(sessionID string, msg Message) error {
	return h.store.Append(sessionID, msg)
}
