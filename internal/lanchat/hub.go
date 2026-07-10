package lanchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
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
	role      string // user-defined role, e.g. "frontend", "backend", "devops"
	team      string // user-defined team, e.g. "platform", "mobile" (default: "dev-team")
	mode      string // "tui", "gui", "daemon"
	endpoint  string // this node's HTTP endpoint (http://ip:port)
	apiKey    string // A2A API key for peer auth

	// workspace metadata (populated by caller, shared via presence)
	workspace   string
	projectName string
	languages   []string
	frameworks  []string
	hasGit      bool
	hasTests    bool

	// agentBusy tracks whether the local agent is currently running.
	// Propagated to peers via presence exchange.
	agentBusy bool

	// peers discovered via A2A registry callbacks
	peers map[string]*Participant // key = NodeID

	// archive is a ring buffer of peers that were deleted after extended
	// unreachability. Used to correlate returning peers with their previous
	// identity (new NodeID, same team/role/nicks). FIFO, max 500 entries.
	archive []ArchivedPeer

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
	onNickChange     func(nodeID, oldNick, newNick string)

	// approval policies: key = peer's human_nick (stable across restarts)
	approvalPolicies map[string]string // "always" | "never" | ""(ask)

	// notifiedNicks tracks human nicks that have already been announced as
	// "is online" to the UI. This prevents duplicate notifications when the
	// same user restarts their process (new NodeID, same nick) or when
	// presence heartbeat flapping causes repeated join/leave cycles.
	// Keyed by HumanNick because that's the identity the user cares about.
	notifiedNicks map[string]bool

	// onAutoApprove is called when a message is auto-approved (by policy or daemon mode)
	// so the host can inject it into the agent loop.
	onAutoApprove func(Message)

	// onInboundDM is called when a non-broadcast message arrives,
	// allowing the rate limiter to reset the DM cooldown for the sender.
	onInboundDM func(fromNodeID string)

	// HTTP client for peer communication
	httpClient *http.Client

	// UDP transport for fallback when TCP is blocked
	udpTransport *UDPTransport

	// peerHealthMap tracks TCP/UDP availability per peer nodeID
	peerHealthMap map[string]*peerHealth

	// ackTracker tracks received ACKs for unicast UDP messages
	ackTracker sync.Map // msgID → bool (received)
}

// PendingAgentMsg is an incoming @agent direct message awaiting host approval.
type PendingAgentMsg struct {
	Message  Message   `json:"message"`
	Received time.Time `json:"received"`
}

// WorkspaceMeta describes the workspace for presence exchange.
// All fields are optional; empty fields are omitted from presence.
type WorkspaceMeta struct {
	Workspace   string   `json:"workspace"`
	ProjectName string   `json:"project_name"`
	Languages   []string `json:"languages"`
	Frameworks  []string `json:"frameworks"`
	HasGit      bool     `json:"has_git"`
	HasTests    bool     `json:"has_tests"`
}

// NewHub creates a new chat hub with a random nickname.
// Each session gets its own random nick — there is no global nick.
// SetSessionID persists the nick to the session directory when called.
func NewHub(nodeID, mode, endpoint, apiKey string, store *Store, ws WorkspaceMeta) *Hub {
	baseNick := RandomNick()
	nick := baseNick + "_" + DefaultRole

	// Load persisted approval policies (keyed by peer nick)
	policies, _ := LoadApprovalPolicies(store.dir)

	return &Hub{
		nodeID:           nodeID,
		humanNick:        nick,
		agentNick:        AgentNick(nick),
		role:             DefaultRole,
		team:             DefaultTeam,
		mode:             mode,
		endpoint:         endpoint,
		apiKey:           apiKey,
		workspace:        ws.Workspace,
		projectName:      ws.ProjectName,
		languages:        ws.Languages,
		frameworks:       ws.Frameworks,
		hasGit:           ws.HasGit,
		hasTests:         ws.HasTests,
		peers:            make(map[string]*Participant),
		receipts:         make(map[string]Receipt),
		store:            store,
		approvalPolicies: policies,
		notifiedNicks:    make(map[string]bool),
		peerHealthMap:    make(map[string]*peerHealth),
		httpClient: &http.Client{
			Timeout: 2 * time.Second, // LAN: if TCP doesn't connect in 2s, it's down
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
	// Load session-specific nick if previously persisted.
	if persisted, err := LoadNick(h.store.dir); err == nil && persisted != "" {
		h.humanNick = persisted
		h.agentNick = AgentNick(persisted)
	} else {
		// No session nick yet — persist the current random nick.
		_ = SaveNick(h.store.dir, h.humanNick)
	}
	// Load session-specific role if previously persisted.
	if persisted, err := LoadRole(h.store.dir); err == nil && persisted != "" {
		h.role = persisted
	} else {
		_ = SaveRole(h.store.dir, h.role)
	}
	// Load session-specific team if previously persisted.
	if persisted, err := LoadTeam(h.store.dir); err == nil && persisted != "" {
		h.team = persisted
	} else {
		_ = SaveTeam(h.store.dir, h.team)
	}
	// Load persisted messages into memory so they survive across restarts.
	if h.store != nil {
		if msgs, err := h.store.LoadRecent(sessionID, maxHistoryPerSession); err == nil {
			h.messages = msgs
		}
	}
	h.mu.Unlock()

	// Re-broadcast presence so peers learn our updated identity (nick/role/team).
	// SetSessionID may change all three when switching sessions.
	for _, peer := range h.Participants() {
		if peer.NodeID == h.nodeID || !peer.Online {
			continue
		}
		p := peer
		safego.Go("lanchat.presenceUpdate", func() { h.sendPresence(p) })
	}
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
	onNickChange func(nodeID, oldNick, newNick string),
) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onMessage = onMessage
	h.onReceipt = onReceipt
	h.onParticipantAdd = onParticipantAdd
	h.onParticipantRm = onParticipantRm
	h.onApprovalReq = onApprovalReq
	h.onNickChange = onNickChange
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

// Role returns this node's user-defined role.
func (h *Hub) Role() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.role
}

// Team returns this node's user-defined team.
func (h *Hub) Team() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.team
}

// SetWorkspaceMeta updates the workspace metadata for this node.
// Call this when switching workspaces so that subsequent presence
// exchanges announce the new project info.
func (h *Hub) SetWorkspaceMeta(ws WorkspaceMeta) {
	h.mu.Lock()
	h.workspace = ws.Workspace
	h.projectName = ws.ProjectName
	h.languages = ws.Languages
	h.frameworks = ws.Frameworks
	h.hasGit = ws.HasGit
	h.hasTests = ws.HasTests
	h.mu.Unlock()
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
		NodeID:      h.nodeID,
		HumanNick:   h.humanNick,
		AgentNick:   h.agentNick,
		Mode:        h.mode,
		Endpoint:    h.endpoint,
		Role:        h.role,
		Team:        h.team,
		Online:      true,
		LastSeen:    time.Now().Unix(),
		Workspace:   h.workspace,
		ProjectName: h.projectName,
		Languages:   h.languages,
		Frameworks:  h.frameworks,
		HasGit:      h.hasGit,
		HasTests:    h.hasTests,
		AgentBusy:   h.agentBusy,
	}
}

// SetAgentBusy updates the local agent's busy state and broadcasts presence
// to all online peers so they know whether this node's agent is available.
func (h *Hub) SetAgentBusy(busy bool) {
	h.mu.Lock()
	if h.agentBusy == busy {
		h.mu.Unlock()
		return
	}
	h.agentBusy = busy
	peers := make([]Participant, 0, len(h.peers))
	for _, p := range h.peers {
		if p.Online {
			peers = append(peers, *p)
		}
	}
	h.mu.Unlock()

	// Notify peers of the state change
	for _, peer := range peers {
		safego.Go("lanchat.agentBusyPresence", func() { h.sendPresence(peer) })
	}
}

// shouldNotifyJoin returns true if we should fire the "is online" callback
// for this nick. It deduplicates by nick so that restarts (new NodeID,
// same nick) and heartbeat flapping don't produce duplicate notifications.
// Caller must hold h.mu.
func (h *Hub) shouldNotifyJoin(nick string) bool {
	nick = strings.TrimSpace(nick)
	if nick == "" {
		return false
	}
	if h.notifiedNicks[nick] {
		return false
	}
	h.notifiedNicks[nick] = true
	return true
}

// shouldNotifyLeave returns true if we should fire the "went offline" callback
// for this nick. It suppresses the notification when another online peer with
// the same nick still exists (e.g. user has two instances, one restarted).
// Caller must hold h.mu.
func (h *Hub) shouldNotifyLeave(nick, excludeNodeID string) bool {
	nick = strings.TrimSpace(nick)
	if nick == "" {
		return false
	}
	for id, p := range h.peers {
		if id == excludeNodeID {
			continue
		}
		if p.Online && strings.TrimSpace(p.HumanNick) == nick {
			return false // another instance with same nick is still online
		}
	}
	// Don't delete from notifiedNicks here — that would allow a duplicate
	// "is online" notification when the peer recovers. notifiedNicks is
	// cleared only when the peer is deleted from the map after peerDeleteAfter.
	return true
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
		Role:      h.role,
		Team:      h.team,
		Online:    true,
		LastSeen:  time.Now().Unix(),
	}}

	for _, p := range h.peers {
		result = append(result, *p)
	}
	return result
}

// archivePeer adds a snapshot of the participant to the archive ring
// buffer. If the archive is full (maxArchiveEntries), the oldest entry
// is evicted (FIFO). Must be called with h.mu held.
func (h *Hub) archivePeer(p *Participant) {
	entry := ArchivedPeer{
		NodeID:      p.NodeID,
		HumanNick:   p.HumanNick,
		AgentNick:   p.AgentNick,
		Role:        p.Role,
		Team:        p.Team,
		Workspace:   p.Workspace,
		ProjectName: p.ProjectName,
		Languages:   p.Languages,
		LastSeen:    p.LastSeen,
		ArchivedAt:  time.Now().Unix(),
	}
	h.archive = append(h.archive, entry)
	if len(h.archive) > maxArchiveEntries {
		h.archive = h.archive[len(h.archive)-maxArchiveEntries:]
	}
	debug.Log("lanchat", "archived peer %s (%s)", p.NodeID, p.HumanNick)
}

// Archive returns a copy of the archived peer snapshots (FIFO order).
func (h *Hub) Archive() []ArchivedPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]ArchivedPeer, len(h.archive))
	copy(result, h.archive)
	return result
}

// LookupArchiveByNodeID searches the archive for a peer with the given
// node_id. Returns nil if not found.
func (h *Hub) LookupArchiveByNodeID(nodeID string) *ArchivedPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for i := range h.archive {
		if h.archive[i].NodeID == nodeID {
			ap := h.archive[i]
			return &ap
		}
	}
	return nil
}

// LookupArchiveByTeamRole searches the archive for peers matching the
// given team AND role. Returns the most recent match (last added), or
// nil if none found.
func (h *Hub) LookupArchiveByTeamRole(team, role string) *ArchivedPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for i := len(h.archive) - 1; i >= 0; i-- {
		if h.archive[i].Team == team && h.archive[i].Role == role {
			ap := h.archive[i]
			return &ap
		}
	}
	return nil
}

// LookupArchiveByNick searches the archive for a peer with the given
// human_nick or agent_nick (case-insensitive). Returns the most recent
// match, or nil if not found.
func (h *Hub) LookupArchiveByNick(nick string) *ArchivedPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	lower := strings.ToLower(nick)
	for i := len(h.archive) - 1; i >= 0; i-- {
		if strings.ToLower(h.archive[i].HumanNick) == lower ||
			strings.ToLower(h.archive[i].AgentNick) == lower {
			ap := h.archive[i]
			return &ap
		}
	}
	return nil
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

// SetNickRole changes the human nickname and role, updates agent nick,
// broadcasts to peers, and persists to the session-scoped path.
// The humanNick is composed as "nick_role" (e.g. "alice_dev") so that
// identity + role are self-evident and collision probability is low.
// On conflict, resolveNickConflict will produce "alice_dev2", "alice_dev3", etc.
// SetNickRoleTeam changes the human nickname, role, and team; updates agent nick,
// persists all three, and broadcasts the change to all peers.
func (h *Hub) SetNickRoleTeam(nick, role, team string) error {
	composite := nick + "_" + role
	h.mu.Lock()
	h.humanNick = composite
	h.agentNick = AgentNick(composite)
	h.role = role
	h.team = team
	sessionDir := h.store.dir
	h.mu.Unlock()

	// Persist nick, role, and team to session-scoped path
	if err := SaveNick(sessionDir, composite); err != nil {
		debug.Log("lanchat", "failed to persist session nick: %v", err)
	}
	if err := SaveRole(sessionDir, role); err != nil {
		debug.Log("lanchat", "failed to persist session role: %v", err)
	}
	if err := SaveTeam(sessionDir, team); err != nil {
		debug.Log("lanchat", "failed to persist session team: %v", err)
	}

	// Broadcast nick/role/team change to peers
	safego.Go("lanchat.broadcastNickChange", func() { h.broadcastNickChange(composite, role, team) })
	return nil
}

// SetNickRole is a backward-compatible wrapper that preserves the current team.
func (h *Hub) SetNickRole(nick, role string) error {
	h.mu.RLock()
	team := h.team
	h.mu.RUnlock()
	return h.SetNickRoleTeam(nick, role, team)
}

// UpdatePeers synchronizes the peer list from A2A registry discovery results.
// Only verified peers (successful presence exchange) trigger join/leave callbacks.
// Unverified peers are tracked internally but never surfaced to the UI.
func (h *Hub) UpdatePeers(participants []Participant) {
	h.mu.Lock()

	var newPeers []Participant

	// ── Loop 1: mDNS discovery — ONLY adds new peers and updates endpoint ──
	// mDNS is a stateless discovery service. Its results jitter between ticks:
	// a peer may appear, disappear, and reappear without any real state change.
	// Therefore mDNS is used SOLELY for initial peer discovery (learning the
	// NodeID + Endpoint of a new peer). It must NEVER influence liveness:
	//   - No LastSeen updates (liveness is HTTP presence only)
	//   - No Online status changes
	//   - No deletion based on mDNS absence
	for _, p := range participants {
		if p.NodeID == h.nodeID {
			continue // skip self
		}
		existing, ok := h.peers[p.NodeID]
		if !ok {
			// New peer discovered via mDNS — add with optimistic initial state.
			// Online=true is provisional; confirmed by sendPresence below.
			cp := p
			cp.Online = true
			cp.LastSeen = time.Now().Unix()
			h.peers[p.NodeID] = &cp
			newPeers = append(newPeers, cp)
		} else {
			// Existing peer — update endpoint metadata only.
			// Don't touch Online, LastSeen, or nicks.
			if p.Endpoint != "" {
				existing.Endpoint = p.Endpoint
			}
			if p.Mode != "" {
				existing.Mode = p.Mode
			}
		}
	}

	// ── Loop 2: Heartbeat probe — iterate ALL known peers ──
	// This is decoupled from mDNS. We probe every peer whose LastSeen is
	// older than presenceHeartbeat, regardless of whether mDNS sees them.
	// This ensures a peer missed by mDNS (but alive on HTTP) still gets
	// probed and stays online.
	var stalePeers []Participant     // peers needing a presence probe
	var emptyNickPeers []Participant // online peers with unknown nick
	for _, p := range h.peers {
		if p.HumanNick == "" {
			emptyNickPeers = append(emptyNickPeers, *p)
		}
		if time.Since(time.Unix(p.LastSeen, 0)) > presenceHeartbeat {
			stalePeers = append(stalePeers, *p)
		}
	}

	// ── Loop 3: Offline detection + delayed leave + deletion ──
	// All time-based, driven by LastSeen which is only updated by
	// successful HTTP presence exchange. mDNS has zero influence here.
	type leftPeer struct {
		nodeID, humanNick string
	}
	var leftPeers []leftPeer
	now := time.Now()
	for id, p := range h.peers {
		isStale := time.Since(time.Unix(p.LastSeen, 0)) > ageOffline
		if isStale && p.Online {
			p.Online = false
			p.lastOfflineTime = now.UnixNano()
			// Don't fire leave yet — wait for offlineNotifyDelay.
		}
		// Fire delayed leave notification: only after offlineNotifyDelay
		// has passed since the peer went offline, and we haven't already
		// notified. This absorbs brief offline blips.
		if !p.Online && p.lastOfflineTime > 0 && !p.notifiedLeave {
			if now.Sub(time.Unix(0, p.lastOfflineTime)) >= offlineNotifyDelay {
				p.notifiedLeave = true
				if p.notifiedJoin && p.HumanNick != "" && h.shouldNotifyLeave(p.HumanNick, id) {
					leftPeers = append(leftPeers, leftPeer{nodeID: id, humanNick: p.HumanNick})
				}
			}
		}
		// Delete peers only after extended unreachability (peerDeleteAfter).
		if time.Since(time.Unix(p.LastSeen, 0)) > peerDeleteAfter {
			if p.HumanNick != "" {
				delete(h.notifiedNicks, strings.TrimSpace(p.HumanNick))
			}
			// Archive the peer before deletion so long-running agents
			// can correlate it with a returning peer (new NodeID, same
			// team/role/nicks).
			h.archivePeer(p)
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
	// Retry presence for peers whose nick we still don't know.
	for _, ep := range emptyNickPeers {
		safego.Go("lanchat.sendPresence", func() { h.sendPresence(ep) })
	}
	// Heartbeat: probe all peers whose LastSeen is stale.
	// This is the SOLE liveness mechanism — independent of mDNS.
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
	safego.Go("lanchat.broadcastNickChange", func() { h.broadcastNickChange(newNick, h.role, h.team) })
}

// HandlePresence processes an incoming presence announcement from a peer.
// This is called when a newly discovered peer sends us their participant info.
func (h *Hub) HandlePresence(p Participant) {
	h.mu.Lock()
	existing, ok := h.peers[p.NodeID]
	needCallback := false
	if !ok {
		// Truly new peer — create and fire callback (if nick is genuinely new)
		cp := p
		cp.Online = true
		cp.LastSeen = time.Now().Unix()
		if cp.HumanNick != "" && h.shouldNotifyJoin(cp.HumanNick) {
			cp.notifiedJoin = true
			needCallback = true
		}
		h.peers[p.NodeID] = &cp
	} else {
		// Update existing — protect learned nicks and workspace info from empty overwrites
		if p.HumanNick != "" {
			existing.HumanNick = p.HumanNick
		}
		if p.AgentNick != "" {
			existing.AgentNick = p.AgentNick
		}
		if p.Role != "" {
			existing.Role = p.Role
		}
		if p.Team != "" {
			existing.Team = p.Team
		}
		existing.Mode = p.Mode
		existing.Endpoint = p.Endpoint
		existing.Online = true
		existing.LastSeen = time.Now().Unix()
		existing.notifiedLeave = false // reset: peer recovered, future offline can notify again
		// Update workspace metadata (always overwrite — workspace changes are meaningful)
		if p.Workspace != "" {
			existing.Workspace = p.Workspace
		}
		if p.ProjectName != "" {
			existing.ProjectName = p.ProjectName
		}
		if p.Languages != nil {
			existing.Languages = p.Languages
		}
		if p.Frameworks != nil {
			existing.Frameworks = p.Frameworks
		}
		if p.HasGit {
			existing.HasGit = true
		}
		if p.HasTests {
			existing.HasTests = true
		}
		// Update agent busy state (always overwrite — busy state changes are meaningful)
		existing.AgentBusy = p.AgentBusy
		// If we previously didn't know the nick but now we do, fire callback
		// so the join notification shows the real name.
		if existing.HumanNick != "" && !existing.notifiedJoin && h.shouldNotifyJoin(existing.HumanNick) {
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
// sendPresence sends our presence to a peer and acts as a TCP health probe.
// On success: marks TCP as up for this peer. On failure: marks TCP as down.
// This is the primary transport health mechanism — no separate probe needed.
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
	req.Header.Set("X-API-Key", communityKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		// TCP failed — mark peer TCP as down immediately
		h.recordTransportResult(peer.NodeID, "tcp", false)
		return
	}
	defer resp.Body.Close()

	// TCP succeeded — mark peer TCP as up
	h.recordTransportResult(peer.NodeID, "tcp", true)

	// The response contains the peer's own presence — learn it too
	var peerInfo Participant
	if err := json.NewDecoder(resp.Body).Decode(&peerInfo); err == nil && peerInfo.NodeID != "" {
		var cb func(Participant)
		var participant Participant
		h.mu.Lock()
		if existing, ok := h.peers[peerInfo.NodeID]; ok {
			// Guard against empty overwrites
			if peerInfo.HumanNick != "" {
				existing.HumanNick = peerInfo.HumanNick
			}
			if peerInfo.AgentNick != "" {
				existing.AgentNick = peerInfo.AgentNick
			}
			if peerInfo.Team != "" {
				existing.Team = peerInfo.Team
			}
			if peerInfo.Role != "" {
				existing.Role = peerInfo.Role
			}
			existing.Mode = peerInfo.Mode
			existing.Online = true
			existing.LastSeen = time.Now().Unix()
			existing.notifiedLeave = false // reset: peer recovered
			// Update workspace metadata from peer's response
			if peerInfo.Workspace != "" {
				existing.Workspace = peerInfo.Workspace
			}
			if peerInfo.ProjectName != "" {
				existing.ProjectName = peerInfo.ProjectName
			}
			if peerInfo.Languages != nil {
				existing.Languages = peerInfo.Languages
			}
			if peerInfo.Frameworks != nil {
				existing.Frameworks = peerInfo.Frameworks
			}
			if peerInfo.HasGit {
				existing.HasGit = true
			}
			if peerInfo.HasTests {
				existing.HasTests = true
			}
			participant = *existing
			// Fire join callback only if we haven't already announced this nick.
			// The wasOffline re-join was removed — it caused duplicate "is online"
			// notifications every time a presence heartbeat recovered.
			if existing.HumanNick != "" && !existing.notifiedJoin && h.shouldNotifyJoin(existing.HumanNick) {
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

// BroadcastAsAgent sends a message from the agent to all peers.
func (h *Hub) BroadcastAsAgent(ctx context.Context, content string) error {
	h.mu.RLock()
	nick := h.agentNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleAgent, nick, "", "", content, nil)
	return h.deliverMessage(ctx, msg, true)
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
			safego.Go("lanchat.postToPeer", func() { h.postToPeerWithRetry(ctx, peer.NodeID, peer.Endpoint, msg, 1) })
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
	return h.postToPeerWithRetry(ctx, peer.NodeID, peer.Endpoint, msg, 2)
}

// postToPeerWithRetry sends a message to a peer, retrying up to maxRetries
// times on transient failures. Uses smart transport selection: skips TCP
// when peerHealthMap indicates TCP is down for this peer.
func (h *Hub) postToPeerWithRetry(ctx context.Context, nodeID, endpoint string, msg Message, maxRetries int) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		if err := h.sendToPeerSmart(ctx, nodeID, endpoint, msg); err != nil {
			lastErr = err
			debug.Log("lanchat", "delivery to %s failed (attempt %d/%d): %v", endpoint, attempt+1, maxRetries, err)
			continue
		}
		return nil
	}
	return lastErr
}

// sendToPeerSmart sends a message to a peer using smart transport selection.
// It checks peerHealthMap to decide whether to try TCP first or go straight to UDP.
// This is zero-wait: if TCP is marked down, UDP is used immediately.
func (h *Hub) sendToPeerSmart(ctx context.Context, nodeID, endpoint string, msg Message) error {
	// Check if TCP is known to be down for this peer
	if !h.shouldTryTCP(nodeID) {
		debug.Log("lanchat", "TCP marked down for %s, using UDP fallback", nodeID)
		return h.postToPeerWithFallback(ctx, endpoint, "message", msg)
	}

	// Try TCP first
	if err := h.postToPeer(ctx, endpoint, msg); err != nil {
		// TCP failed — record and fallback to UDP immediately
		h.recordTransportResult(nodeID, "tcp", false)
		debug.Log("lanchat", "TCP failed for %s, falling back to UDP: %v", nodeID, err)
		return h.postToPeerWithFallback(ctx, endpoint, "message", msg)
	}

	// TCP succeeded
	h.recordTransportResult(nodeID, "tcp", true)
	return nil
}

// shouldTryTCP returns true if TCP should be attempted for this peer.
// Returns true when: peer is unknown (no health data), TCP is healthy,
// or the retry cooldown has expired.
func (h *Hub) shouldTryTCP(nodeID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ph, ok := h.peerHealthMap[nodeID]
	if !ok {
		return true // unknown peer — try TCP
	}
	if ph.tcpOK {
		return true
	}
	// TCP is down — check if retry cooldown has expired
	return time.Now().After(ph.tcpRetryAt)
}

// recordTransportResult updates the peer health map based on transport results.
// For TCP: single failure marks TCP as down immediately (LAN env — TCP either
// works or it doesn't, no partial degradation). Retry after tcpRetryInterval.
func (h *Hub) recordTransportResult(nodeID, transport string, success bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ph, ok := h.peerHealthMap[nodeID]
	if !ok {
		ph = &peerHealth{}
		h.peerHealthMap[nodeID] = ph
	}
	switch transport {
	case "tcp":
		ph.lastTCP = time.Now()
		if success {
			if !ph.tcpOK {
				debug.Log("lanchat", "TCP recovered for %s", nodeID)
			}
			ph.tcpOK = true
			ph.tcpFail = 0
		} else {
			ph.tcpFail++
			if ph.tcpOK {
				ph.tcpOK = false
				ph.tcpRetryAt = time.Now().Add(tcpRetryInterval)
				debug.Log("lanchat", "TCP marked down for %s (retry in %v)", nodeID, tcpRetryInterval)
			}
		}
	case "udp":
		ph.lastUDP = time.Now()
	case "mcast":
		ph.lastMcast = time.Now()
	}
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
	req.Header.Set("X-API-Key", communityKey)

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

func (h *Hub) broadcastNickChange(newNick, newRole, newTeam string) {
	h.mu.RLock()
	change := NickChange{
		NodeID:    h.nodeID,
		HumanNick: newNick,
		AgentNick: AgentNick(newNick),
		Role:      newRole,
		Team:      newTeam,
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
			req.Header.Set("X-API-Key", communityKey)
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

	// Check if this is an @agent direct message
	needsApproval := msg.IsDirectToAgent() && msg.ToNodeID == h.nodeID

	// Agent-directed messages are injected into the agent loop and should NOT
	// appear in the LAN Chat panel or history — they are agent context, not
	// human conversation. Skip both in-memory history and disk persistence.
	// Regular (human-to-human) messages are stored normally.
	if !needsApproval {
		h.messages = append(h.messages, msg)
		if len(h.messages) > maxHistoryPerSession*2 {
			h.messages = h.messages[len(h.messages)-maxHistoryPerSession:]
		}
		h.persistMessage(msg)
	}

	// Determine approval action based on policy, daemon mode, or agent-to-agent auto-approve
	autoApproved := false
	autoRejected := false
	if needsApproval {
		if msg.FromRole == RoleAgent {
			// Agent-to-agent messages are always auto-approved — no human intervention needed
			autoApproved = true
		} else {
			policy := h.approvalPolicies[msg.FromNick]
			if policy == "always" || h.mode == "daemon" {
				// Auto-approve: daemon mode defaults to approve, or explicit policy
				autoApproved = true
			} else if policy == "never" {
				autoRejected = true
			}
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

	// Fire onMessage callback for regular messages only.
	// Agent-directed messages are injected into the agent loop (via
	// onAutoApprove or manual approval) and will appear as user messages —
	// firing onMessage here would cause duplicate rendering in the UI.
	if callback != nil && !needsApproval {
		safego.Go("lanchat.messageCallback", func() { callback(msg) })
	}

	// Broadcast messages (no specific recipient) are fire-and-forget.
	// They do NOT generate any receipt — there is no single sender expecting
	// confirmation, and sending receipts from every receiver creates noise.
	if msg.IsBroadcast() {
		return
	}

	// Send delivered receipt immediately (direct messages only)
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

// HandleNickChange updates a peer's nickname and fires the onNickChange callback
// so the UI can display a system message.
func (h *Hub) HandleNickChange(change NickChange) {
	h.mu.Lock()
	oldNick := ""
	if peer, ok := h.peers[change.NodeID]; ok {
		oldNick = peer.HumanNick
		if change.HumanNick != "" {
			peer.HumanNick = change.HumanNick
		}
		if change.AgentNick != "" {
			peer.AgentNick = change.AgentNick
		}
		if change.Role != "" {
			peer.Role = change.Role
		}
		if change.Team != "" {
			peer.Team = change.Team
		}
	}
	cb := h.onNickChange
	h.mu.Unlock()
	if cb != nil {
		safego.Go("lanchat.nickChangeCb", func() { cb(change.NodeID, oldNick, change.HumanNick) })
	}
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
	// Read nicks under lock — SetSessionID may change them concurrently.
	h.mu.RLock()
	fromNick := h.humanNick
	fromRole := RoleHuman
	if originalMsg.ToRole == RoleAgent {
		fromNick = h.agentNick
		fromRole = RoleAgent
	}
	nodeID := h.nodeID
	h.mu.RUnlock()

	r := Receipt{
		MessageID:  originalMsg.ID,
		Status:     status,
		FromNodeID: nodeID,
		FromNick:   fromNick,
		FromRole:   fromRole,
		ToNodeID:   originalMsg.FromNodeID, // route back to original sender
		ToRole:     originalMsg.ToRole,     // original message's target role (agent/human) — routes receipt to the same DM room
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
	req.Header.Set("X-API-Key", communityKey)
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

// SetOnInboundDM registers a callback invoked when a non-broadcast message
// arrives, allowing the rate limiter to reset the DM cooldown for the sender
// so the local agent can reply immediately.
func (h *Hub) SetOnInboundDM(cb func(fromNodeID string)) {
	h.mu.Lock()
	h.onInboundDM = cb
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

// handleUDPEnvelope processes incoming UDP messages (called by UDPTransport).
func (h *Hub) handleUDPEnvelope(env udpEnvelope, remoteAddr net.Addr) {
	// Handle ACK
	if env.Type == "ack" && env.ACKID != "" {
		h.ackTracker.Store(env.ACKID, true)
		return
	}

	// Dispatch based on type
	switch env.Type {
	case "message":
		var msg Message
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			debug.Log("lanchat-udp", "message payload parse error: %v", err)
			return
		}
		// For multicast DMs, check if this message is for us
		h.mu.RLock()
		myNodeID := h.nodeID
		h.mu.RUnlock()
		if msg.ToNodeID != "" && msg.ToNodeID != myNodeID {
			return // not for us (multicast DM filtering)
		}
		h.handleReceiveMessageData(msg, remoteAddr.String())

	case "presence":
		var p Participant
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			debug.Log("lanchat-udp", "presence payload parse error: %v", err)
			return
		}
		h.HandlePresence(p)

	case "nick":
		var nc NickChange
		if err := json.Unmarshal(env.Payload, &nc); err != nil {
			debug.Log("lanchat-udp", "nick payload parse error: %v", err)
			return
		}
		h.handleNickChangeData(nc)

	case "receipt":
		var r Receipt
		if err := json.Unmarshal(env.Payload, &r); err != nil {
			debug.Log("lanchat-udp", "receipt payload parse error: %v", err)
			return
		}
		h.handleReceiveReceiptData(r)
	}
}

// SetUDPTransport connects the UDP transport to the Hub.
func (h *Hub) SetUDPTransport(t *UDPTransport) {
	h.udpTransport = t
}

// postToPeerWithFallback tries TCP first, then UDP unicast, then UDP multicast.
func (h *Hub) postToPeerWithFallback(ctx context.Context, endpoint string, msgType string, payload interface{}) error {
	// 1. Try TCP (HTTP POST)
	if err := h.postToPeerTCPDirect(ctx, endpoint, msgType, payload); err == nil {
		return nil
	}

	// 2. TCP failed — try UDP if available
	if h.udpTransport == nil {
		return fmt.Errorf("tcp failed and no udp transport")
	}

	// Extract host:port from endpoint
	host, port, err := parseHostPort(endpoint)
	if err != nil {
		return fmt.Errorf("tcp failed, cannot parse endpoint for udp: %w", err)
	}

	payloadData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	env := udpEnvelope{
		Type:     msgType,
		APIKey:   communityKey,
		FromNode: h.nodeID,
		Payload:  payloadData,
	}

	// 2a. Try UDP unicast
	udpAddr := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	if err := h.udpTransport.SendUnicast(ctx, udpAddr, env); err == nil {
		debug.Log("lanchat", "delivered via udp unicast to %s", host)
		return nil
	}

	// 3. Try UDP multicast (last resort — fire and forget, no ACK)
	if err := h.udpTransport.SendMulticast(env); err == nil {
		debug.Log("lanchat", "delivered via udp multicast")
		return nil
	}

	return fmt.Errorf("all transports failed for %s", endpoint)
}

func parseHostPort(endpoint string) (string, int, error) {
	// endpoint format: "http://192.168.1.100:38471"
	u := strings.TrimPrefix(endpoint, "http://")
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimRight(u, "/")
	host, portStr, err := net.SplitHostPort(u)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

// postToPeerTCPDirect sends a payload via HTTP POST to the TCP endpoint.
// This is the internal TCP-only sender used by postToPeerWithFallback.
func (h *Hub) postToPeerTCPDirect(ctx context.Context, endpoint string, msgType string, payload interface{}) error {
	var path string
	switch msgType {
	case "message":
		path = "/lanchat/message"
	case "presence":
		path = "/lanchat/presence"
	case "nick":
		path = "/lanchat/nick"
	case "receipt":
		path = "/lanchat/receipt"
	default:
		return fmt.Errorf("unknown message type: %s", msgType)
	}

	url := strings.TrimRight(endpoint, "/") + path
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", communityKey)
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

// handleReceiveMessageData processes a message from any transport.
func (h *Hub) handleReceiveMessageData(msg Message, source string) {
	// Ignore messages from self (loop prevention for UDP multicast path).
	// HTTP handler already filters this in handlers.go, but UDP multicast
	// can deliver our own packets back to us.
	if msg.FromNodeID == h.nodeID {
		return
	}
	// Delegate to the existing handler (extracted from HTTP handler)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hasMessageLocked(msg.ID) {
		return // dedup
	}
	h.messages = append(h.messages, msg)
	if len(h.messages) > maxHistoryPerSession {
		h.messages = h.messages[len(h.messages)-maxHistoryPerSession:]
	}
	debug.Log("lanchat", "received message from %s via %s: %s", msg.FromNick, source, truncate(msg.Content, 40))
	// Fire onInboundDM callback for non-broadcast messages so the rate
	// limiter can reset the DM cooldown for the sender.
	if !msg.IsBroadcast() && h.onInboundDM != nil {
		fromID := msg.FromNodeID
		safego.Go("lanchat.onInboundDM", func() { h.onInboundDM(fromID) })
	}
	// Fire onMessage callback outside lock
	msgCopy := msg
	if h.onMessage != nil {
		safego.Go("lanchat.onMessage", func() { h.onMessage(msgCopy) })
	}
}

// handleReceiveReceiptData processes a receipt from any transport.
func (h *Hub) handleReceiveReceiptData(r Receipt) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onReceipt != nil {
		rc := r
		safego.Go("lanchat.onReceipt", func() { h.onReceipt(rc) })
	}
}

// handleNickChangeData processes a nick change from any transport.
func (h *Hub) handleNickChangeData(nc NickChange) {
	h.mu.RLock()
	peer, ok := h.peers[nc.NodeID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	peer.HumanNick = nc.HumanNick
	peer.AgentNick = nc.AgentNick
	peer.Role = nc.Role
	peer.Team = nc.Team
	if h.onNickChange != nil {
		ncCopy := nc
		safego.Go("lanchat.onNickChange", func() { h.onNickChange(ncCopy.NodeID, ncCopy.HumanNick, ncCopy.AgentNick) })
	}
}

// hasMessageLocked checks if a message ID already exists (must be called with lock held).
func (h *Hub) hasMessageLocked(msgID string) bool {
	for _, m := range h.messages {
		if m.ID == msgID {
			return true
		}
	}
	return false
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
