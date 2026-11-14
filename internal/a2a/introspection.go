package a2a

import (
	"encoding/json"
	"time"
)

// #5597 runtime introspection v3. FINAL DESIGN RULING (user, 2026-11-14):
// all trust machinery REMOVED as over-engineering - same workspace has no
// sensitive faces. runtime/snapshot is a PLAIN a2a JSON-RPC method behind
// the server's existing first gate (X-API-Key if configured), identical
// trust to message/send. No .git/a2a.key, no HMAC/nonce/signature headers,
// no server-side redaction (raw verbatim is the requirement). The v1/v2
// HMAC helpers, domain key, and RedactSnapshotStrings are deleted.

// Depth tiers: brief = face headers/counters; full = verbatim content
// (conversation N turns, activity K events) with cursor pagination.
const (
	DepthBrief = "brief"
	DepthFull  = "full"
)

// SnapshotRequest is the caller-facing query shape (agent tool / CLI).
type SnapshotRequest struct {
	Depth      string   `json:"depth"`               // brief|full (default full)
	Faces      []string `json:"faces,omitempty"`     // optional subset filter
	CursorConv string   `json:"cursor_conversation"` // opaque cursor, conversation face
	CursorAct  string   `json:"cursor_activity"`     // opaque cursor, activity face
	LimitConv  int      `json:"limit_conversation"`  // override default 20
	LimitAct   int      `json:"limit_activity"`      // override default 50
}

// RuntimeStatus is face 1: one-glance "what is this seat".
type RuntimeStatus struct {
	Busy       bool    `json:"busy"`
	BusySince  string  `json:"busy_since,omitempty"`
	Spinner    string  `json:"spinner,omitempty"`
	Goal       string  `json:"goal,omitempty"`
	Model      string  `json:"model,omitempty"`
	Iteration  int     `json:"iteration,omitempty"`
	TokensIn   int64   `json:"tokens_in,omitempty"`
	TokensOut  int64   `json:"tokens_out,omitempty"`
	ContextPct float64 `json:"context_pct,omitempty"`
}

// RuntimeInflight is face 2: what is executing RIGHT NOW. P2 scope:
// the active sub-agent task of this seat (main-agent tool-call loop
// instrumentation remains a later refinement, spec §6).
type RuntimeInflight struct {
	Tool             string `json:"tool,omitempty"`
	ParamsHead       string `json:"params_head,omitempty"`
	StartedAt        string `json:"started_at,omitempty"`
	PendingApprovals int    `json:"pending_approvals,omitempty"`
}

// RuntimeConversationTurn is face 3 verbatim: role + full text.
type RuntimeConversationTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
	TS   string `json:"ts,omitempty"`
}

// RuntimeConversation is the conversation face with cursor pagination.
type RuntimeConversation struct {
	Turns      []RuntimeConversationTurn `json:"turns"`
	NextCursor string                    `json:"next_cursor,omitempty"`
	Truncated  bool                      `json:"truncated,omitempty"`
}

// RuntimeActivityEvent is face 4: one recent tool call/result.
type RuntimeActivityEvent struct {
	Tool string `json:"tool"`
	OK   bool   `json:"ok"`
	Size int    `json:"size,omitempty"`
	TS   string `json:"ts,omitempty"`
}

// RuntimeFileTouch is a touched file (read/edited) in the activity window.
type RuntimeFileTouch struct {
	Path string `json:"path"`
	Op   string `json:"op"` // read|edit|write
	TS   string `json:"ts,omitempty"`
}

// RuntimeActivity is the activity face with cursor pagination.
type RuntimeActivity struct {
	Events     []RuntimeActivityEvent `json:"events"`
	Files      []RuntimeFileTouch     `json:"files,omitempty"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	Truncated  bool                   `json:"truncated,omitempty"`
}

// RuntimeSubagent is a running sub-agent (children face; #5589 data in P2).
type RuntimeSubagent struct {
	ID       string `json:"id"`
	TaskHead string `json:"task_head,omitempty"`
	State    string `json:"state,omitempty"`
	// #5605 P2 additive fields (children face detail).
	Name        string `json:"name,omitempty"`
	Model       string `json:"model,omitempty"`
	Worktree    string `json:"worktree,omitempty"`
	CurrentTool string `json:"current_tool,omitempty"`
	ToolCalls   int    `json:"tool_calls,omitempty"`
	// #5637: recent live events so a PEER can watch a running sub-agent
	// in-flight via agent RPC (children face), same as the local
	// subagent_context tool. Bounded tail, oldest-first.
	Events []RuntimeSubagentEvent `json:"events,omitempty"`
}

// RuntimeSubagentEvent is one tail event of a running sub-agent, flattened
// from the in-memory ring: text output, tool call, or tool result.
type RuntimeSubagentEvent struct {
	Type string `json:"type"` // "text" | "tool" | "result" | "system"
	Head string `json:"head,omitempty"`
}

// RuntimeJob is a background command job (children face; P2 wiring).
type RuntimeJob struct {
	ID      string `json:"id"`
	CmdHead string `json:"cmd_head,omitempty"`
	State   string `json:"state,omitempty"`
}

// RuntimeChildren is face 5: subagents + background jobs.
type RuntimeChildren struct {
	Subagents []RuntimeSubagent `json:"subagents,omitempty"`
	Jobs      []RuntimeJob      `json:"jobs,omitempty"`
}

// RuntimeSchedule is face 6: crons + task board.
type RuntimeSchedule struct {
	Crons []RuntimeCron `json:"crons,omitempty"`
	Todos []RuntimeTodo `json:"todos,omitempty"`
}

// RuntimeCron is one scheduled job summary.
type RuntimeCron struct {
	ID       string `json:"id"`
	Cron     string `json:"cron,omitempty"`
	NextFire string `json:"next_fire,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
}

// RuntimeTodo is one task-board entry.
type RuntimeTodo struct {
	ID         string `json:"id"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}

// RuntimeEnv is face 7: workspace environment.
// RuntimeEnvService is one mounted integration surfaced in the env face
// (#5635): MCP servers, IM adapters, skill mounts. State is the runtime
// truth ("connected", "muted", ...) - verbatim, no interpretation.
type RuntimeEnvService struct {
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
	Tools int    `json:"tools,omitempty"`
}

type RuntimeEnv struct {
	Workspace  string              `json:"workspace,omitempty"`
	Branch     string              `json:"branch,omitempty"`
	Head       string              `json:"head,omitempty"`
	DirtyFiles int                 `json:"dirty_files,omitempty"`
	Provider   string              `json:"provider,omitempty"`
	MCPServers []RuntimeEnvService `json:"mcp_servers,omitempty"`
	IMAdapters []RuntimeEnvService `json:"im_adapters,omitempty"`
	Skills     []RuntimeEnvService `json:"skills,omitempty"`
	// #5635 P2: live mobile-share/tunnel connection state, same truth the
	// local `runtime` tool reports (RuntimeMobileInfo provider).
	Mobile *RuntimeMobileState `json:"mobile,omitempty"`
}

// RuntimeMobileState is the mobile share tunnel state in the env face.
type RuntimeMobileState struct {
	Connected bool   `json:"connected"`
	RelayURL  string `json:"relay_url,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// RuntimeSnapshotV3 is the runtime/snapshot result: seven faces, zero LLM,
// raw verbatim content (user ruling: same workspace = trust domain).
type RuntimeSnapshotV3 struct {
	Schema       string               `json:"schema"` // "v3"
	TakenAt      string               `json:"taken_at"`
	Depth        string               `json:"depth"`
	Status       *RuntimeStatus       `json:"status,omitempty"`
	Inflight     *RuntimeInflight     `json:"inflight,omitempty"`
	Conversation *RuntimeConversation `json:"conversation,omitempty"`
	Activity     *RuntimeActivity     `json:"activity,omitempty"`
	Children     *RuntimeChildren     `json:"children,omitempty"`
	Schedule     *RuntimeSchedule     `json:"schedule,omitempty"`
	Env          *RuntimeEnv          `json:"env,omitempty"`
}

// RuntimeIntrospectorV3 is injected by the assembly layer (cmd/ggcode
// root); the a2a server stays free of agent/lanchat imports.
type RuntimeIntrospectorV3 interface {
	RuntimeSnapshotV3(req SnapshotRequest) (*RuntimeSnapshotV3, error)
}

// RuntimeIntrospectorV3Func adapts a function to the interface.
type RuntimeIntrospectorV3Func func(req SnapshotRequest) (*RuntimeSnapshotV3, error)

func (f RuntimeIntrospectorV3Func) RuntimeSnapshotV3(req SnapshotRequest) (*RuntimeSnapshotV3, error) {
	return f(req)
}

// MethodRuntimeSnapshot is the plain JSON-RPC method name.
const MethodRuntimeSnapshot = "runtime/snapshot"

// NewRuntimeSnapshotHandlerV3 builds the plain method handler: no second
// gate, no nonce/signature - the legacy auth middleware has already run.
// The params (depth/faces/cursors/limits) arrive as the JSON-RPC params
// object; nil params default to full depth.
func NewRuntimeSnapshotHandlerV3(inst RuntimeIntrospectorV3, now func() time.Time) func(params json.RawMessage) (*RuntimeSnapshotV3, *JSONRPCError) {
	return func(params json.RawMessage) (*RuntimeSnapshotV3, *JSONRPCError) {
		if inst == nil {
			return nil, &JSONRPCError{Code: -32002, Message: "Introspection source unavailable"}
		}
		req := SnapshotRequest{Depth: DepthFull}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, ErrInvalidParams
			}
		}
		if req.Depth == "" {
			req.Depth = DepthFull
		}
		snap, err := inst.RuntimeSnapshotV3(req)
		if err != nil {
			return nil, &JSONRPCError{Code: -32002, Message: "Snapshot source error: " + err.Error()}
		}
		if snap == nil {
			return nil, &JSONRPCError{Code: -32002, Message: "Snapshot source unavailable"}
		}
		return snap, nil
	}
}
