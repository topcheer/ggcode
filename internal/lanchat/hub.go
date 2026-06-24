package lanchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
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
	store     *Store
	sessionID string

	// callbacks for UI integration
	onMessage        func(Message)
	onReceipt        func(Receipt)
	onParticipantAdd func(Participant)
	onParticipantRm  func(nodeID, humanNick string) // nodeID + nick for display
	onApprovalReq    func(PendingAgentMsg)

	// approval policies: key = peer's human_nick (stable across restarts)
	approvalPolicies map[string]string // "always" | "never" | ""(ask)

	// onAutoApprove is called when a message is auto-approved (by policy or daemon mode)
	// so the host can inject it into the agent loop.
	onAutoApprove func(Message)

	// HTTP client for peer communication
	httpClient *http.Client
}

// PendingAgentMsg is an incoming @agent direct message awaiting host approval.
type PendingAgentMsg struct {
	Message  Message   `json:"message"`
	Received time.Time `json:"received"`
}

// globalNickDir extracts the global lanchat dir from a session-scoped path.
// sessionDir = <baseDir>/sessions/<sessionID>
// returns <baseDir>, or "" if the path doesn't match the expected pattern.
func globalNickDir(sessionDir string) string {
	idx := strings.LastIndex(sessionDir, "/sessions/")
	if idx < 0 {
		return ""
	}
	return sessionDir[:idx]
}

// NewHub creates a new chat hub.
// Nickname resolution:
//  1. If a global nick exists at store.dir/lanchat-nick, use it as the default
//     (this is the user's preferred nick from a previous session).
//  2. If not, generate a random nick and save it globally.
//
// Per-session override happens later via SetSessionID.
func NewHub(nodeID, mode, endpoint, apiKey string, store *Store) *Hub {
	nick := RandomNick()
	globalExisted := false
	if persisted, err := LoadNick(store.dir); err == nil && persisted != "" {
		nick = persisted
		globalExisted = true
	}
	if !globalExisted {
		_ = SaveNick(store.dir, nick)
	}

	// Load persisted approval policies (keyed by peer nick)
	policies, _ := LoadApprovalPolicies(store.dir)

	return &Hub{
		nodeID:           nodeID,
		humanNick:        nick,
		agentNick:        AgentNick(nick),
		mode:             mode,
		endpoint:         endpoint,
		apiKey:           apiKey,
		peers:            make(map[string]*Participant),
		receipts:         make(map[string]Receipt),
		store:            store,
		approvalPolicies: policies,
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
	h.sessionID = sessionID
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

// APIKey returns the A2A API key used for peer authentication.
func (h *Hub) APIKey() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.apiKey
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
// and persists locally to both session-scoped and global paths.
func (h *Hub) SetNick(nick string) error {
	h.mu.Lock()
	h.humanNick = nick
	h.agentNick = AgentNick(nick)
	sessionDir := h.store.dir
	h.mu.Unlock()

	// Persist to session-scoped path
	if err := SaveNick(sessionDir, nick); err != nil {
		debug.Log("lanchat", "failed to persist session nick: %v", err)
	}

	// Also persist to global path so new instances get a sensible default.
	// We derive the global dir by stripping the /sessions/<id> suffix.
	globalDir := globalNickDir(sessionDir)
	if globalDir != "" && globalDir != sessionDir {
		if err := SaveNick(globalDir, nick); err != nil {
			debug.Log("lanchat", "failed to persist global nick: %v", err)
		}
	}

	// Broadcast nick change to peers
	safego.Go("lanchat.broadcastNickChange", func() { h.broadcastNickChange(nick) })
	return nil
}

// UpdatePeers synchronizes the peer list from A2A registry discovery results.
// Only verified peers (successful presence exchange) trigger join/leave callbacks.
// Unverified peers are tracked internally but never surfaced to the UI.
func (h *Hub) UpdatePeers(participants []Participant) {
	h.mu.Lock()

	seen := make(map[string]bool)
	var newPeers []Participant
	var emptyNickPeers []Participant // existing online peers with unknown nick
	var stalePeers []Participant     // peers whose LastSeen is older than heartbeatThreshold

	for _, p := range participants {
		if p.NodeID == h.nodeID {
			continue // skip self
		}
		seen[p.NodeID] = true
		existing, ok := h.peers[p.NodeID]
		if !ok {
			// New peer — start as unverified (HumanNick empty).
			// Will be promoted to verified after presence exchange.
			cp := p
			cp.Online = true
			cp.LastSeen = time.Now().Unix()
			if cp.HumanNick != "" {
				cp.notifiedJoin = true // will fire callback below
			}
			h.peers[p.NodeID] = &cp
			newPeers = append(newPeers, cp)
		} else {
			// Update existing peer metadata — but DON'T touch Online status.
			// Online is driven entirely by presence/LastSeen, NOT by mDNS
			// discovery. mDNS proves the process is alive, but lanchat
			// responsiveness is tracked via LastSeen. If we set Online=true
			// here, the stale check below immediately sets it back to false,
			// causing an endless join/leave cycle every tick.
			//
			// Also DON'T overwrite nicks with empty values. A2A discovery
			// doesn't carry lanchat nicks, so p.HumanNick/p.AgentNick are
			// usually "". We must preserve nicks learned from presence.
			if p.HumanNick != "" {
				existing.HumanNick = p.HumanNick
			}
			if p.AgentNick != "" {
				existing.AgentNick = p.AgentNick
			}
			existing.Mode = p.Mode
			existing.Endpoint = p.Endpoint
			// Only probe peers that are currently online (or were never seen).
			// Offline peers that reappear via mDNS will be re-probed by the
			// stale/heartbeat logic below.
			if existing.Online {
				if time.Since(time.Unix(existing.LastSeen, 0)) > presenceHeartbeat {
					stalePeers = append(stalePeers, *existing)
				}
				if existing.HumanNick == "" {
					emptyNickPeers = append(emptyNickPeers, *existing)
				}
			} else {
				// Peer is offline but re-discovered via mDNS — probe it.
				stalePeers = append(stalePeers, *existing)
			}
		}
	}

	// Mark peers offline — based SOLELY on LastSeen (presence heartbeat).
	// Do NOT use mDNS discovery absence (!seen[id]) as an offline signal:
	// mDNS results jitter (a peer can briefly disappear for one tick then
	// reappear), which causes endless online→offline→online flapping.
	//
	// Deletion: a peer is removed from the map only when BOTH conditions hold:
	//   1. It has been offline (LastSeen stale beyond ageOffline)
	//   2. It is no longer in the current mDNS discovery results
	// This prevents stale entries (from restarted processes with new nodeIDs)
	// from accumulating forever, while tolerating transient mDNS jitter.
	type leftPeer struct {
		nodeID, humanNick string
	}
	var leftPeers []leftPeer
	for id, p := range h.peers {
		isStale := time.Since(time.Unix(p.LastSeen, 0)) > ageOffline
		if isStale && p.Online {
			p.Online = false
			// Only notify offline for peers we previously announced as online
			// (verified via presence exchange). Unverified peers are silently removed.
			if p.notifiedJoin && p.HumanNick != "" {
				leftPeers = append(leftPeers, leftPeer{nodeID: id, humanNick: p.HumanNick})
			}
		}
		// Delete peers that are offline AND no longer discovered via mDNS.
		// This cleans up stale nodeIDs from restarted processes while
		// tolerating mDNS jitter for peers that are still alive.
		if !seen[id] && (!p.Online || isStale) {
			delete(h.peers, id)
		}
	}

	callbacks := struct {
		add func(Participant)
		rm  func(nodeID, humanNick string)
	}{h.onParticipantAdd, h.onParticipantRm}
	h.mu.Unlock()

	// Fire callbacks outside lock — but only for peers whose nick we
	// already know (from a prior presence exchange). Peers discovered
	// via A2A registry have empty nicks; we sendPresence to learn them,
	// and HandlePresence will fire the callback once the nick arrives.
	for _, np := range newPeers {
		if np.HumanNick != "" && callbacks.add != nil {
			safego.Go("lanchat.participantAdd", func() { callbacks.add(np) })
		}
		// Proactively send our presence to the new peer
		safego.Go("lanchat.sendPresence", func() { h.sendPresence(np) })
	}
	// Also retry presence for existing online peers whose nick we still
	// don't know (presence exchange may have failed on a previous tick).
	for _, ep := range emptyNickPeers {
		safego.Go("lanchat.sendPresence", func() { h.sendPresence(ep) })
	}
	// Heartbeat: re-probe peers whose LastSeen hasn't been updated recently.
	for _, sp := range stalePeers {
		safego.Go("lanchat.sendPresence", func() { h.sendPresence(sp) })
	}
	for _, lp := range leftPeers {
		if callbacks.rm != nil {
			safego.Go("lanchat.participantRm", func() { callbacks.rm(lp.nodeID, lp.humanNick) })
		}
	}

	// Check for nick conflicts with peers (only once — when we first learn
	// peer nicks). If a peer shares our nick, auto-resolve by appending a suffix.
	h.resolveNickConflict()
}

// resolveNickConflict checks if any online peer has the same human nick as us.
// If so, auto-renames to a suffixed variant (e.g. "CleverOtter2").
// Only fires once per conflict to avoid loops.
func (h *Hub) resolveNickConflict() {
	h.mu.Lock()
	myNick := h.humanNick
	conflict := false
	for _, p := range h.peers {
		if p.Online && p.HumanNick == myNick {
			conflict = true
			break
		}
	}
	if !conflict {
		h.mu.Unlock()
		return
	}
	// Collect all taken nicks
	taken := make(map[string]bool)
	for _, p := range h.peers {
		if p.HumanNick != "" {
			taken[p.HumanNick] = true
		}
	}
	newNick := ResolveNickConflict(myNick, taken)
	h.humanNick = newNick
	h.agentNick = AgentNick(newNick)
	sessionDir := h.store.dir
	h.mu.Unlock()

	debug.Log("lanchat", "nick conflict: %s -> %s", myNick, newNick)
	_ = SaveNick(sessionDir, newNick)
	safego.Go("lanchat.broadcastNickChange", func() { h.broadcastNickChange(newNick) })
}

// HandlePresence processes an incoming presence announcement from a peer.
// This is called when a newly discovered peer sends us their participant info.
func (h *Hub) HandlePresence(p Participant) {
	h.mu.Lock()
	existing, ok := h.peers[p.NodeID]
	needCallback := false
	if !ok {
		// Truly new peer — create and fire callback
		cp := p
		cp.Online = true
		cp.LastSeen = time.Now().Unix()
		h.peers[p.NodeID] = &cp
		needCallback = p.HumanNick != ""
	} else {
		// Update existing — protect learned nicks from empty overwrites
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
		// If we previously didn't know the nick but now we do, fire callback
		// so the join notification shows the real name.
		if existing.HumanNick != "" && !existing.notifiedJoin {
			needCallback = true
			existing.notifiedJoin = true
		}
	}
	callback := h.onParticipantAdd
	participant := p
	if existing != nil {
		participant.HumanNick = existing.HumanNick
		participant.AgentNick = existing.AgentNick
		participant.Endpoint = existing.Endpoint
	}
	h.mu.Unlock()

	if needCallback && callback != nil {
		safego.Go("lanchat.presenceCallback", func() { callback(participant) })
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
		var cb func(Participant)
		var participant Participant
		h.mu.Lock()
		if existing, ok := h.peers[peerInfo.NodeID]; ok {
			wasOffline := !existing.Online
			// Guard against empty overwrites
			if peerInfo.HumanNick != "" {
				existing.HumanNick = peerInfo.HumanNick
			}
			if peerInfo.AgentNick != "" {
				existing.AgentNick = peerInfo.AgentNick
			}
			existing.Mode = peerInfo.Mode
			existing.Online = true
			existing.LastSeen = time.Now().Unix()
			participant = *existing
			// Fire join callback when nick is first learned OR when peer
			// recovers from offline (was offline, now presence succeeded).
			if existing.HumanNick != "" && (!existing.notifiedJoin || wasOffline) {
				existing.notifiedJoin = true
				cb = h.onParticipantAdd
			}
		}
		h.mu.Unlock()
		if cb != nil {
			safego.Go("lanchat.presenceCallback", func() { cb(participant) })
		}
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
	h.mu.RLock()
	nick := h.humanNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleHuman, nick, "", "", content, attachments)
	return h.deliverMessage(ctx, msg, true)
}

// SendDirect sends a targeted message to a specific node/role.
func (h *Hub) SendDirect(ctx context.Context, toNodeID, toRole, content string, attachments []Attachment) error {
	h.mu.RLock()
	nick := h.humanNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleHuman, nick, toNodeID, toRole, content, attachments)
	return h.deliverMessage(ctx, msg, false)
}

// SendAsAgent sends a message from the agent role (for agent responses to @agent messages).
func (h *Hub) SendAsAgent(ctx context.Context, toNodeID, toRole, content string) error {
	h.mu.RLock()
	nick := h.agentNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleAgent, nick, toNodeID, toRole, content, nil)
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
	// Persist to session store if available
	h.persistMessage(msg)
	h.mu.Unlock()

	if broadcast {
		// Send to all peers (fire-and-forget for broadcasts)
		h.mu.RLock()
		peers := make([]Participant, 0, len(h.peers))
		for _, p := range h.peers {
			if p.Online {
				peers = append(peers, *p)
			}
		}
		h.mu.RUnlock()

		for _, peer := range peers {
			safego.Go("lanchat.postToPeer", func() { h.postToPeerWithRetry(ctx, peer.Endpoint, msg, 1) })
		}
		return nil
	}

	// Direct message — find the target peer and deliver synchronously
	// so the caller learns about delivery failures immediately.
	h.mu.RLock()
	peer, ok := h.peers[msg.ToNodeID]
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown peer: %s", msg.ToNodeID)
	}
	return h.postToPeerWithRetry(ctx, peer.Endpoint, msg, 2)
}

// postToPeerWithRetry sends a message to a peer, retrying up to maxRetries
// times on transient failures. Returns an error if all attempts fail.
func (h *Hub) postToPeerWithRetry(ctx context.Context, endpoint string, msg Message, maxRetries int) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		if err := h.postToPeer(ctx, endpoint, msg); err != nil {
			lastErr = err
			debug.Log("lanchat", "POST to %s failed (attempt %d/%d): %v", endpoint, attempt+1, maxRetries, err)
			continue
		}
		return nil
	}
	return lastErr
}

func (h *Hub) postToPeer(ctx context.Context, endpoint string, msg Message) error {
	url := strings.TrimRight(endpoint, "/") + "/lanchat/message"

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("X-API-Key", h.apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("peer returned %d for %s", resp.StatusCode, endpoint)
	}
	return nil
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
		safego.Go("lanchat.nickChangePeer", func() {
			url := strings.TrimRight(peer.Endpoint, "/") + "/lanchat/nick"
			req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/json")
			if h.apiKey != "" {
				req.Header.Set("X-API-Key", h.apiKey)
			}
			resp, err := h.httpClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
		})
	}
}

// ---- Inbound (called by HTTP handlers) ----

// persistMessage saves a message to the per-session store if both store and
// sessionID are set. Must be called with h.mu held.
func (h *Hub) persistMessage(msg Message) {
	if h.store != nil && h.sessionID != "" {
		if err := h.store.Append(h.sessionID, msg); err != nil {
			debug.Log("lanchat", "persist message: %v", err)
		}
	}
}

// HandleIncomingMessage processes a message received from a peer.
func (h *Hub) HandleIncomingMessage(msg Message) {
	h.mu.Lock()
	// Store message
	h.messages = append(h.messages, msg)
	if len(h.messages) > maxHistoryPerSession*2 {
		h.messages = h.messages[len(h.messages)-maxHistoryPerSession:]
	}
	// Persist to session store if available
	h.persistMessage(msg)

	// Check if this is an @agent direct message
	needsApproval := msg.IsDirectToAgent() && msg.ToNodeID == h.nodeID

	// Determine approval action based on policy or daemon mode
	autoApproved := false
	autoRejected := false
	if needsApproval {
		policy := h.approvalPolicies[msg.FromNick]
		if policy == "always" || h.mode == "daemon" {
			// Auto-approve: daemon mode defaults to approve, or explicit policy
			autoApproved = true
		} else if policy == "never" {
			autoRejected = true
		}
	}

	// Only queue for manual approval if not auto-handled
	if needsApproval && !autoApproved && !autoRejected {
		pending := PendingAgentMsg{Message: msg, Received: time.Now()}
		h.pendingApproval = append(h.pendingApproval, pending)
	}

	autoApproveCb := h.onAutoApprove
	callback := h.onMessage
	approvalCb := h.onApprovalReq
	h.mu.Unlock()

	// Fire callbacks asynchronously so we never block the HTTP handler.
	if callback != nil {
		safego.Go("lanchat.messageCallback", func() { callback(msg) })
	}

	// Send delivered receipt immediately
	safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusDelivered, "") })

	// Handle auto-approve or auto-reject
	if autoApproved {
		// Auto-approved: approved → processing → (agent runs) → completed
		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusApproved, "") })
		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusProcessing, "") })
		if autoApproveCb != nil {
			safego.Go("lanchat.autoApprove", func() {
				autoApproveCb(msg)
				// After agent run finishes, send completed receipt
				safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusCompleted, "") })
			})
		}
	} else if autoRejected {
		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusRejected, "auto-rejected by policy") })
	} else if needsApproval {
		// Manual approval needed — send pending receipt, then trigger approval callback
		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusPending, "") })
		if approvalCb != nil {
			safego.Go("lanchat.approvalCallback", func() { approvalCb(PendingAgentMsg{Message: msg, Received: time.Now()}) })
		}
	}
}

// HandleReceipt processes a receipt from a peer.
func (h *Hub) HandleReceipt(r Receipt) {
	h.mu.Lock()
	h.receipts[r.MessageID] = r
	callback := h.onReceipt
	h.mu.Unlock()

	// Fire callback asynchronously to avoid blocking the HTTP handler.
	if callback != nil {
		safego.Go("lanchat.receiptCallback", func() { callback(r) })
	}
}

// HandleNickChange updates a peer's nickname.
func (h *Hub) HandleNickChange(change NickChange) {
	h.mu.Lock()
	if peer, ok := h.peers[change.NodeID]; ok {
		if change.HumanNick != "" {
			peer.HumanNick = change.HumanNick
		}
		if change.AgentNick != "" {
			peer.AgentNick = change.AgentNick
		}
	}
	h.mu.Unlock()
}

// HandleParticipantQuery returns this node's participant info.
func (h *Hub) HandleParticipantQuery() Participant {
	return h.SelfParticipant()
}

// ---- Approval flow ----

// ApproveMessage removes a pending approval and returns the message for
// injection into the agent loop. Sends "approved" then "processing" receipts.
// The caller must call NotifyAgentComplete(messageID) when the agent run finishes.
func (h *Hub) ApproveMessage(messageID string) (*Message, error) {
	h.mu.Lock()
	for i, pending := range h.pendingApproval {
		if pending.Message.ID == messageID {
			h.pendingApproval = append(h.pendingApproval[:i], h.pendingApproval[i+1:]...)
			msg := pending.Message
			h.mu.Unlock()

			// Send approved then processing receipts
			safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusApproved, "") })
			safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusProcessing, "") })
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

			safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusRejected, reason) })
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
		ToNodeID:   originalMsg.FromNodeID, // route back to original sender
		ToRole:     originalMsg.FromRole,   // original sender's role
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

// ---- Approval Policy ----

// SetApprovalPolicy sets the approval policy for a peer (by nick) and persists it.
// policy: "always" (auto-approve), "never" (auto-reject), "" (ask).
func (h *Hub) SetApprovalPolicy(peerNick string, policy string) {
	h.mu.Lock()
	if h.approvalPolicies == nil {
		h.approvalPolicies = make(map[string]string)
	}
	if policy == "" {
		delete(h.approvalPolicies, peerNick)
	} else {
		h.approvalPolicies[peerNick] = policy
	}
	dir := ""
	if h.store != nil {
		dir = h.store.dir
	}
	h.mu.Unlock()

	// Persist outside lock
	if dir != "" {
		h.mu.RLock()
		policies := make(map[string]string, len(h.approvalPolicies))
		for k, v := range h.approvalPolicies {
			policies[k] = v
		}
		h.mu.RUnlock()
		_ = SaveApprovalPolicies(dir, policies)
	}
}

// GetApprovalPolicies returns a copy of all approval policies.
func (h *Hub) GetApprovalPolicies() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make(map[string]string, len(h.approvalPolicies))
	for k, v := range h.approvalPolicies {
		result[k] = v
	}
	return result
}

// SetOnAutoApprove registers the callback invoked when a message is auto-approved
// (by policy or daemon mode). The host uses this to inject the message into the agent loop.
func (h *Hub) SetOnAutoApprove(cb func(Message)) {
	h.mu.Lock()
	h.onAutoApprove = cb
	h.mu.Unlock()
}

// NotifyAgentComplete sends a "completed" receipt to the sender of a manually-approved
// message after the agent run finishes. For auto-approved messages, the receipt is sent
// automatically inside the onAutoApprove callback wrapper.
func (h *Hub) NotifyAgentComplete(messageID string) {
	h.mu.RLock()
	// Find the message in history to get sender info
	var msg *Message
	for i := range h.messages {
		if h.messages[i].ID == messageID {
			msg = &h.messages[i]
			break
		}
	}
	h.mu.RUnlock()
	if msg == nil {
		return
	}
	safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(*msg, StatusCompleted, "") })
}
