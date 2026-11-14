package a2a

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// SignIntrospectionProof is the client-side twin of VerifyIntrospectionProof:
// sig = HMAC-SHA256(key, nonce + "|" + method). Same-workspace peers share
// the domain key (#5575), so an agent holding the key can prove membership
// without a session state machine (#5593).
func SignIntrospectionProof(key []byte, method, nonce string) string {
	if len(key) == 0 || nonce == "" {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(nonce + "|" + method))
	return hex.EncodeToString(mac.Sum(nil))
}

// NewIntrospectionNonce returns a fresh random nonce for one runtime/*
// request. Crypto-random by design: the proof is stateless, so the nonce is
// the caller-supplied entropy.
func NewIntrospectionNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// IntrospectPeer requests a runtime/snapshot from a same-domain peer over
// the existing A2A transport (JSON-RPC POST + the two introspection
// headers). domainKey is the caller's workspace domain key; the peer
// verifies with its own copy — a mismatch fails closed (#5593).
func IntrospectPeer(ctx context.Context, endpoint, apiKey string, domainKey []byte, mode string) (*RuntimeSnapshotV2, error) {
	if mode == "" {
		mode = IntrospectionFull
	}
	nonce, err := NewIntrospectionNonce()
	if err != nil {
		return nil, fmt.Errorf("a2a introspect: nonce: %w", err)
	}
	const method = "runtime/snapshot"
	sig := SignIntrospectionProof(domainKey, method, nonce)

	reqID, _ := json.Marshal(fmt.Sprintf("introspect-%d", time.Now().UnixNano()))
	body, err := json.Marshal(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  json.RawMessage(`{"mode":` + quoteJSON(mode) + `}`),
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-A2A-Introspection-Nonce", nonce)
	req.Header.Set("X-A2A-Introspection-Signature", sig)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a introspect: transport: %w", err)
	}
	defer resp.Body.Close()
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("a2a introspect: decode: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("a2a introspect: peer denied (code %d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	raw, err := json.Marshal(rpcResp.Result)
	if err != nil {
		return nil, fmt.Errorf("a2a introspect: snapshot decode: %w", err)
	}
	var snap RuntimeSnapshotV2
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("a2a introspect: snapshot decode: %w", err)
	}
	return &snap, nil
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// IntrospectTool is the agent-facing one-hop runtime snapshot client
// (#5593): with the shared domain key an agent can read any same-workspace
// peer's five faces without hand-building HMAC headers.
type IntrospectTool struct {
	registry  *Registry
	apiKey    string
	localDir  string // workspace dir whose domain key signs the request
	localTool *RemoteTool
}

func NewIntrospectTool(reg *Registry, apiKey, localDir string) *IntrospectTool {
	rt := NewRemoteTool(reg, apiKey)
	return &IntrospectTool{registry: reg, apiKey: apiKey, localDir: localDir, localTool: rt}
}

func (t *IntrospectTool) Name() string { return "a2a_introspect" }

func (t *IntrospectTool) Description() string {
	return "Read a same-workspace peer ggcode instance's runtime snapshot (five faces: busy, todos, crons, recent tool traces, conversation digest) over the A2A runtime/* namespace. " +
		"Auth is the shared workspace domain key (HMAC proof, no session state) - works only between instances of the SAME workspace family. " +
		"Use this to check what another agent is doing right now; use 'a2a_remote' to delegate work, 'lanchat' to talk."
}

func (t *IntrospectTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {
				"type": "string",
				"description": "Target instance name (project name, e.g. 'order-service'). Required."
			},
			"mode": {
				"type": "string",
				"enum": ["full", "metadata"],
				"description": "full = all five faces (default); metadata = face headers only, content faces dropped."
			}
		},
		"required": ["target"]
	}`)
}

func (t *IntrospectTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	var params struct {
		Target string `json:"target"`
		Mode   string `json:"mode"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return tool.Result{Content: fmt.Sprintf("Invalid input: %v", err), IsError: true}, nil
	}
	inst, err := t.localTool.FindInstance(params.Target)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	key, err := LoadOrCreateDomainKey(t.localDir)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("domain key: %v", err), IsError: true}, nil
	}
	snap, err := IntrospectPeer(ctx, inst.Endpoint, t.apiKey, key, params.Mode)
	if err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}
	out := FormatSnapshot(params.Target, inst.Workspace, snap)
	return tool.Result{Content: out}, nil
}

// formatSnapshot renders the five faces compactly for an agent consumer.
func FormatSnapshot(target, workspace string, s *RuntimeSnapshotV2) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Runtime snapshot: %s (%s) at %s\n", target, workspace, s.TakenAt))
	b.WriteString(fmt.Sprintf("Mode: %s | Schema: %s\n", s.Mode, s.Schema))
	// #5599: Busy is *RuntimeBusy with omitempty - a peer may legitimately
	// omit the face (pre-phase-2b stub, unwired source, nil-snap handler
	// branch). Degrade instead of dereferencing nil (RedactSnapshotStrings
	// guards the same pointer in this package).
	if s.Busy == nil {
		b.WriteString("Busy: (face not available)\n")
	} else {
		b.WriteString(fmt.Sprintf("Busy: %v", s.Busy.Busy))
		if s.Busy.Busy && s.Busy.InFlightTool != "" {
			b.WriteString(fmt.Sprintf(" since %s in-flight=%s", s.Busy.BusySince, s.Busy.InFlightTool))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("Todos: %d", len(s.Todos)))
	if len(s.Todos) > 0 {
		top := s.Todos
		if len(top) > 5 {
			top = top[:5]
		}
		for _, tk := range top {
			b.WriteString(fmt.Sprintf("\n  - [%s] %s", tk.Status, tk.Subject))
		}
		if len(s.Todos) > 5 {
			b.WriteString(fmt.Sprintf("\n  ... +%d more", len(s.Todos)-5))
		}
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Crons: %d", len(s.Crons)))
	for i, c := range s.Crons {
		if i >= 5 {
			b.WriteString(fmt.Sprintf("\n  ... +%d more", len(s.Crons)-5))
			break
		}
		b.WriteString(fmt.Sprintf("\n  - %s %s", c.Cron, c.ID))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Recent: %d traces\n", len(s.Recent)))
	for i, tr := range s.Recent {
		if i >= 5 {
			b.WriteString(fmt.Sprintf("  ... +%d more\n", len(s.Recent)-5))
			break
		}
		b.WriteString(fmt.Sprintf("  - %s %s: %s\n", tr.TS, tr.Tool, tr.Summary))
	}
	if len(s.Conversation) > 0 {
		b.WriteString(fmt.Sprintf("Conversation: %d windowed turns\n", len(s.Conversation)))
		for i, cv := range s.Conversation {
			if i >= 3 {
				b.WriteString(fmt.Sprintf("  ... +%d more\n", len(s.Conversation)-3))
				break
			}
			b.WriteString(fmt.Sprintf("  - %s %s\n", cv.Role, cv.Summary))
		}
	}
	return b.String()
}
