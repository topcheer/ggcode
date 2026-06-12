package acp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

const acpPromptMalformedHelperEnv = "GGCODE_TEST_ACP_PROMPT_MALFORMED_HELPER"

func TestPromptReturnsTransportParseErrorInsteadOfTimingOut(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(acpPromptMalformedHelperEnv, "1")

	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "prompt-malformed-helper",
				ACPCommand: []string{"-test.run=TestACPPromptMalformedHelperProcess", "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil,
		nil,
	)
	client.promptIdleTime = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer client.Close()
	if err := client.NewSession(ctx, t.TempDir()); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	_, err = client.Prompt(ctx, "trigger malformed transport output")
	if err == nil {
		t.Fatal("expected prompt error, got nil")
	}
	if strings.Contains(err.Error(), "timeout waiting for agent prompt completion") {
		t.Fatalf("expected transport error instead of timeout, got %v", err)
	}
	if !strings.Contains(err.Error(), "parsing JSON-RPC message") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestACPPromptMalformedHelperProcess(t *testing.T) {
	if os.Getenv(acpPromptMalformedHelperEnv) != "1" {
		t.Skip("helper process only")
	}

	transport := NewTransport(os.Stdin, os.Stdout)
	for {
		req, resp, err := transport.ReadAnyMessage()
		if err != nil {
			return
		}
		if resp != nil || req == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			if err := transport.WriteResponse(req.ID, InitializeResponse{
				ProtocolVersion:   ProtocolVersion,
				AgentCapabilities: AgentCapabilities{},
				AgentInfo:         ImplementationInfo{Name: "prompt-malformed-helper"},
				AuthMethods:       []AuthMethod{},
			}); err != nil {
				t.Fatalf("write initialize response: %v", err)
			}
		case "session/new":
			if err := transport.WriteResponse(req.ID, NewSessionResponse{SessionID: "malformed"}); err != nil {
				t.Fatalf("write session/new response: %v", err)
			}
		case "session/prompt":
			if err := transport.WriteNotification("session/update", SessionNotification{
				SessionID: "malformed",
				Update: SessionUpdate{
					Type:       UpdateToolCall,
					ToolCallID: "call-1",
					Title:      "malformed",
					Kind:       ToolKindOther,
				},
			}); err != nil {
				t.Fatalf("write session/update: %v", err)
			}
			if err := transport.WriteRaw([]byte("not-json")); err != nil {
				t.Fatalf("write malformed line: %v", err)
			}
			return
		default:
			if req.ID != nil {
				_ = transport.WriteError(req.ID, -32601, "method not supported")
			}
		}
	}
}
