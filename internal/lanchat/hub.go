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

const (
	seenMsgCap          = 512
	replayWindowMs      = int64(15 * time.Minute / time.Millisecond)
	recentAgentMsgCap   = 256
	receiptCap          = 512
	maxPendingApprovals = 100
	pendingApprovalTTL  = 10 * time.Minute
)

type Hub struct {
	mu sync.RWMutex

	closeOnce sync.Once

	nodeID    string
	humanNick string
	agentNick string
	role      string
	team      string
	mode      string
	endpoint  string
	apiKey    string

	workspace   string
	projectName string
	languages   []string
	frameworks  []string
	hasGit      bool
	hasTests    bool

	agentBusy bool

	agentStatus          string
	agentStatusSince     int64
	model                string
	consecRateLimitFails int
	healthProber         HealthProber
	probeCancel          context.CancelFunc

	peers map[string]*Participant

	archive []ArchivedPeer

	messages []Message

	pendingApproval []PendingAgentMsg

	seenMsgIDs   map[string]bool
	seenMsgOrder []string

	recentAgentMsgs     map[string]Message
	recentAgentMsgOrder []string

	attachments *AttachmentManager

	receipts     map[string]Receipt
	receiptOrder []string

	store     *Store
	sessionID string

	onMessage        func(Message)
	onReceipt        func(Receipt)
	onParticipantAdd func(Participant)
	onParticipantRm  func(nodeID, humanNick string)
	onApprovalReq    func(PendingAgentMsg)
	onNickChange     func(nodeID, oldNick, newNick string)

	approvalPolicies   map[string]string
	trustedPeers       map[string]bool
	trustedPeersLoaded bool

	nodeKey      *NodeKey
	keyPins      map[string]string
	trustedKeys  map[string]string
	pinsLoaded   bool
	nodeKeyReady bool

	approvalPoliciesLoaded bool

	requireAgentApproval bool

	notifiedNicks map[string]bool

	onAutoApprove func(Message)

	onInboundDM func(fromNodeID string)

	httpClient *http.Client

	udpTransport *UDPTransport

	peerHealthMap map[string]*peerHealth
}

type PendingAgentMsg struct {
	Message  Message   `json:"message"`
	Received time.Time `json:"received"`
}

type WorkspaceMeta struct {
	Workspace   string   `json:"workspace"`
	ProjectName string   `json:"project_name"`
	Languages   []string `json:"languages"`
	Frameworks  []string `json:"frameworks"`
	HasGit      bool     `json:"has_git"`
	HasTests    bool     `json:"has_tests"`
}

func NewHub(nodeID, mode, endpoint, apiKey string, store *Store, ws WorkspaceMeta) *Hub {
	baseNick := RandomNick()
	nick := baseNick + "_" + DefaultRole

	policies, err := LoadApprovalPolicies(store.dir)
	policiesLoaded := err == nil
	if err != nil {
		policies = map[string]string{}
		debug.Log("lanchat", "approval policies in %s not loaded (refusing to overwrite on save): %v", store.dir, err)
	}

	trustedFPs, trustErr := LoadTrustedPeers(store.dir)
	trustedLoaded := trustErr == nil
	if trustErr != nil {
		trustedFPs = map[string]string{}
		debug.Log("lanchat", "trusted peers in %s not loaded (refusing to overwrite on save): %v", store.dir, trustErr)
	}
	trusted := map[string]bool{}
	trustedKeys := map[string]string{}
	for id, fp := range trustedFPs {
		trusted[id] = true
		if fp != "" {
			trustedKeys[id] = fp
		}
	}
	pins, pinsErr := LoadKeyPins(store.dir)
	pinsLoaded := pinsErr == nil
	if pinsErr != nil {
		pins = map[string]string{}
		debug.Log("lanchat", "key pins in %s not loaded: %v", store.dir, pinsErr)
	}
	nodeKey, keyErr := LoadOrCreateNodeKey(store.dir)
	nodeKeyReady := keyErr == nil
	if keyErr != nil {
		nodeKey = nil
		debug.Log("lanchat", "node identity key unavailable, messages will be unsigned: %v", keyErr)
	}

	return &Hub{
		nodeID:                 nodeID,
		humanNick:              nick,
		agentNick:              AgentNick(nick),
		role:                   DefaultRole,
		team:                   DefaultTeamFor(ws.Workspace),
		mode:                   mode,
		endpoint:               endpoint,
		apiKey:                 apiKey,
		workspace:              ws.Workspace,
		projectName:            ws.ProjectName,
		languages:              ws.Languages,
		frameworks:             ws.Frameworks,
		hasGit:                 ws.HasGit,
		hasTests:               ws.HasTests,
		peers:                  make(map[string]*Participant),
		receipts:               make(map[string]Receipt),
		store:                  store,
		approvalPolicies:       policies,
		approvalPoliciesLoaded: policiesLoaded,
		trustedPeers:           trusted,
		trustedPeersLoaded:     trustedLoaded,
		nodeKey:                nodeKey,
		keyPins:                pins,
		trustedKeys:            trustedKeys,
		pinsLoaded:             pinsLoaded,
		nodeKeyReady:           nodeKeyReady,
		notifiedNicks:          make(map[string]bool),
		peerHealthMap:          make(map[string]*peerHealth),
		seenMsgIDs:             make(map[string]bool),
		recentAgentMsgs:        make(map[string]Message),
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

func (h *Hub) SetAttachments(am *AttachmentManager) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attachments = am
}

func (h *Hub) SetHTTPTimeout(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.httpClient = &http.Client{Timeout: d}
}

func (h *Hub) SetSessionID(baseDir, sessionID string) {
	if sessionID == "" || baseDir == "" {
		return
	}

	if strings.ContainsAny(sessionID, `\/`) || strings.Contains(sessionID, "..") {
		debug.Log("lanchat", "rejecting unsafe sessionID %q (path separator or parent traversal)", sessionID)
		return
	}
	sessionDir := filepath.Join(baseDir, "sessions", sessionID)
	h.mu.Lock()
	h.sessionID = sessionID
	h.store = NewStore(sessionDir)

	if persisted, err := LoadNick(h.store.dir); err == nil && persisted != "" {
		h.humanNick = persisted
		h.agentNick = AgentNick(persisted)
	} else {

		_ = SaveNick(h.store.dir, h.humanNick)
	}

	if persisted, err := LoadRole(h.store.dir); err == nil && persisted != "" {
		h.role = persisted
	} else {
		_ = SaveRole(h.store.dir, h.role)
	}

	// The persisted team wins only when the user set it explicitly
	// (set_identity); otherwise the workspace-derived default applies so the
	// initial team keeps following the current workspace.
	if persistedTeam, err := LoadTeam(h.store.dir); LoadTeamExplicit(h.store.dir) && err == nil && persistedTeam != "" {
		h.team = persistedTeam
	} else {
		_ = SaveTeam(h.store.dir, h.team)
	}

	if h.store != nil {
		if msgs, err := h.store.LoadRecent(sessionID, maxHistoryPerSession); err == nil {
			h.messages = msgs
		}
	}
	h.mu.Unlock()

	for _, peer := range h.Participants() {
		if peer.NodeID == h.nodeID || !peer.Online {
			continue
		}
		p := peer
		safego.Go("lanchat.presenceUpdate", func() { h.sendPresence(p) })
	}
}

func (h *Hub) Attachments() *AttachmentManager {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.attachments
}

func (h *Hub) APIKey() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.apiKey
}

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

func (h *Hub) HumanNick() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.humanNick
}

func (h *Hub) AgentNick() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agentNick
}

func (h *Hub) Role() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.role
}

func (h *Hub) Team() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.team
}

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

func (h *Hub) NodeID() string {
	return h.nodeID
}

func (h *Hub) SelfParticipant() Participant {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return Participant{
		NodeID:           h.nodeID,
		HumanNick:        h.humanNick,
		AgentNick:        h.agentNick,
		Mode:             h.mode,
		Endpoint:         h.endpoint,
		Role:             h.role,
		Team:             h.team,
		Online:           true,
		LastSeen:         time.Now().Unix(),
		Workspace:        h.workspace,
		ProjectName:      h.projectName,
		Languages:        h.languages,
		Frameworks:       h.frameworks,
		HasGit:           h.hasGit,
		HasTests:         h.hasTests,
		AgentBusy:        h.agentBusy,
		AgentStatus:      h.agentStatus,
		AgentStatusSince: h.agentStatusSince,
		Model:            h.model,
	}
}

func (h *Hub) SetAgentBusy(busy bool) {
	h.mu.Lock()
	if h.agentBusy == busy {
		h.mu.Unlock()
		return
	}
	h.agentBusy = busy
	h.mu.Unlock()

	h.broadcastPresence()
}

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
			return false
		}
	}

	return true
}

// SetPeerOfflineForTest flips a tracked peer offline with the given
// last-seen timestamp. Test-support seam (#5117): cross-package tests
// (internal/tool) cannot reach the private peers map, and HandlePresence
// upserts force Online=true, so there is no other deterministic way to
// build stale-recipient state from outside this package.
func (h *Hub) SetPeerOfflineForTest(nodeID string, lastSeen time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p, ok := h.peers[nodeID]; ok {
		p.Online = false
		p.LastSeen = lastSeen.Unix()
	}
}

// SetPeerLastSeenForTest backdates only LastSeen, leaving Online untouched.
// Test-support seam (#5117): pins the sweep-lag shape (Online=true but
// LastSeen older than ageOffline) in which the registry entry still resolves
// but delivery can no longer succeed.
func (h *Hub) SetPeerLastSeenForTest(nodeID string, lastSeen time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p, ok := h.peers[nodeID]; ok {
		p.LastSeen = lastSeen.Unix()
	}
}

// StaleRecipient reports whether nodeID refers to a stale-but-cached peer:
// still in the registry (not yet swept past peerDeleteAfter) but whose
// LastSeen is older than ageOffline. Such ids still resolve at the tool
// layer yet cannot be delivered to, so sends to them must fail fast instead
// of burning the ~10s unicast ACK timeout (#5117). The returned age is how
// long ago the peer was last seen.
func (h *Hub) StaleRecipient(nodeID string) (time.Duration, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if nodeID == h.nodeID {
		return 0, false
	}
	p, ok := h.peers[nodeID]
	if !ok {
		return 0, false
	}
	age := time.Since(time.Unix(p.LastSeen, 0))
	return age, age > ageOffline
}

// LookupPeerIDByNick resolves a nick (HumanNick or AgentNick,
// case-insensitive) to the node_id of its unique fresh holder, ignoring
// stale pre-restart entries still awaiting the peer sweep (#5200). A nick
// held by zero or multiple fresh peers does not resolve: senders must get
// an explicit ambiguity error rather than a silent coin-flip delivery. The
// returned id is how a DM addressed by nick keeps working across a peer's
// process restarts: node_ids rotate per process, nicks do not.
func (h *Hub) LookupPeerIDByNick(nick string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(nick))
	if lower == "" {
		return "", false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	found := ""
	for _, p := range h.peers {
		if strings.ToLower(p.HumanNick) != lower && strings.ToLower(p.AgentNick) != lower {
			continue
		}
		if time.Since(time.Unix(p.LastSeen, 0)) > ageOffline {
			continue
		}
		if found != "" {
			return "", false
		}
		found = p.NodeID
	}
	return found, found != ""
}

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

func (h *Hub) ArchivePeer(p Participant) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := p
	h.archivePeer(&cp)
}

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

func (h *Hub) Archive() []ArchivedPeer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]ArchivedPeer, len(h.archive))
	copy(result, h.archive)
	return result
}

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

func (h *Hub) Messages() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Message, len(h.messages))
	copy(out, h.messages)
	return out
}

func (h *Hub) PendingApprovals() []PendingAgentMsg {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]PendingAgentMsg, len(h.pendingApproval))
	copy(out, h.pendingApproval)
	return out
}

// WorkspaceDefaultTeam returns the team name derived from the hub's current
// workspace (the /nick default when no team argument is given).
func (h *Hub) WorkspaceDefaultTeam() string {
	h.mu.RLock()
	ws := h.workspace
	h.mu.RUnlock()
	return DefaultTeamFor(ws)
}

func (h *Hub) SetNickRoleTeam(nick, role, team string) error {
	composite := nick + "_" + role
	h.mu.Lock()
	h.humanNick = composite
	h.agentNick = AgentNick(composite)
	h.role = role
	h.team = team
	sessionDir := h.store.dir
	h.mu.Unlock()

	if err := SaveNick(sessionDir, composite); err != nil {
		debug.Log("lanchat", "failed to persist session nick: %v", err)
	}
	if err := SaveRole(sessionDir, role); err != nil {
		debug.Log("lanchat", "failed to persist session role: %v", err)
	}
	if err := SaveTeam(sessionDir, team); err != nil {
		debug.Log("lanchat", "failed to persist session team: %v", err)
	}
	if err := SaveTeamExplicit(sessionDir); err != nil {
		debug.Log("lanchat", "failed to persist explicit-team marker: %v", err)
	}

	safego.Go("lanchat.broadcastNickChange", func() { h.broadcastNickChange(composite, role, team) })
	return nil
}

func (h *Hub) SetNickRole(nick, role string) error {
	h.mu.RLock()
	team := h.team
	h.mu.RUnlock()
	return h.SetNickRoleTeam(nick, role, team)
}

func (h *Hub) UpdatePeers(participants []Participant) {
	h.mu.Lock()

	var newPeers []Participant

	for _, p := range participants {
		if p.NodeID == h.nodeID {
			continue
		}
		existing, ok := h.peers[p.NodeID]
		if !ok {

			cp := p
			cp.Online = true
			cp.LastSeen = time.Now().Unix()
			h.peers[p.NodeID] = &cp
			newPeers = append(newPeers, cp)
		} else {

			if p.Endpoint != "" {
				existing.Endpoint = p.Endpoint
			}
			if p.Mode != "" {
				existing.Mode = p.Mode
			}
		}
	}

	var stalePeers []Participant
	var emptyNickPeers []Participant
	for _, p := range h.peers {
		if p.HumanNick == "" {
			emptyNickPeers = append(emptyNickPeers, *p)
		}
		if time.Since(time.Unix(p.LastSeen, 0)) > presenceHeartbeat {
			stalePeers = append(stalePeers, *p)
		}
	}

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

		}

		if !p.Online && p.lastOfflineTime > 0 && !p.notifiedLeave {
			if now.Sub(time.Unix(0, p.lastOfflineTime)) >= offlineNotifyDelay {
				p.notifiedLeave = true
				if p.notifiedJoin && p.HumanNick != "" && h.shouldNotifyLeave(p.HumanNick, id) {
					leftPeers = append(leftPeers, leftPeer{nodeID: id, humanNick: p.HumanNick})
				}
			}
		}

		if time.Since(time.Unix(p.LastSeen, 0)) > peerDeleteAfter {
			if p.HumanNick != "" {
				delete(h.notifiedNicks, strings.TrimSpace(p.HumanNick))
			}

			h.archivePeer(p)
			delete(h.peers, id)
		}
	}

	callbacks := struct {
		add func(Participant)
		rm  func(nodeID, humanNick string)
	}{h.onParticipantAdd, h.onParticipantRm}
	h.mu.Unlock()

	for _, np := range newPeers {
		if np.HumanNick != "" && callbacks.add != nil {
			safego.Go("lanchat.participantAdd", func() { callbacks.add(np) })
		}

		safego.Go("lanchat.sendPresence", func() { h.sendPresence(np) })
	}

	for _, ep := range emptyNickPeers {
		safego.Go("lanchat.sendPresence", func() { h.sendPresence(ep) })
	}

	for _, sp := range stalePeers {
		safego.Go("lanchat.sendPresence", func() { h.sendPresence(sp) })
	}
	for _, lp := range leftPeers {
		if callbacks.rm != nil {
			safego.Go("lanchat.participantRm", func() { callbacks.rm(lp.nodeID, lp.humanNick) })
		}
	}

	h.resolveNickConflict()
}

func (h *Hub) resolveNickConflict() {
	h.mu.Lock()
	myNick := h.humanNick
	conflict := false
	for _, p := range h.peers {
		if !p.Online {
			continue
		}
		// #5557: BOTH nick surfaces must conflict-check. HumanNick-only let
		// two agents with identical AgentNicks coexist, making agent-to-agent
		// DMs silently ambiguous until send time.
		if p.HumanNick == myNick || p.AgentNick == h.agentNick {
			conflict = true
			break
		}
	}
	if !conflict {
		h.mu.Unlock()
		return
	}

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

	role, team := h.role, h.team
	h.mu.Unlock()

	debug.Log("lanchat", "nick conflict: %s -> %s", myNick, newNick)
	_ = SaveNick(sessionDir, newNick)
	safego.Go("lanchat.broadcastNickChange", func() { h.broadcastNickChange(newNick, role, team) })
}

func (h *Hub) HandlePresence(p Participant) {
	h.mu.Lock()
	existing, ok := h.peers[p.NodeID]
	needCallback := false
	if !ok {

		cp := p
		cp.Online = true
		cp.LastSeen = time.Now().Unix()
		if cp.HumanNick != "" && h.shouldNotifyJoin(cp.HumanNick) {
			cp.notifiedJoin = true
			needCallback = true
		}
		h.peers[p.NodeID] = &cp
	} else {

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
		existing.notifiedLeave = false

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

		existing.AgentBusy = p.AgentBusy

		existing.AgentStatus = p.AgentStatus
		existing.AgentStatusSince = p.AgentStatusSince
		if p.Model != "" {
			existing.Model = p.Model
		}

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
	req.Header.Set("X-API-Key", h.APIKey())

	resp, err := h.httpClient.Do(req)
	if err != nil {

		h.recordTransportResult(peer.NodeID, "tcp", false)
		return
	}
	defer resp.Body.Close()

	h.recordTransportResult(peer.NodeID, "tcp", true)

	var peerInfo Participant
	if err := json.NewDecoder(io.LimitReader(resp.Body, lanchatMaxBodyBytes)).Decode(&peerInfo); err == nil && peerInfo.NodeID != "" {
		var cb func(Participant)
		var participant Participant
		h.mu.Lock()
		if existing, ok := h.peers[peerInfo.NodeID]; ok {

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
			existing.notifiedLeave = false

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

func (h *Hub) SendBroadcast(ctx context.Context, content string, attachments []Attachment) error {
	h.mu.RLock()
	nick := h.humanNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleHuman, nick, "", "", content, attachments)
	return h.deliverMessage(ctx, msg, true)
}

func (h *Hub) SendDirect(ctx context.Context, toNodeID, toRole, content string, attachments []Attachment) error {
	h.mu.RLock()
	nick := h.humanNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleHuman, nick, toNodeID, toRole, content, attachments)
	return h.deliverMessage(ctx, msg, false)
}

func (h *Hub) SendAsAgent(ctx context.Context, toNodeID, toRole, content string) error {
	h.mu.RLock()
	nick := h.agentNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleAgent, nick, toNodeID, toRole, content, nil)
	return h.deliverMessage(ctx, msg, false)
}

func (h *Hub) BroadcastAsAgent(ctx context.Context, content string) error {
	h.mu.RLock()
	nick := h.agentNick
	h.mu.RUnlock()
	msg := h.newMessage(RoleAgent, nick, "", "", content, nil)
	return h.deliverMessage(ctx, msg, true)
}

func (h *Hub) newMessage(fromRole, fromNick, toNodeID, toRole, content string, attachments []Attachment) Message {

	if len(attachments) > 0 {
		h.mu.RLock()
		ep := h.endpoint
		h.mu.RUnlock()
		for i := range attachments {
			SetAttachmentURL(ep, &attachments[i])
		}
	}
	m := Message{
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

	signMessage(&m, h.nodeKey)
	return m
}

func (h *Hub) deliverMessage(ctx context.Context, msg Message, broadcast bool) error {

	h.mu.Lock()
	h.messages = append(h.messages, msg)
	if len(h.messages) > maxHistoryPerSession*2 {
		h.messages = h.messages[len(h.messages)-maxHistoryPerSession:]
	}

	h.persistMessage(msg)
	h.mu.Unlock()

	if broadcast {

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

	h.mu.RLock()
	peer, ok := h.peers[msg.ToNodeID]
	var peerCopy Participant
	if ok {
		peerCopy = *peer
	}
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown peer: %s", msg.ToNodeID)
	}
	return h.postToPeerWithRetry(ctx, peerCopy.NodeID, peerCopy.Endpoint, msg, 2)
}

var PeerRetryDelayFn = func(attempt int) time.Duration {
	return time.Duration(attempt) * time.Second
}

func (h *Hub) postToPeerWithRetry(ctx context.Context, nodeID, endpoint string, msg Message, maxRetries int) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(PeerRetryDelayFn(attempt)):
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

func (h *Hub) sendToPeerSmart(ctx context.Context, nodeID, endpoint string, msg Message) error {

	if !h.shouldTryTCP(nodeID) {
		debug.Log("lanchat", "TCP marked down for %s, using UDP fallback", nodeID)
		return h.postToPeerWithFallback(ctx, endpoint, "message", msg)
	}

	if err := h.postToPeer(ctx, endpoint, msg); err != nil {

		h.recordTransportResult(nodeID, "tcp", false)
		debug.Log("lanchat", "TCP failed for %s, falling back to UDP: %v", nodeID, err)
		return h.postToPeerWithFallback(ctx, endpoint, "message", msg)
	}

	h.recordTransportResult(nodeID, "tcp", true)
	return nil
}

func (h *Hub) shouldTryTCP(nodeID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ph, ok := h.peerHealthMap[nodeID]
	if !ok {
		return true
	}
	if ph.tcpOK {
		return true
	}

	return time.Now().After(ph.tcpRetryAt)
}

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
			if !ph.tcpOK && !ph.tcpRetryAt.IsZero() {
				downDur := time.Since(ph.tcpRetryAt)
				if downDur >= 2*tcpRetryInterval {
					debug.Log("lanchat", "TCP recovered for %s (was down %v)", nodeID, downDur.Round(time.Second))
				}
			}
			ph.tcpOK = true
			ph.tcpFail = 0
		} else {
			ph.tcpFail++
			if ph.tcpOK {
				ph.tcpOK = false
				ph.tcpRetryAt = time.Now().Add(tcpRetryInterval)
				upDur := time.Since(ph.lastTCP)
				if upDur >= 2*tcpRetryInterval {
					debug.Log("lanchat", "TCP marked down for %s (up %v, retry in %v)", nodeID, upDur.Round(time.Second), tcpRetryInterval)
				}
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
	req.Header.Set("X-API-Key", h.APIKey())

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

			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", h.APIKey())
			resp, err := h.httpClient.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
		})
	}
}

func (h *Hub) persistMessage(msg Message) {
	if h.store != nil && h.sessionID != "" {
		if err := h.store.Append(h.sessionID, msg); err != nil {
			debug.Log("lanchat", "persist message: %v", err)
		}
	}
}

func (h *Hub) HandleIncomingMessage(msg Message) {
	h.mu.Lock()

	var persistSession string
	var persistStore *Store

	if msg.Sig != "" {
		if skew := time.Now().UnixMilli() - msg.Timestamp; skew > replayWindowMs || skew < -replayWindowMs {
			h.mu.Unlock()
			debug.Log("lanchat", "drop stale signed message %s from %s (replay window %.1fmin)",
				msg.ID, msg.FromNodeID, float64(skew)/float64(time.Minute/time.Millisecond))
			return
		}
	}
	if h.seenMsgIDs[msg.ID] {
		h.mu.Unlock()
		debug.Log("lanchat", "drop duplicate message %s from %s (double delivery)", msg.ID, msg.FromNodeID)
		return
	}
	h.markSeenLocked(msg.ID)

	if ok, fp, err := verifyMessage(&msg); ok {
		if pin, seen := h.keyPins[msg.FromNodeID]; seen && pin != fp {
			debug.Log("lanchat", "identity pin conflict for %s: pinned %s, now presenting %s - treating as unverified", msg.FromNodeID, pin, fp)
		} else {
			msg.Verified = true
			msg.SignerFP = fp
			if !seen {
				h.keyPins[msg.FromNodeID] = fp
				h.persistPinsLocked()
			}
		}
	} else if err != nil && err != errNoSignature {
		debug.Log("lanchat", "message from %s failed signature verification: %v", msg.FromNodeID, err)
	}

	needsApproval := msg.IsDirectToAgent() && msg.ToNodeID == h.nodeID

	if !needsApproval {
		h.messages = append(h.messages, msg)
		if len(h.messages) > maxHistoryPerSession*2 {
			h.messages = h.messages[len(h.messages)-maxHistoryPerSession:]
		}
		if h.store != nil {
			persistSession = h.sessionID
			persistStore = h.store
		}
	} else {

		h.recordAgentMsgLocked(msg)
	}

	autoApproved, autoRejected := false, false
	if needsApproval {
		autoApproved, autoRejected = h.decideAutoApprovalLocked(msg)
	}

	var evicted []evictedPending
	if needsApproval && !autoApproved && !autoRejected {

		evicted = h.prunePendingApprovalsLocked()
		pending := PendingAgentMsg{Message: msg, Received: time.Now()}
		h.pendingApproval = append(h.pendingApproval, pending)
	}

	h.mu.Unlock()

	if persistSession != "" && persistStore != nil {
		if err := persistStore.Append(persistSession, msg); err != nil {
			debug.Log("lanchat", "persist message: %v", err)
		}
	}

	h.dispatchInboundReceipts(msg, needsApproval, autoApproved, autoRejected, evicted)
}

func (h *Hub) decideAutoApprovalLocked(msg Message) (autoApproved, autoRejected bool) {

	if msg.FromRole == RoleAgent && (!h.requireAgentApproval || h.trustedExemptLocked(&msg)) {

		return true, false
	}
	policy := h.approvalPolicies[msg.FromNick]
	if policy == "always" {
		return true, false
	}
	if policy == "never" {
		return false, true
	}
	return false, false
}

func (h *Hub) dispatchInboundReceipts(msg Message, needsApproval, autoApproved, autoRejected bool, evicted []evictedPending) {

	for _, ev := range evicted {
		e := ev
		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(e.msg, StatusRejected, e.reason) })
	}

	h.mu.RLock()
	callback := h.onMessage
	autoApproveCb := h.onAutoApprove
	approvalCb := h.onApprovalReq
	inboundDMCb := h.onInboundDM
	h.mu.RUnlock()

	if callback != nil && !needsApproval {
		safego.Go("lanchat.messageCallback", func() { callback(msg) })
	}

	if !msg.IsBroadcast() && inboundDMCb != nil {
		fromID := msg.FromNodeID
		safego.Go("lanchat.onInboundDM", func() { inboundDMCb(fromID) })
	}

	if msg.IsBroadcast() {
		return
	}

	safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusDelivered, "") })

	if autoApproved {

		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusApproved, "") })
		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusProcessing, "") })
		if autoApproveCb != nil {
			safego.Go("lanchat.autoApprove", func() {
				autoApproveCb(msg)

				safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusCompleted, "") })
			})
		}
	} else if autoRejected {
		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusRejected, "auto-rejected by policy") })
	} else if needsApproval {

		safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusPending, "") })
		if approvalCb != nil {
			safego.Go("lanchat.approvalCallback", func() { approvalCb(PendingAgentMsg{Message: msg, Received: time.Now()}) })
		}
	}
}

func (h *Hub) HandleReceipt(r Receipt) {
	h.mu.Lock()
	h.storeReceiptLocked(r)
	callback := h.onReceipt
	h.mu.Unlock()

	if callback != nil {
		safego.Go("lanchat.receiptCallback", func() { callback(r) })
	}
}

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

func (h *Hub) HandleParticipantQuery() Participant {
	return h.SelfParticipant()
}

func (h *Hub) ApproveMessage(messageID string) (*Message, error) {
	h.mu.Lock()
	for i, pending := range h.pendingApproval {
		if pending.Message.ID == messageID {
			h.pendingApproval = append(h.pendingApproval[:i], h.pendingApproval[i+1:]...)
			msg := pending.Message
			h.mu.Unlock()

			safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusApproved, "") })
			safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(msg, StatusProcessing, "") })
			return &msg, nil
		}
	}
	h.mu.Unlock()
	return nil, fmt.Errorf("message %s not found in pending approvals", messageID)
}

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

func (h *Hub) GetReceipt(messageID string) (Receipt, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.receipts[messageID]
	return r, ok
}

func (h *Hub) sendReceipt(originalMsg Message, status, reason string) {

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
		ToNodeID:   originalMsg.FromNodeID,
		ToRole:     originalMsg.ToRole,
		Timestamp:  time.Now().UnixMilli(),
		Reason:     reason,
	}

	h.mu.RLock()
	peer, ok := h.peers[originalMsg.FromNodeID]
	var peerCopy Participant
	if ok {
		peerCopy = *peer
	}
	h.mu.RUnlock()
	if !ok {
		return
	}

	url := strings.TrimRight(peerCopy.Endpoint, "/") + "/lanchat/receipt"
	data, _ := json.Marshal(r)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", h.APIKey())
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func (h *Hub) LoadHistory(sessionID string) ([]Message, error) {
	return h.store.LoadRecent(sessionID, maxHistoryPerSession)
}

func (h *Hub) PersistMessage(sessionID string, msg Message) error {
	return h.store.Append(sessionID, msg)
}

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

	if dir != "" {
		h.mu.RLock()
		loaded := h.approvalPoliciesLoaded
		policies := make(map[string]string, len(h.approvalPolicies))
		for k, v := range h.approvalPolicies {
			policies[k] = v
		}
		h.mu.RUnlock()
		if !loaded {
			debug.Log("lanchat", "approval policies not persisted: %s was never loaded successfully, refusing to overwrite", dir)
			return
		}
		_ = SaveApprovalPolicies(dir, policies)
	}
}

func (h *Hub) GetApprovalPolicies() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make(map[string]string, len(h.approvalPolicies))
	for k, v := range h.approvalPolicies {
		result[k] = v
	}
	return result
}

func (h *Hub) trustedExemptLocked(msg *Message) bool {
	if !h.trustedPeers[msg.FromNodeID] {
		return false
	}
	want, ok := h.trustedKeys[msg.FromNodeID]
	if !ok || want == "" {
		return false
	}
	return msg.Verified && msg.SignerFP == want
}

func (h *Hub) persistPinsLocked() {
	if h.store == nil || !h.pinsLoaded {
		return
	}
	pins := make(map[string]string, len(h.keyPins))
	for k, v := range h.keyPins {
		pins[k] = v
	}
	dir := h.store.dir

	safego.Go("lanchat.persistPins", func() {
		if err := SaveKeyPins(dir, pins); err != nil {
			debug.Log("lanchat", "persist key pins: %v", err)
		}
	})
}

func (h *Hub) SetTrustedPeer(nodeID string, trusted bool) {
	h.mu.Lock()
	if h.trustedPeers == nil {
		h.trustedPeers = make(map[string]bool)
	}
	if trusted {
		h.trustedPeers[nodeID] = true

		if h.trustedKeys == nil {
			h.trustedKeys = map[string]string{}
		}
		if fp := h.keyPins[nodeID]; fp != "" {
			h.trustedKeys[nodeID] = fp
		} else {
			delete(h.trustedKeys, nodeID)
		}
	} else {
		delete(h.trustedPeers, nodeID)
		delete(h.trustedKeys, nodeID)
	}
	dir := ""
	if h.store != nil {
		dir = h.store.dir
	}
	h.mu.Unlock()

	if dir != "" {
		h.mu.RLock()
		loaded := h.trustedPeersLoaded
		peers := make(map[string]string, len(h.trustedPeers))
		for k := range h.trustedPeers {
			peers[k] = h.trustedKeys[k]
		}
		h.mu.RUnlock()
		if !loaded {
			debug.Log("lanchat", "trusted peers not persisted: %s was never loaded successfully, refusing to overwrite", dir)
			return
		}
		_ = SaveTrustedPeers(dir, peers)
	}
}

func (h *Hub) IsTrustedPeer(nodeID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.trustedPeers[nodeID]
}

func (h *Hub) GetTrustedPeers() map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make(map[string]bool, len(h.trustedPeers))
	for k, v := range h.trustedPeers {
		result[k] = v
	}
	return result
}

func (h *Hub) SetOnAutoApprove(cb func(Message)) {
	h.mu.Lock()
	h.onAutoApprove = cb
	h.mu.Unlock()
}

func (h *Hub) SetRequireAgentApproval(require bool) {
	h.mu.Lock()
	h.requireAgentApproval = require
	h.mu.Unlock()
}

func (h *Hub) SetOnInboundDM(cb func(fromNodeID string)) {
	h.mu.Lock()
	h.onInboundDM = cb
	h.mu.Unlock()
}

func (h *Hub) NotifyAgentComplete(messageID string) {
	h.mu.RLock()
	var msg *Message
	if m, ok := h.recentAgentMsgs[messageID]; ok {
		msg = &m
	} else {
		for i := range h.messages {
			if h.messages[i].ID == messageID {
				msg = &h.messages[i]
				break
			}
		}
	}
	h.mu.RUnlock()
	if msg == nil {
		debug.Log("lanchat", "NotifyAgentComplete: message %s not found, no completed receipt sent", messageID)
		return
	}
	safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(*msg, StatusCompleted, "") })
}

func (h *Hub) NotifyAgentNotCompleted(messageID string) {
	h.mu.RLock()
	var msg *Message
	if m, ok := h.recentAgentMsgs[messageID]; ok {
		msg = &m
	} else {
		for i := range h.messages {
			if h.messages[i].ID == messageID {
				msg = &h.messages[i]
				break
			}
		}
	}
	h.mu.RUnlock()
	if msg == nil {
		debug.Log("lanchat", "NotifyAgentNotCompleted: message %s not found, no receipt sent", messageID)
		return
	}
	safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(*msg, StatusRejected, "") })
}

func (h *Hub) handleUDPEnvelope(env udpEnvelope, remoteAddr net.Addr) {

	if env.Type == "ack" && env.ACKID != "" {
		return
	}

	switch env.Type {
	case "leave":
		var p Participant
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			debug.Log("lanchat-udp", "leave payload parse error: %v", err)
			return
		}
		debug.Log("lanchat", "received leave from %s (%s)", p.NodeID, p.HumanNick)
		h.mu.Lock()
		if existing, ok := h.peers[p.NodeID]; ok {

			existing.LastSeen = time.Now().Add(-offlineNotifyDelay * 2).Unix()
			existing.lastOfflineTime = time.Now().UnixNano()
			h.peers[p.NodeID] = existing
		}
		h.mu.Unlock()
		h.UpdatePeers(h.Participants())
		return

	case "message":
		var msg Message
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			debug.Log("lanchat-udp", "message payload parse error: %v", err)
			return
		}

		h.mu.RLock()
		myNodeID := h.nodeID
		h.mu.RUnlock()
		if msg.ToNodeID != "" && msg.ToNodeID != myNodeID {
			return
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

func (h *Hub) SetUDPTransport(t *UDPTransport) {
	h.udpTransport = t
}

func (h *Hub) postToPeerWithFallback(ctx context.Context, endpoint string, msgType string, payload interface{}) error {

	if err := h.postToPeerTCPDirect(ctx, endpoint, msgType, payload); err == nil {
		return nil
	}

	if h.udpTransport == nil {
		return fmt.Errorf("tcp failed and no udp transport")
	}

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
		APIKey:   h.APIKey(),
		FromNode: h.nodeID,
		Payload:  payloadData,
	}

	udpAddr := &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	if err := h.udpTransport.SendUnicast(ctx, udpAddr, env); err == nil {
		debug.Log("lanchat", "delivered via udp unicast to %s", host)
		return nil
	}

	if err := h.udpTransport.SendMulticast(env); err == nil {
		debug.Log("lanchat", "delivered via udp multicast")
		return nil
	}

	return fmt.Errorf("all transports failed for %s", endpoint)
}

func parseHostPort(endpoint string) (string, int, error) {

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
	req.Header.Set("X-API-Key", h.APIKey())
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

func (h *Hub) handleReceiveMessageData(msg Message, source string) {

	if msg.FromNodeID == h.nodeID {
		return
	}

	h.mu.Lock()
	dedup := h.hasMessageLocked(msg.ID)
	h.mu.Unlock()
	if dedup {
		return
	}
	h.HandleIncomingMessage(msg)
}

func (h *Hub) handleReceiveReceiptData(r Receipt) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.storeReceiptLocked(r)
	if h.onReceipt != nil {
		rc := r
		safego.Go("lanchat.onReceipt", func() { h.onReceipt(rc) })
	}
}

func (h *Hub) handleNickChangeData(nc NickChange) {
	h.mu.Lock()
	peer, ok := h.peers[nc.NodeID]
	if !ok {
		h.mu.Unlock()
		return
	}
	oldNick := peer.HumanNick
	if nc.HumanNick != "" {
		peer.HumanNick = nc.HumanNick
	}
	if nc.AgentNick != "" {
		peer.AgentNick = nc.AgentNick
	}
	if nc.Role != "" {
		peer.Role = nc.Role
	}
	if nc.Team != "" {
		peer.Team = nc.Team
	}
	callback := h.onNickChange
	h.mu.Unlock()
	if callback != nil {
		nodeID := nc.NodeID
		newNick := nc.HumanNick
		safego.Go("lanchat.onNickChange", func() { callback(nodeID, oldNick, newNick) })
	}
}

func (h *Hub) storeReceiptLocked(r Receipt) {
	if h.receipts == nil {
		h.receipts = make(map[string]Receipt)
	}
	if _, exists := h.receipts[r.MessageID]; !exists {
		h.receiptOrder = append(h.receiptOrder, r.MessageID)
	}
	h.receipts[r.MessageID] = r
	if len(h.receiptOrder) > receiptCap {
		evict := h.receiptOrder[0]
		h.receiptOrder = h.receiptOrder[1:]
		delete(h.receipts, evict)
	}
}

func (h *Hub) markSeenLocked(msgID string) {
	if h.seenMsgIDs == nil {
		h.seenMsgIDs = make(map[string]bool)
	}
	if h.seenMsgIDs[msgID] {
		return
	}
	h.seenMsgIDs[msgID] = true
	h.seenMsgOrder = append(h.seenMsgOrder, msgID)
	if len(h.seenMsgOrder) > seenMsgCap {
		evict := h.seenMsgOrder[0]
		h.seenMsgOrder = h.seenMsgOrder[1:]
		delete(h.seenMsgIDs, evict)
	}
}

func (h *Hub) recordAgentMsgLocked(msg Message) {
	if h.recentAgentMsgs == nil {
		h.recentAgentMsgs = make(map[string]Message)
	}
	if _, exists := h.recentAgentMsgs[msg.ID]; exists {
		return
	}
	h.recentAgentMsgs[msg.ID] = msg
	h.recentAgentMsgOrder = append(h.recentAgentMsgOrder, msg.ID)
	if len(h.recentAgentMsgOrder) > recentAgentMsgCap {
		evict := h.recentAgentMsgOrder[0]
		h.recentAgentMsgOrder = h.recentAgentMsgOrder[1:]
		delete(h.recentAgentMsgs, evict)
	}
}

type evictedPending struct {
	msg    Message
	reason string
}

func (h *Hub) prunePendingApprovalsLocked() []evictedPending {
	now := time.Now()
	kept := h.pendingApproval[:0]
	var removed []evictedPending
	for _, p := range h.pendingApproval {
		if now.Sub(p.Received) > pendingApprovalTTL {
			debug.Log("lanchat", "pending approval %s from %s expired after %s", p.Message.ID, p.Message.FromNick, pendingApprovalTTL)
			removed = append(removed, evictedPending{
				msg:    p.Message,
				reason: fmt.Sprintf("pending approval expired after %s", pendingApprovalTTL),
			})
			continue
		}
		kept = append(kept, p)
	}
	h.pendingApproval = kept

	for len(h.pendingApproval) >= maxPendingApprovals {
		oldest := h.pendingApproval[0]
		h.pendingApproval = h.pendingApproval[1:]
		debug.Log("lanchat", "pending approval queue full (cap %d), dropping oldest %s from %s", maxPendingApprovals, oldest.Message.ID, oldest.Message.FromNick)
		removed = append(removed, evictedPending{
			msg:    oldest.Message,
			reason: fmt.Sprintf("pending approval queue full (cap %d), oldest dropped", maxPendingApprovals),
		})
	}
	return removed
}

func (h *Hub) hasMessageLocked(msgID string) bool {
	for _, m := range h.messages {
		if m.ID == msgID {
			return true
		}
	}
	return false
}

func (h *Hub) Close() {
	h.closeOnce.Do(func() {

		h.mu.Lock()
		pending := append([]PendingAgentMsg(nil), h.pendingApproval...)
		h.pendingApproval = nil
		h.mu.Unlock()
		for _, p := range pending {
			pm := p
			safego.Go("lanchat.sendReceipt", func() { h.sendReceipt(pm.Message, StatusRejected, "node shutting down") })
		}

		h.mu.Lock()
		if h.probeCancel != nil {
			h.probeCancel()
			h.probeCancel = nil
		}
		h.mu.Unlock()

		h.mu.RLock()
		transport := h.udpTransport
		h.mu.RUnlock()

		if transport == nil {
			return
		}

		self := h.SelfParticipant()
		if payload, err := json.Marshal(self); err == nil {
			env := udpEnvelope{
				Type:    "leave",
				Payload: payload,
			}
			_ = transport.SendMulticast(env)
			debug.Log("lanchat", "broadcast leave to all peers")
		}

		transport.Stop()
	})
}
