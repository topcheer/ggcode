package acp

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestPermissionRequestResponseUsesStableToolKeyAndPersistentAllow(t *testing.T) {
	policy := permission.NewConfigPolicyWithMode(map[string]permission.Decision{}, nil, permission.SupervisedMode)
	client := NewClient(DiscoveredAgent{Def: AgentDef{Name: "copilot"}}, t.TempDir(), policy, nil)

	var gotToolName string
	var gotInput string
	client.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
		gotToolName = toolName
		gotInput = input
		policy.SetOverride(toolName, permission.Allow)
		return permission.Allow
	})

	rawInput := json.RawMessage(`{"command":"make test"}`)
	resp, err := client.permissionRequestResponse(context.Background(), RequestPermissionRequest{
		ToolCall: &ToolCallUpdate{
			Kind:     ToolKindExecute,
			Title:    "Run build",
			RawInput: string(rawInput),
		},
		Options: []PermissionOption{
			{OptionID: "allow-once", Kind: PermissionOptionAllowOnce},
			{OptionID: "allow-always", Kind: PermissionOptionAllowAlways},
			{OptionID: "reject-once", Kind: PermissionOptionRejectOnce},
		},
	}, rawInput)
	if err != nil {
		t.Fatalf("permissionRequestResponse error: %v", err)
	}
	if gotToolName != "delegate_copilot_execute" {
		t.Fatalf("expected stable tool name, got %q", gotToolName)
	}
	if gotInput != string(rawInput) {
		t.Fatalf("expected raw input to be forwarded, got %q", gotInput)
	}
	if resp.Outcome.Outcome != "selected" || resp.Outcome.SelectedOption == nil || resp.Outcome.SelectedOption.OptionID != "allow-always" {
		t.Fatalf("expected persistent allow response, got %+v", resp.Outcome)
	}
}

func TestPermissionRequestResponseSkipsApprovalWhenPolicyAlreadyAllows(t *testing.T) {
	policy := permission.NewConfigPolicyWithMode(map[string]permission.Decision{}, nil, permission.SupervisedMode)
	policy.SetOverride("delegate_copilot_execute", permission.Allow)

	client := NewClient(DiscoveredAgent{Def: AgentDef{Name: "copilot"}}, t.TempDir(), policy, nil)
	approvalCalls := 0
	client.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
		approvalCalls++
		return permission.Allow
	})

	rawInput := json.RawMessage(`{"command":"go test ./..."}`)
	resp, err := client.permissionRequestResponse(context.Background(), RequestPermissionRequest{
		ToolCall: &ToolCallUpdate{
			Kind:     ToolKindExecute,
			RawInput: string(rawInput),
		},
		Options: []PermissionOption{
			{OptionID: "allow-always", Kind: PermissionOptionAllowAlways},
			{OptionID: "allow-once", Kind: PermissionOptionAllowOnce},
			{OptionID: "reject-once", Kind: PermissionOptionRejectOnce},
		},
	}, rawInput)
	if err != nil {
		t.Fatalf("permissionRequestResponse error: %v", err)
	}
	if approvalCalls != 0 {
		t.Fatalf("expected policy allow to bypass approval, got %d approval calls", approvalCalls)
	}
	if resp.Outcome.Outcome != "selected" || resp.Outcome.SelectedOption == nil || resp.Outcome.SelectedOption.OptionID != "allow-always" {
		t.Fatalf("expected policy allow to select persistent allow option, got %+v", resp.Outcome)
	}
}

func TestEnsureReadyRecreatesSessionAfterWorkingDirChange(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	clientTransport := NewTransport(clientRead, clientWrite)
	serverTransport := NewTransport(serverRead, serverWrite)

	client := NewClient(DiscoveredAgent{Def: AgentDef{Name: "copilot"}}, "/new", nil, nil)
	client.transport = clientTransport
	client.running = true
	client.sessionID = "session-old"
	client.sessionCWD = "/old"

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			req, err := serverTransport.ReadMessage()
			if err != nil {
				return
			}
			switch req.Method {
			case "session/close":
				_ = serverTransport.WriteResponse(req.ID, CloseSessionResponse{})
			case "session/new":
				_ = serverTransport.WriteResponse(req.ID, NewSessionResponse{SessionID: "session-new"})
				return
			}
		}
	}()

	go func() {
		for {
			_, resp, err := clientTransport.ReadAnyMessage()
			if err != nil {
				return
			}
			if resp != nil {
				clientTransport.DeliverResponse(resp)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.EnsureReady(ctx); err != nil {
		t.Fatalf("EnsureReady error: %v", err)
	}
	<-done

	if client.sessionID != "session-new" {
		t.Fatalf("expected recreated session id, got %q", client.sessionID)
	}
	if client.sessionCWD != "/new" {
		t.Fatalf("expected recreated session cwd /new, got %q", client.sessionCWD)
	}
}

func TestNewSessionSendsEmptyMCPServersArray(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	clientTransport := NewTransport(clientRead, clientWrite)
	serverTransport := NewTransport(serverRead, serverWrite)

	client := NewClient(DiscoveredAgent{Def: AgentDef{Name: "copilot"}}, "/workspace", nil, nil)
	client.transport = clientTransport
	client.running = true

	reqCh := make(chan JSONRPCRequest, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, err := serverTransport.ReadMessage()
		if err != nil {
			t.Errorf("server read message: %v", err)
			return
		}
		reqCh <- *req
		_ = serverTransport.WriteResponse(req.ID, NewSessionResponse{SessionID: "session-1"})
	}()

	go func() {
		for {
			_, resp, err := clientTransport.ReadAnyMessage()
			if err != nil {
				return
			}
			if resp != nil {
				clientTransport.DeliverResponse(resp)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.NewSession(ctx, "/workspace"); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}
	<-done

	req := <-reqCh
	if req.Method != "session/new" {
		t.Fatalf("expected session/new request, got %q", req.Method)
	}
	var params map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["cwd"] != "/workspace" {
		t.Fatalf("expected cwd /workspace, got %#v", params["cwd"])
	}
	rawServers, ok := params["mcpServers"]
	if !ok {
		t.Fatal("expected mcpServers field to be present")
	}
	servers, ok := rawServers.([]any)
	if !ok {
		t.Fatalf("expected mcpServers array, got %#v", rawServers)
	}
	if len(servers) != 0 {
		t.Fatalf("expected empty mcpServers array, got %#v", servers)
	}
}

func TestNewSessionSendsConfiguredMCPServers(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	clientTransport := NewTransport(clientRead, clientWrite)
	serverTransport := NewTransport(serverRead, serverWrite)

	client := NewClient(DiscoveredAgent{Def: AgentDef{Name: "copilot"}}, "/workspace", nil, mcpServersFromConfig([]config.MCPServerConfig{
		{
			Name:    "repo-tools",
			Command: "node",
			Args:    []string{"repo-mcp.js"},
			Env: map[string]string{
				"API_TOKEN": "secret",
			},
		},
		{
			Name: "remote-http",
			Type: "http",
			URL:  "https://example.com/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer token",
			},
		},
	}))
	client.transport = clientTransport
	client.running = true

	reqCh := make(chan JSONRPCRequest, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, err := serverTransport.ReadMessage()
		if err != nil {
			t.Errorf("server read message: %v", err)
			return
		}
		reqCh <- *req
		_ = serverTransport.WriteResponse(req.ID, NewSessionResponse{SessionID: "session-1"})
	}()

	go func() {
		for {
			_, resp, err := clientTransport.ReadAnyMessage()
			if err != nil {
				return
			}
			if resp != nil {
				clientTransport.DeliverResponse(resp)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.NewSession(ctx, "/workspace"); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}
	<-done

	req := <-reqCh
	if req.Method != "session/new" {
		t.Fatalf("expected session/new request, got %q", req.Method)
	}
	var params struct {
		MCPServers []MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if len(params.MCPServers) != 2 {
		t.Fatalf("expected 2 mcpServers, got %+v", params.MCPServers)
	}
	if params.MCPServers[0].Name != "repo-tools" || params.MCPServers[0].Type != "stdio" || params.MCPServers[0].Command != "node" {
		t.Fatalf("unexpected stdio MCP payload: %+v", params.MCPServers[0])
	}
	if len(params.MCPServers[0].Env) != 1 || params.MCPServers[0].Env[0].Name != "API_TOKEN" {
		t.Fatalf("unexpected stdio env passthrough: %+v", params.MCPServers[0].Env)
	}
	if params.MCPServers[1].Name != "remote-http" || params.MCPServers[1].Type != "http" || params.MCPServers[1].URL != "https://example.com/mcp" {
		t.Fatalf("unexpected http MCP payload: %+v", params.MCPServers[1])
	}
	if len(params.MCPServers[1].Headers) != 1 || params.MCPServers[1].Headers[0].Name != "Authorization" {
		t.Fatalf("unexpected http header passthrough: %+v", params.MCPServers[1].Headers)
	}
}
