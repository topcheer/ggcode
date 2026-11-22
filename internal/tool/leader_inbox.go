package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// maxLeaderInboxPerSession bounds one session's leader inbox. A session
// whose leader never drains must not let workers grow memory without
// limit; the push side gets a deterministic, actionable rejection instead.
const maxLeaderInboxPerSession = 64

// InboxMessage is one worker->leader report pushed mid-run.
type InboxMessage struct {
	Seq       uint64    `json:"seq"`
	From      string    `json:"from"`
	Kind      string    `json:"kind"` // "subagent" (agent-*) or "teammate" (tm-*)
	Summary   string    `json:"summary,omitempty"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// LeaderInboxStore is the session-scoped worker->leader report channel.
// Every queue is keyed by the session ID resolved inside the tools at
// execution time - the key is never caller-supplied - so a worker running
// in session A can only reach session A's inbox and a leader can only
// drain its own session's inbox; cross-session reads are impossible by
// construction. The store is process-memory only, so queues never leak
// into a NEW session (a fresh process starts empty); DropSession clears
// stale queues on same-process session switches (TUI rebind path).
type LeaderInboxStore struct {
	mu      sync.Mutex
	queues  map[string][]InboxMessage
	nextSeq uint64
}

func NewLeaderInboxStore() *LeaderInboxStore {
	return &LeaderInboxStore{queues: make(map[string][]InboxMessage)}
}

// Push enqueues one report into the inbox of the given session. Pushes
// without a resolvable session id are rejected: session scoping is the
// hard requirement, so an unscoped message is never accepted.
func (s *LeaderInboxStore) Push(sessionID, from, kind, summary, content string) (InboxMessage, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return InboxMessage{}, fmt.Errorf("leader inbox: no session id resolved - report rejected (session scoping is mandatory)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[sid]
	if len(q) >= maxLeaderInboxPerSession {
		debug.Log("tool", "leader inbox full for session %s: %d/%d messages", sid, len(q), maxLeaderInboxPerSession)
		return InboxMessage{}, fmt.Errorf("leader inbox for this session is full (%d/%d): the leader must drain it via leader_inbox before more reports are accepted", len(q), maxLeaderInboxPerSession)
	}
	s.nextSeq++
	msg := InboxMessage{
		Seq:       s.nextSeq,
		From:      strings.TrimSpace(from),
		Kind:      kind,
		Summary:   strings.TrimSpace(summary),
		Content:   content,
		Timestamp: time.Now(),
	}
	s.queues[sid] = append(q, msg)
	return msg, nil
}

// Drain returns and consumes every pending report of ONE session
// (read-on-consume lifecycle: the pull is the ack, so a session inbox can
// never outlive its last read by much; whatever the leader never reads is
// dropped with the process or at session rebind via DropSession).
func (s *LeaderInboxStore) Drain(sessionID string) []InboxMessage {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.queues[sid]
	delete(s.queues, sid)
	return msgs
}

// Len reports how many reports are pending for one session.
func (s *LeaderInboxStore) Len(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queues[sessionID])
}

// DropSession discards a session's queue entirely. Called on same-process
// session switches (TUI rebind) so reports pushed by torn-down workers of
// the previous session are never surfaced to the new session's leader.
func (s *LeaderInboxStore) DropSession(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.queues[sid]); n > 0 {
		debug.Log("tool", "leader inbox dropped %d undelivered report(s) for ended session %s", n, sid)
	}
	delete(s.queues, sid)
}

// ReportToLeaderTool is the worker-side PUSH half of the session-scoped
// worker->leader report channel. Sub-agents (agent-*) and teammates
// (tm-*) use it to push a mid-run report into their leader's session
// inbox. The instance registered on the leader's own registry refuses to
// run (leader cannot report to itself); only the clones handed out in
// worker toolsets (registry.Clone at spawn time) execute.
type ReportToLeaderTool struct {
	// Inbox is the process-level, session-keyed store.
	Inbox *LeaderInboxStore
	// SessionID resolves the CURRENT session id at execution time on the
	// leader instance; Clone() freezes it so a worker spawned in session A
	// keeps reporting to session A even if the leader later rebinds.
	SessionID func() string
	// worker is false on the leader's registered instance and true on
	// every clone - it is the identity boundary, not a caller-supplied flag.
	worker bool
}

func (t ReportToLeaderTool) Name() string { return "report_to_leader" }

func (t ReportToLeaderTool) Description() string {
	return "Push a mid-run report into THIS session's leader inbox (session-scoped, pull-based: the leader reads it on its next leader_inbox call; it is NOT interrupted). For sub-agents (agent-*) and teammates (tm-*) only - the session leader itself cannot call it. Use it when you finish early, hit a blocker, or need a mid-run decision, instead of staying silent until your final report. Delivery confirms enqueue only; your final result still goes through the normal result channel."
}

func (t ReportToLeaderTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"message": {
			"type": "string",
			"description": "The report content: early completion notice, blocker description, or the decision you need from the leader. Keep it self-contained - the leader has not seen your conversation."
		},
		"summary": {
			"type": "string",
			"description": "Optional one-line summary shown in the leader's inbox listing."
		},
		"from": {
			"type": "string",
			"description": "Your worker id (e.g. 'agent-1' or 'tm-2') so the leader can attribute the report. Optional but recommended."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"message",
		"description"
	]
}`)
}

func (t ReportToLeaderTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if !t.worker {
		return Result{IsError: true, Content: "report_to_leader is worker-only (agent-* / tm-*): the session leader cannot report to itself; leaders drain worker reports with leader_inbox"}, nil
	}
	if t.Inbox == nil {
		return Result{IsError: true, Content: "report_to_leader: no leader inbox available (not wired in this runtime)"}, nil
	}
	var args struct {
		Message     string `json:"message"`
		Summary     string `json:"summary"`
		From        string `json:"from"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Message) == "" {
		return Result{IsError: true, Content: "message is required"}, nil
	}
	sid := ""
	if t.SessionID != nil {
		sid = t.SessionID()
	}
	kind := "worker"
	from := strings.TrimSpace(args.From)
	if from == "" {
		from = "worker"
	}
	if strings.HasPrefix(from, "tm-") {
		kind = "teammate"
	} else if strings.HasPrefix(from, "agent-") {
		kind = "subagent"
	}
	msg, err := t.Inbox.Push(sid, from, kind, args.Summary, args.Message)
	if err != nil {
		debug.Log("tool", "report_to_leader push failed (session %s, from %s): %v", sid, from, err)
		return Result{IsError: true, Content: fmt.Sprintf("report_to_leader: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Report #%d queued for this session's leader (inbox: %d pending). Pull-based: the leader reads it on its next leader_inbox call - keep working or finish normally; do not wait for a reply.\n", msg.Seq, t.Inbox.Len(sid))}, nil
}

// Clone implements Cloner: worker toolsets are built via Registry.Clone()
// from the leader's registry, so every clone is a worker instance bound to
// the session that was current at clone time.
func (t ReportToLeaderTool) Clone() Tool {
	return ReportToLeaderTool{
		Inbox:     t.Inbox,
		SessionID: frozenSessionID(t.SessionID),
		worker:    true,
	}
}

// LeaderInboxTool is the leader-side PULL half of the channel. It drains
// ONLY the pending reports of the leader's current session and consumes
// them on read; worker toolsets receive a clone that refuses to run, so a
// worker can never read (or clear) the leader's inbox.
type LeaderInboxTool struct {
	Inbox     *LeaderInboxStore
	SessionID func() string
	// leader is true only on the instance registered for the leader.
	leader bool
}

func (t LeaderInboxTool) Name() string { return "leader_inbox" }

func (t LeaderInboxTool) Description() string {
	return "Drain THIS session's pending worker->leader reports (pushed by sub-agents agent-* and teammates tm-* via report_to_leader). Session-scoped: only messages pushed within the current session are returned, never other sessions'. Messages are consumed on read (pull = ack). Call it between your own tool batches to pick up mid-run reports instead of only polling final results."
}

func (t LeaderInboxTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"description"
	]
}`)
}

func (t LeaderInboxTool) Execute(_ context.Context, input json.RawMessage) (Result, error) {
	if !t.leader {
		return Result{IsError: true, Content: "leader_inbox is leader-only: worker toolsets receive a clone that cannot read the leader's inbox; workers push with report_to_leader"}, nil
	}
	if t.Inbox == nil {
		return Result{IsError: true, Content: "leader_inbox: no leader inbox available (not wired in this runtime)"}, nil
	}
	var args struct {
		Description string `json:"description"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
		}
	}
	sid := ""
	if t.SessionID != nil {
		sid = t.SessionID()
	}
	if strings.TrimSpace(sid) == "" {
		return Result{IsError: true, Content: "leader_inbox: no session id resolved - cannot scope the inbox read"}, nil
	}
	msgs := t.Inbox.Drain(sid)
	if len(msgs) == 0 {
		return Result{Content: "No pending worker reports in this session's leader inbox.\n"}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d worker report(s) consumed from this session's inbox:\n", len(msgs))
	for _, m := range msgs {
		fmt.Fprintf(&b, "\n[#%d] %s (%s) %s\n", m.Seq, m.From, m.Kind, m.Timestamp.Format("15:04:05"))
		if m.Summary != "" {
			fmt.Fprintf(&b, "summary: %s\n", m.Summary)
		}
		fmt.Fprintf(&b, "%s\n", m.Content)
	}
	return Result{Content: b.String()}, nil
}

// Clone implements Cloner: worker clones refuse to drain (leader=false)
// and stay bound to the session frozen at clone time.
func (t LeaderInboxTool) Clone() Tool {
	return LeaderInboxTool{
		Inbox:     t.Inbox,
		SessionID: frozenSessionID(t.SessionID),
		leader:    false,
	}
}

// frozenSessionID evaluates a live session getter once and returns a
// constant getter, so worker clones keep the session they were spawned in
// even after the leader rebinds to another session.
func frozenSessionID(getter func() string) func() string {
	sid := ""
	if getter != nil {
		sid = getter()
	}
	return func() string { return sid }
}
