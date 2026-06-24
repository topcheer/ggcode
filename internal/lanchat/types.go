// Package lanchat provides a decentralized LAN chat system built on top of
// the A2A HTTP server and mDNS discovery. Each ggcode node runs two roles:
// a human user and their agent. Messages are P2P (HTTP POST) with no central
// server.
package lanchat

import (
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
	Online    bool   `json:"online"`
	LastSeen  int64  `json:"last_seen"`

	// Internal (not serialized): tracks whether we already fired
	// onParticipantAdd for this peer. Prevents duplicate join
	// notifications when presence exchanges complete.
	notifiedJoin bool `json:"-"`
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
	ToNodeID   string `json:"to_node_id"`   // original sender (for DM routing on the receiving side)
	ToRole     string `json:"to_role"`      // original sender's role (human/agent)
	Timestamp  int64  `json:"timestamp"`
	Reason     string `json:"reason,omitempty"`
}

// NickChange broadcasts a nickname update to all peers.
type NickChange struct {
	NodeID    string `json:"node_id"`
	HumanNick string `json:"human_nick"`
	AgentNick string `json:"agent_nick"`
	Timestamp int64  `json:"timestamp"`
}

// maxHistoryPerSession is the maximum number of messages persisted per session.
const maxHistoryPerSession = 100

// ageOffline marks a participant offline if not seen within this duration.
// This is the app-level liveness check: if a peer hasn't responded to
// heartbeats or presence exchanges within this window, it's marked offline.
var ageOffline = 60 * time.Second

// presenceHeartbeat is how long without communication before we re-probe
// a peer via sendPresence. If the probe fails (peer's lanchat server is
// dead), LastSeen stays stale and after ageOffline the peer goes offline.
var presenceHeartbeat = 30 * time.Second
