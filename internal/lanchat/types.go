// Package lanchat provides a decentralized LAN chat system built on top of
// the A2A HTTP server and mDNS discovery. Each ggcode node runs two roles:
// a human user and their agent. Messages are P2P (HTTP POST) with no central
// server.
package lanchat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Role constants.
const (
	RoleHuman = "human"
	RoleAgent = "agent"
)

// Receipt status constants.
const (
	StatusDelivered  = "delivered"
	StatusPending    = "pending"    // waiting for host approval
	StatusApproved   = "approved"   // host approved, agent about to run
	StatusProcessing = "processing" // agent is running
	StatusCompleted  = "completed"  // agent run finished
	StatusRejected   = "rejected"
)

// Participant represents one of the two roles on a node.
type Participant struct {
	NodeID    string `json:"node_id"`
	HumanNick string `json:"human_nick"`
	AgentNick string `json:"agent_nick"`
	Mode      string `json:"mode"` // "tui", "gui", "daemon"
	Endpoint  string `json:"endpoint"`
	Role      string `json:"role"` // user-defined role, e.g. "frontend", "backend", "devops"
	Team      string `json:"team"` // user-defined team, e.g. "platform", "mobile" (default: "dev-team")
	Online    bool   `json:"online"`
	LastSeen  int64  `json:"last_seen"`

	// Workspace & project info (populated via presence exchange).
	Workspace   string   `json:"workspace,omitempty"`    // full path to working directory
	ProjectName string   `json:"project_name,omitempty"` // basename or git remote name
	Languages   []string `json:"languages,omitempty"`    // e.g. ["go", "typescript"]
	Frameworks  []string `json:"frameworks,omitempty"`   // e.g. ["npm", "flutter"]
	HasGit      bool     `json:"has_git,omitempty"`
	HasTests    bool     `json:"has_tests,omitempty"`

	// AgentBusy indicates whether the agent on this node is currently
	// processing a task. Propagated via presence exchange so peers know
	// which agents are available and which are occupied.
	AgentBusy bool `json:"agent_busy,omitempty"`

	// UDPCapable indicates this node supports UDP transport for lanchat
	// messages. When true, peers can fallback to UDP unicast/multicast
	// if TCP is blocked by a firewall.
	UDPCapable bool `json:"udp_capable,omitempty"`

	// Internal (not serialized): tracks notification state to prevent
	// excessive online/offline churn.
	notifiedJoin    bool  `json:"-"` // already fired onParticipantAdd
	lastOfflineTime int64 `json:"-"` // unix nanoseconds when peer went offline (0 = never)
	notifiedLeave   bool  `json:"-"` // already fired leave notification for current offline period
}

// Message is a chat message exchanged between nodes.
type Message struct {
	ID          string       `json:"id"`
	FromNodeID  string       `json:"from_node_id"`
	FromRole    string       `json:"from_role"` // "human" or "agent"
	FromNick    string       `json:"from_nick"`
	ToNodeID    string       `json:"to_node_id"` // empty = broadcast
	ToRole      string       `json:"to_role"`    // "human", "agent" (for direct)
	Content     string       `json:"content"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Timestamp   int64        `json:"timestamp"` // unix ms
}

// IsBroadcast returns true if this is a broadcast message (no specific recipient).
func (m Message) IsBroadcast() bool {
	return m.ToNodeID == ""
}

// IsDirectToAgent returns true if this message is directed at an agent role.
func (m Message) IsDirectToAgent() bool {
	return m.ToNodeID != "" && m.ToRole == RoleAgent
}

// Attachment represents a file attachment. The sender hosts the file and
// provides a URL; the receiver downloads it on demand.
type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
	URL      string `json:"url"` // http://sender:port/lanchat/attach/<id>
}

// Receipt is an acknowledgement sent back to the original sender.
type Receipt struct {
	MessageID  string `json:"message_id"`
	Status     string `json:"status"`
	FromNodeID string `json:"from_node_id"` // node reporting the receipt (the original receiver)
	FromNick   string `json:"from_nick"`    // human or agent nick of the node reporting the receipt
	FromRole   string `json:"from_role"`    // "human" or "agent" — which role the receipt is for
	ToNodeID   string `json:"to_node_id"`   // original sender (for DM routing on the receiving side)
	ToRole     string `json:"to_role"`      // original sender's role (human/agent)
	Timestamp  int64  `json:"timestamp"`
	Reason     string `json:"reason,omitempty"`
}

// NickChange broadcasts a nickname/role update to all peers.
type NickChange struct {
	NodeID    string `json:"node_id"`
	HumanNick string `json:"human_nick"`
	AgentNick string `json:"agent_nick"`
	Role      string `json:"role"` // new role
	Team      string `json:"team"` // new team
	Timestamp int64  `json:"timestamp"`
}

// maxHistoryPerSession is the maximum number of messages persisted per session.
const maxHistoryPerSession = 100

// ageOffline marks a participant offline if not seen within this duration.
// This is the app-level liveness check: if a peer hasn't responded to
// heartbeats or presence exchanges within this window, it's marked offline.
// Set to 3 minutes (was 60s) to tolerate transient probe failures that
// caused excessive online/offline cycling.
var ageOffline = 3 * time.Minute

// presenceHeartbeat is how long without communication before we re-probe
// a peer via sendPresence. If the probe fails (peer's lanchat server is
// dead), LastSeen stays stale and after ageOffline the peer goes offline.
var presenceHeartbeat = 30 * time.Second

// peerDeleteAfter is how long a peer must be unreachable before it's
// removed from the peers map entirely. Until deletion, the peer's
// notifiedJoin flag is preserved, preventing duplicate "is online"
// notifications on recovery from transient offline periods.
var peerDeleteAfter = 10 * time.Minute

// offlineNotifyDelay is the grace period before firing a leave notification.
// When a peer goes offline (LastSeen stale), we wait this long before
// notifying. If the peer recovers within this window, no notification
// is fired — absorbing transient blips.
var offlineNotifyDelay = 30 * time.Second

// maxArchiveEntries is the maximum number of archived peers kept in the
// ring buffer. When full, the oldest entry is evicted (FIFO).
const maxArchiveEntries = 500

// UDP transport constants.
const (
	// udpMaxPayload is the maximum payload size for a single UDP datagram
	// (before compression). Messages larger than this after gzip are split
	// into fragments.
	udpMaxPayload = 32 * 1024 // 32 KB

	// udpACKTimeout is how long the sender waits for an ACK before retrying.
	udpACKTimeout = 2 * time.Second

	// udpMaxRetries is the max number of retransmissions for unicast UDP.
	udpMaxRetries = 2

	// udpFragmentTimeout is how long the receiver waits for all fragments
	// of a multi-fragment message before discarding the partial assembly.
	udpFragmentTimeout = 5 * time.Second

	// udpFailThreshold is the number of consecutive failures before marking
	// a transport as unavailable for a peer.
	udpFailThreshold = 3

	// udpMulticastAddr is the multicast group for UDP fallback transport.
	// Uses the same group as mDNS discovery for simplicity.
	udpMulticastAddr = "224.0.0.251:5354" // separate port from mDNS (5353)
)

// udpEnvelope is the wire format for UDP lanchat messages. Unlike HTTP,
// UDP doesn't have URL paths to distinguish message types, so we use a
// unified envelope with a type field.
type udpEnvelope struct {
	Type     string          `json:"type"`      // "message", "presence", "nick", "receipt", "ack"
	APIKey   string          `json:"api_key"`   // community key for auth
	FromNode string          `json:"from_node"` // sender node ID
	Payload  json.RawMessage `json:"payload"`   // type-specific JSON

	// Fragmentation fields (only used when a message is split)
	FragmentID    string `json:"fragment_id,omitempty"`    // unique ID for the fragment set
	FragmentTotal int    `json:"fragment_total,omitempty"` // total number of fragments
	FragmentSeq   int    `json:"fragment_seq,omitempty"`   // sequence number (0-based)
	IsFragment    bool   `json:"is_fragment,omitempty"`    // true if this is a fragment

	// ACK fields (only used for type="ack")
	ACKID string `json:"ack_id,omitempty"` // message ID being acknowledged
}

// peerHealth tracks transport availability for each peer.
// Updated by recordTransportResult, read by shouldTryTCP.
type peerHealth struct {
	tcpOK      bool
	tcpFail    int       // consecutive failures
	tcpRetryAt time.Time // when to retry TCP after marking it down
	lastTCP    time.Time
	lastUDP    time.Time
	lastMcast  time.Time
}

const (
	// tcpRetryInterval: how long to skip TCP before probing again.
	// In LAN environments, TCP either works or it doesn't — no partial degradation.
	// One failure = TCP down. Probed again by sendPresence heartbeat every ~30s.
	tcpRetryInterval = 30 * time.Second
)

// ArchivedPeer is a snapshot of a participant stored when the peer is
// deleted from the active peers map (after peerDeleteAfter). This allows
// long-running agents to correlate a returning peer (new NodeID) with its
// previous identity (same team + role, or same nicks).
type ArchivedPeer struct {
	NodeID      string   `json:"node_id"`
	HumanNick   string   `json:"human_nick"`
	AgentNick   string   `json:"agent_nick"`
	Role        string   `json:"role"`
	Team        string   `json:"team"`
	Workspace   string   `json:"workspace,omitempty"`
	ProjectName string   `json:"project_name,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	LastSeen    int64    `json:"last_seen"`
	ArchivedAt  int64    `json:"archived_at"` // unix seconds when the peer was archived
}

// DetectWorkspaceMeta scans the working directory for language/framework
// signals and returns a WorkspaceMeta suitable for presence exchange.
// This is a lightweight version that doesn't import the a2a package.
func DetectWorkspaceMeta(dir string) WorkspaceMeta {
	meta := WorkspaceMeta{
		Workspace:   dir,
		ProjectName: filepath.Base(dir),
	}

	// Check for .git
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		meta.HasGit = true
	}

	// Quick scan for languages (max depth 3, max 5000 entries)
	rootDepth := strings.Count(filepath.Clean(dir), string(filepath.Separator))
	langSet := make(map[string]bool)
	visited := 0
	const maxEntries = 5000
	const maxDepth = 3

	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		visited++
		if visited > maxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			base := strings.ToLower(d.Name())
			// Skip common noise directories
			switch base {
			case "node_modules", ".git", "vendor", ".next", "dist", "build",
				"target", "__pycache__", ".cache", ".venv", "venv":
				return filepath.SkipDir
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go":
			langSet["go"] = true
		case ".ts", ".tsx":
			langSet["typescript"] = true
		case ".js", ".jsx":
			langSet["javascript"] = true
		case ".py":
			langSet["python"] = true
		case ".rs":
			langSet["rust"] = true
		case ".java":
			langSet["java"] = true
		case ".dart":
			langSet["dart"] = true
		case ".rb":
			langSet["ruby"] = true
		case ".c", ".h":
			langSet["c"] = true
		case ".cpp", ".cc", ".hpp":
			langSet["cpp"] = true
		case "_test.go", ".test.ts", ".test.js", ".test.dart":
			meta.HasTests = true
		}
		// Check for test file patterns
		name := strings.ToLower(d.Name())
		if strings.Contains(name, "_test.go") || strings.Contains(name, ".test.") ||
			strings.Contains(name, ".spec.") || strings.Contains(name, "_test.") {
			meta.HasTests = true
		}
		return nil
	})

	for lang := range langSet {
		meta.Languages = append(meta.Languages, lang)
	}

	// Detect frameworks from config files
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "npm")
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "go")
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "cargo")
	}
	if _, err := os.Stat(filepath.Join(dir, "pubspec.yaml")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "flutter")
	}
	if _, err := os.Stat(filepath.Join(dir, "requirements.txt")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "pip")
	}
	if _, err := os.Stat(filepath.Join(dir, "pom.xml")); err == nil {
		meta.Frameworks = append(meta.Frameworks, "maven")
	}

	return meta
}
