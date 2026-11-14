package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// IntrospectPeerV3 requests a runtime/snapshot from a peer over the plain
// A2A JSON-RPC transport (#5597 v3: no HMAC/nonce headers - identical trust
// to message/send, the server's existing first gate applies).
func IntrospectPeerV3(ctx context.Context, endpoint, apiKey string, req SnapshotRequest) (*RuntimeSnapshotV3, error) {
	if req.Depth == "" {
		req.Depth = DepthFull
	}
	params, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	reqID, _ := json.Marshal(fmt.Sprintf("introspect-%d", time.Now().UnixNano()))
	body, err := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  MethodRuntimeSnapshot,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		hreq.Header.Set("X-API-Key", apiKey)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("a2a introspect: transport: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// #125/G4: actionable 401 diagnosis instead of a JSON decode error.
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, fmt.Errorf("a2a introspect: HTTP 401 unauthorized from %s (reason=%s); remediation: present the peer's configured credential (X-API-Key from config a2a.auth.api_key, or Authorization: Bearer); server accepts: %s", endpoint, resp.Header.Get("X-Auth-Reason"), resp.Header.Get("WWW-Authenticate"))
		}
		return nil, fmt.Errorf("a2a introspect: HTTP %d from %s", resp.StatusCode, endpoint)
	}
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("a2a introspect: decode: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("a2a introspect: peer error (code %d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	raw, err := json.Marshal(rpcResp.Result)
	if err != nil {
		return nil, err
	}
	var snap RuntimeSnapshotV3
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("a2a introspect: snapshot decode: %w", err)
	}
	return &snap, nil
}

// IntrospectTool is the agent-facing peer context client (#5597 v3):
// one hop, seven faces, verbatim. target="list" enumerates peers.
type IntrospectTool struct {
	registry  *Registry
	apiKey    string
	localTool *RemoteTool
}

func NewIntrospectTool(reg *Registry, apiKey, _ string) *IntrospectTool {
	rt := NewRemoteTool(reg, apiKey)
	return &IntrospectTool{registry: reg, apiKey: apiKey, localTool: rt}
}

// SetIdentityLookup wires the seat-identity bridge through to the internal
// resolver (#5616) so nick targets work for introspect too.
func (t *IntrospectTool) SetIdentityLookup(fn IdentityLookup) {
	t.localTool.SetIdentityLookup(fn)
}

func (t *IntrospectTool) Name() string { return "a2a_introspect" }

func (t *IntrospectTool) Description() string {
	return "Read a peer ggcode instance's full current context (seven faces: status / inflight / conversation verbatim with cursor / activity / children / schedule / env) in ONE hop over the A2A runtime/snapshot method. " +
		"Same-workspace trust domain: raw verbatim content, no redaction. Use target='list' to enumerate peers; depth='brief' for headers only; cursors paginate conversation/activity. " +
		"Use 'a2a_remote' to delegate work, 'lanchat' to talk."
}

func (t *IntrospectTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {
				"type": "string",
				"description": "Target seat: project name, seat nick in a shared-workspace fleet (e.g. 'dev_ggcode'), or 'name:port' to pin an exact instance; or 'list' to enumerate peers (default: this workspace only; set all=true to include other workspaces)."
			},
			"all": {
				"type": "boolean",
				"description": "With target='list': include instances from OTHER workspaces too (default lists only this workspace's fleet; foreign seats collapse to one summary line)."
			},
			"depth": {
				"type": "string",
				"enum": ["brief", "full"],
				"description": "brief = face headers/counters; full (default) = verbatim conversation (20 turns) + activity (50 events)."
			},
			"faces": {
				"type": "array",
				"items": {"type": "string", "enum": ["status","inflight","conversation","activity","children","schedule","env"]},
				"description": "Optional subset filter."
			},
			"cursor_conversation": {"type": "string", "description": "Opaque cursor from a previous conversation face."},
			"cursor_activity": {"type": "string", "description": "Opaque cursor from a previous activity face."}
		},
		"required": ["target"]
	}`)
}

func (t *IntrospectTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var params struct {
		Target     string   `json:"target"`
		All        bool     `json:"all"`
		Depth      string   `json:"depth"`
		Faces      []string `json:"faces"`
		CursorConv string   `json:"cursor_conversation"`
		CursorAct  string   `json:"cursor_activity"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("Invalid input: %v", err), IsError: true}, nil
	}
	if params.Target == "" || params.Target == "list" {
		return t.localTool.listInstances(ctx, params.All)
	}
	inst, err := t.localTool.FindInstance(params.Target)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	snap, err := IntrospectPeerV3(ctx, inst.Endpoint, t.apiKey, SnapshotRequest{
		Depth:      params.Depth,
		Faces:      params.Faces,
		CursorConv: params.CursorConv,
		CursorAct:  params.CursorAct,
	})
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	return tool.Result{Content: FormatSnapshotV3(params.Target, inst.Workspace, snap)}, nil
}

// FormatSnapshotV3 renders the eight faces for an agent consumer.
func FormatSnapshotV3(target, workspace string, s *RuntimeSnapshotV3) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Peer context: %s (%s) taken %s depth=%s\n", target, workspace, s.TakenAt, s.Depth))
	if s.Status != nil {
		st := s.Status
		busy := "idle"
		if st.Busy {
			busy = "BUSY since " + st.BusySince
			if st.Spinner != "" {
				busy += " (" + st.Spinner + ")"
			}
		}
		b.WriteString(fmt.Sprintf("status: %s | model=%s goal=%q iter=%d tokens=%d/%d ctx=%.0f%%\n",
			busy, st.Model, st.Goal, st.Iteration, st.TokensIn, st.TokensOut, st.ContextPct))
	}
	if s.Inflight != nil {
		b.WriteString(fmt.Sprintf("inflight: tool=%s params=%s started=%s pendingApprovals=%d\n",
			s.Inflight.Tool, s.Inflight.ParamsHead, s.Inflight.StartedAt, s.Inflight.PendingApprovals))
	}
	if s.Conversation != nil {
		b.WriteString(fmt.Sprintf("conversation: %d turns (next_cursor=%s truncated=%v)\n",
			len(s.Conversation.Turns), s.Conversation.NextCursor, s.Conversation.Truncated))
		for i, tr := range s.Conversation.Turns {
			txt := tr.Text
			if s.Depth == DepthBrief && len(txt) > 120 {
				txt = txt[:120] + "..."
			} else if len(txt) > 2000 {
				txt = txt[:2000] + "..."
			}
			b.WriteString(fmt.Sprintf("  [%s] %s\n", tr.Role, txt))
			if i >= 40 {
				b.WriteString("  ... (render cap; use cursors)\n")
				break
			}
		}
	}
	if s.Activity != nil {
		b.WriteString(fmt.Sprintf("activity: %d events, %d files (next_cursor=%s)\n",
			len(s.Activity.Events), len(s.Activity.Files), s.Activity.NextCursor))
		for i, ev := range s.Activity.Events {
			if i >= 20 {
				b.WriteString("  ... (render cap; use cursors)\n")
				break
			}
			status := "ok"
			if !ev.OK {
				status = "FAIL"
			}
			b.WriteString(fmt.Sprintf("  - %s %s %s\n", ev.TS, ev.Tool, status))
		}
		for i, f := range s.Activity.Files {
			if i >= 15 {
				break
			}
			b.WriteString(fmt.Sprintf("  file[%s]: %s\n", f.Op, f.Path))
		}
	}
	if s.Children != nil {
		b.WriteString(fmt.Sprintf("children: %d subagents, %d jobs\n", len(s.Children.Subagents), len(s.Children.Jobs)))
		for _, sa := range s.Children.Subagents {
			b.WriteString(fmt.Sprintf("  - subagent %s: %s (%s)\n", sa.ID, sa.TaskHead, sa.State))
		}
		for _, j := range s.Children.Jobs {
			b.WriteString(fmt.Sprintf("  - job %s: %s (%s)\n", j.ID, j.CmdHead, j.State))
		}
	}
	if s.Schedule != nil {
		b.WriteString(fmt.Sprintf("schedule: %d crons, %d todos\n", len(s.Schedule.Crons), len(s.Schedule.Todos)))
		for i, c := range s.Schedule.Crons {
			if i >= 5 {
				break
			}
			b.WriteString(fmt.Sprintf("  cron %s %s next=%s\n", c.ID, c.Cron, c.NextFire))
		}
		for i, tk := range s.Schedule.Todos {
			if i >= 5 {
				break
			}
			b.WriteString(fmt.Sprintf("  todo [%s] %s\n", tk.Status, tk.Subject))
		}
	}
	if s.Env != nil {
		b.WriteString(fmt.Sprintf("env: workspace=%s branch=%s head=%s dirty=%d provider=%s\n",
			s.Env.Workspace, s.Env.Branch, s.Env.Head, s.Env.DirtyFiles, s.Env.Provider))
	}
	if s.Plan != nil {
		b.WriteString(fmt.Sprintf("plan: %d todos, %d goal nodes, %d board tasks\n",
			len(s.Plan.Todos), len(s.Plan.GoalNodes), len(s.Plan.Tasks)))
		for i, t := range s.Plan.Todos {
			if i >= 10 {
				b.WriteString("  ... (todo render cap)\n")
				break
			}
			b.WriteString(fmt.Sprintf("  todo [%s] %s\n", t.Status, t.Content))
		}
		for i, g := range s.Plan.GoalNodes {
			if i >= 10 {
				b.WriteString("  ... (goal render cap)\n")
				break
			}
			b.WriteString(fmt.Sprintf("  goal[%s] %s %s", g.Status, g.Level, g.Title))
			if g.NextStep != "" {
				b.WriteString(fmt.Sprintf(" -> %s", g.NextStep))
			}
			b.WriteString("\n")
		}
		for i, tk := range s.Plan.Tasks {
			if i >= 10 {
				b.WriteString("  ... (task render cap)\n")
				break
			}
			b.WriteString(fmt.Sprintf("  task [%s] %s", tk.Status, tk.Subject))
			if tk.Owner != "" {
				b.WriteString(fmt.Sprintf(" (owner=%s)", tk.Owner))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
