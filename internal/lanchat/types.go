package lanchat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	RoleHuman = "human"
	RoleAgent = "agent"
)

const (
	StatusDelivered  = "delivered"
	StatusPending    = "pending"
	StatusApproved   = "approved"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusRejected   = "rejected"
)

type Participant struct {
	NodeID    string `json:"node_id"`
	HumanNick string `json:"human_nick"`
	AgentNick string `json:"agent_nick"`
	Mode      string `json:"mode"`
	Endpoint  string `json:"endpoint"`
	Role      string `json:"role"`
	Team      string `json:"team"`
	Online    bool   `json:"online"`
	LastSeen  int64  `json:"last_seen"`

	Workspace   string   `json:"workspace,omitempty"`
	ProjectName string   `json:"project_name,omitempty"`
	Languages   []string `json:"languages,omitempty"`
	Frameworks  []string `json:"frameworks,omitempty"`
	HasGit      bool     `json:"has_git,omitempty"`
	HasTests    bool     `json:"has_tests,omitempty"`

	AgentBusy bool `json:"agent_busy,omitempty"`

	AgentStatus      string `json:"agent_status,omitempty"`
	AgentStatusSince int64  `json:"agent_status_since,omitempty"`

	Model string `json:"model,omitempty"`

	UDPCapable bool `json:"udp_capable,omitempty"`

	notifiedJoin    bool  `json:"-"`
	lastOfflineTime int64 `json:"-"`
	notifiedLeave   bool  `json:"-"`
}

type Message struct {
	ID          string       `json:"id"`
	FromNodeID  string       `json:"from_node_id"`
	FromRole    string       `json:"from_role"`
	FromNick    string       `json:"from_nick"`
	ToNodeID    string       `json:"to_node_id"`
	ToRole      string       `json:"to_role"`
	Content     string       `json:"content"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Timestamp   int64        `json:"timestamp"`
	PubKey      string       `json:"pub_key,omitempty"`
	Sig         string       `json:"sig,omitempty"`

	Verified bool   `json:"-"`
	SignerFP string `json:"-"`
}

func (m Message) IsBroadcast() bool {
	return m.ToNodeID == ""
}

func (m Message) IsDirectToAgent() bool {
	return m.ToNodeID != "" && m.ToRole == RoleAgent
}

type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
	URL      string `json:"url"`
}

type Receipt struct {
	MessageID  string `json:"message_id"`
	Status     string `json:"status"`
	FromNodeID string `json:"from_node_id"`
	FromNick   string `json:"from_nick"`
	FromRole   string `json:"from_role"`
	ToNodeID   string `json:"to_node_id"`
	ToRole     string `json:"to_role"`
	Timestamp  int64  `json:"timestamp"`
	Reason     string `json:"reason,omitempty"`
}

type NickChange struct {
	NodeID    string `json:"node_id"`
	HumanNick string `json:"human_nick"`
	AgentNick string `json:"agent_nick"`
	Role      string `json:"role"`
	Team      string `json:"team"`
	Timestamp int64  `json:"timestamp"`
}

const maxHistoryPerSession = 100

var ageOffline = 3 * time.Minute

var presenceHeartbeat = 30 * time.Second

var peerDeleteAfter = 10 * time.Minute

var offlineNotifyDelay = 30 * time.Second

const maxArchiveEntries = 500

const (
	udpMaxPayload = 32 * 1024

	udpACKTimeout = 2 * time.Second

	udpMaxRetries = 2

	udpFragmentTimeout = 5 * time.Second

	udpMulticastAddr = "224.0.0.251:5354"
)

type udpEnvelope struct {
	Type     string          `json:"type"`
	APIKey   string          `json:"api_key"`
	FromNode string          `json:"from_node"`
	Payload  json.RawMessage `json:"payload"`

	FragmentID    string `json:"fragment_id,omitempty"`
	FragmentTotal int    `json:"fragment_total,omitempty"`
	FragmentSeq   int    `json:"fragment_seq,omitempty"`
	IsFragment    bool   `json:"is_fragment,omitempty"`

	ACKID string `json:"ack_id,omitempty"`
}

type peerHealth struct {
	tcpOK      bool
	tcpFail    int
	tcpRetryAt time.Time
	lastTCP    time.Time
	lastUDP    time.Time
	lastMcast  time.Time
}

const (
	tcpRetryInterval = 30 * time.Second
)

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
	ArchivedAt  int64    `json:"archived_at"`
}

func DetectWorkspaceMeta(dir string) WorkspaceMeta {
	meta := WorkspaceMeta{
		Workspace:   dir,
		ProjectName: filepath.Base(dir),
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		meta.HasGit = true
	}

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
