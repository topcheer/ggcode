//go:build unix

package acp

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	acpCloseHangHelperEnv = "GGCODE_TEST_ACP_CLOSE_HANG_HELPER"
	acpCloseHangChildEnv  = "GGCODE_TEST_ACP_CLOSE_HANG_CHILD"
)

func TestClientCloseDoesNotHangOnUnresponsiveSessionClose(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(acpCloseHangHelperEnv, "1")

	client := NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       "close-hang-helper",
				ACPCommand: []string{"-test.run=TestACPUnresponsiveCloseHelperProcess", "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if err := client.NewSession(ctx, t.TempDir()); err != nil {
		t.Fatalf("NewSession error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- client.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked on unresponsive session/close helper")
	}
}

func TestACPUnresponsiveCloseHelperProcess(t *testing.T) {
	if os.Getenv(acpCloseHangChildEnv) == "1" {
		time.Sleep(30 * time.Second)
		return
	}
	if os.Getenv(acpCloseHangHelperEnv) != "1" {
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
				AgentInfo:         ImplementationInfo{Name: "close-hang-helper"},
				AuthMethods:       []AuthMethod{},
			}); err != nil {
				t.Fatalf("write initialize response: %v", err)
			}
		case "session/new":
			if err := transport.WriteResponse(req.ID, NewSessionResponse{SessionID: "close-hang"}); err != nil {
				t.Fatalf("write session/new response: %v", err)
			}
		case "session/close":
			exe, err := os.Executable()
			if err != nil {
				t.Fatalf("resolve child executable: %v", err)
			}
			child := exec.Command(exe, "-test.run=TestACPUnresponsiveCloseHelperProcess", "--")
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
			child.Env = append(os.Environ(), acpCloseHangHelperEnv+"=1", acpCloseHangChildEnv+"=1")
			if err := child.Start(); err != nil {
				t.Fatalf("start leaked child: %v", err)
			}
			select {}
		default:
			if req.ID != nil {
				_ = transport.WriteError(req.ID, -32601, "method not supported")
			}
		}
	}
}
