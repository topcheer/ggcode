package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

const (
	probeCrashHelperEnv    = "GGCODE_TEST_PROBE_CRASH_HELPER"
	probeApprovalHelperEnv = "GGCODE_TEST_PROBE_APPROVAL_HELPER"
	probeHOLHelperEnv      = "GGCODE_TEST_PROBE_HOL_HELPER"
	probeHOLMarkerEnv      = "GGCODE_TEST_PROBE_HOL_MARKER"
)

func probeClient(t *testing.T, env string, helper string) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	t.Setenv(env, "1")
	return NewClient(
		DiscoveredAgent{
			Def: AgentDef{
				Name:       helper,
				ACPCommand: []string{"-test.run=" + helper, "--"},
			},
			Path: exe,
		},
		t.TempDir(),
		nil,
		nil,
	)
}

func TestProbeF3_CrashLeavesStaleReadyState(t *testing.T) {
	c := probeClient(t, probeCrashHelperEnv, "TestProbeCrashHelperProcess")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()
	if err := c.NewSession(ctx, c.workingDir); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err1 := c.Prompt(ctx, "go")
	if err1 == nil {
		t.Fatal("expected prompt #1 to fail after crash")
	}
	t.Logf("prompt #1 error: %v", err1)

	deadline := time.Now().Add(2 * time.Second)
	c.mu.Lock()
	done := c.done
	c.mu.Unlock()
	for done != nil {
		select {
		case <-done:
			done = nil
		case <-time.After(50 * time.Millisecond):
		}
		if done != nil && time.Now().After(deadline) {
			t.Fatal("readLoop did not exit after crash")
		}
	}

	if err := c.EnsureReady(ctx); err != nil {
		t.Logf("EnsureReady returned error (claim would be weakened): %v", err)
	} else {
		c.mu.Lock()
		stillRunning := c.running
		staleSession := c.sessionID
		c.mu.Unlock()
		t.Logf("EnsureReady returned NIL with running=%v staleSession=%q (stale ready state)", stillRunning, staleSession)
		if !stillRunning {
			t.Fatal("running flag unexpectedly cleared - claim F3 structure changed")
		}
		if staleSession == "" {
			t.Fatal("sessionID unexpectedly cleared - claim F3 structure changed")
		}
	}

	_, err2 := c.Prompt(ctx, "go again")
	if err2 == nil {
		t.Fatal("expected prompt #2 to fail (no self-heal); got success")
	}
	if !strings.Contains(err2.Error(), "exited unexpectedly") &&
		!strings.Contains(err2.Error(), "EOF") &&
		!strings.Contains(err2.Error(), "closed") {
		t.Fatalf("prompt #2 unexpected error: %v", err2)
	}
	t.Logf("prompt #2 error (no self-heal): %v", err2)
}

func TestProbeCrashHelperProcess(t *testing.T) {
	if os.Getenv(probeCrashHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: InitializeResponse{
					ProtocolVersion:   ProtocolVersion,
					AgentCapabilities: AgentCapabilities{LoadSession: true},
					AgentInfo:         ImplementationInfo{Name: "crash-helper", Version: "1.0"},
				},
			})
		case "session/new":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID, Result: NewSessionResponse{SessionID: "s-crash"},
			})
		case "session/prompt":
			os.Exit(1)
		}
	}
}

func TestProbeF2_IdleTimeoutFiresDuringHumanApproval(t *testing.T) {
	t.Run("human_slower_than_idle_false_timeout", func(t *testing.T) {
		c := probeClient(t, probeApprovalHelperEnv, "TestProbeApprovalHelperProcess")
		c.promptIdleTime = 500 * time.Millisecond
		approvalDelay := 5 * time.Second
		c.SetApprovalHandler(func(ctx context.Context, toolName, input string) permission.Decision {
			select {
			case <-time.After(approvalDelay):
				return permission.Allow
			case <-ctx.Done():
				return permission.Deny
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := c.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer c.Close()
		if err := c.NewSession(ctx, c.workingDir); err != nil {
			t.Fatalf("NewSession: %v", err)
		}

		_, err := c.Prompt(ctx, "needs approval")
		if err == nil {
			t.Fatal("expected timeout error, got success")
		}
		if !strings.Contains(err.Error(), "timeout waiting for agent prompt completion") {
			t.Fatalf("expected idle-timeout error, got: %v", err)
		}
		t.Logf("CONFIRMED F2: prompt aborted while human approval was in flight: %v", err)
	})

	t.Run("human_faster_than_idle_succeeds", func(t *testing.T) {
		c := probeClient(t, probeApprovalHelperEnv, "TestProbeApprovalHelperProcess")
		c.promptIdleTime = 15 * time.Second
		c.SetApprovalHandler(func(ctx context.Context, toolName, input string) permission.Decision {
			time.Sleep(300 * time.Millisecond)
			return permission.Allow
		})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := c.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer c.Close()
		if err := c.NewSession(ctx, c.workingDir); err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		res, err := c.Prompt(ctx, "needs approval")
		if err != nil {
			t.Fatalf("control case must succeed, got: %v", err)
		}
		if res == nil || res.StopReason == "" {
			t.Fatalf("expected completed result, got %+v", res)
		}
	})
}

func TestProbeApprovalHelperProcess(t *testing.T) {
	if os.Getenv(probeApprovalHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: InitializeResponse{
					ProtocolVersion:   ProtocolVersion,
					AgentCapabilities: AgentCapabilities{LoadSession: true},
					AgentInfo:         ImplementationInfo{Name: "approval-helper", Version: "1.0"},
				},
			})
		case "session/new":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID, Result: NewSessionResponse{SessionID: "s-appr"},
			})
		case "session/prompt":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": 9001, "method": "session/request_permission",
				"params": map[string]any{
					"sessionID": "s-appr",
					"options": []map[string]any{
						{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
						{"optionId": "deny", "name": "Deny", "kind": "reject_once"},
					},
				},
			})
			answered := false
			for !answered && scanner.Scan() {
				var line map[string]json.RawMessage
				if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
					continue
				}
				if _, isResp := line["result"]; isResp {
					answered = true
				}
				if _, isErr := line["error"]; isErr {
					answered = true
				}
			}
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: map[string]any{"stopReason": "end_turn"},
			})
		}
	}
}

func TestProbeF1_HeadOfLineBlockingAndCloseHang(t *testing.T) {
	t.Skip("#1087 F1 fixed: permission/FS requests now handled asynchronously; blocking probe no longer applies")

	markerPath := filepath.Join(t.TempDir(), "noop-responded")
	t.Setenv(probeHOLMarkerEnv, markerPath)

	c := probeClient(t, probeHOLHelperEnv, "TestProbeHOLHelperProcess")
	release := make(chan struct{})
	c.SetApprovalHandler(func(ctx context.Context, toolName, input string) permission.Decision {
		<-release
		return permission.Allow
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.NewSession(ctx, c.workingDir); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	type reqResult struct {
		err error
	}
	reqCh := make(chan reqResult, 1)
	go func() {
		_, err := c.sendRequest(context.Background(), "probe/noop", map[string]any{}, 700*time.Millisecond)
		reqCh <- reqResult{err: err}
	}()

	markerSeen := false
	deadlineM := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadlineM) {
		if _, err := os.Stat(markerPath); err == nil {
			markerSeen = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !markerSeen {
		t.Fatal("helper never wrote the response marker")
	}

	select {
	case r := <-reqCh:
		if r.err == nil {
			t.Fatal("probe/noop unexpectedly succeeded - no head-of-line blocking?")
		}
		if !strings.Contains(r.err.Error(), "timeout waiting for client response") {
			t.Fatalf("expected delivery timeout, got: %v", r.err)
		}
		t.Logf("CONFIRMED F1 (a): response written by agent but undeliverable; sendRequest timed out: %v", r.err)
	case <-time.After(3 * time.Second):
		t.Fatal("sendRequest did not settle")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	select {
	case <-closeDone:
		t.Fatal("Close returned while readLoop was still parked in approval handler")
	case <-time.After(400 * time.Millisecond):
		t.Log("CONFIRMED F1 (b): Close() blocked behind parked readLoop (done not closed)")
	}

	close(release)
	select {
	case <-closeDone:
		t.Log("Close returned after approval released")
	case <-time.After(15 * time.Second):
		t.Fatal("Close never returned after approval release")
	}
}

func TestProbeHOLHelperProcess(t *testing.T) {
	if os.Getenv(probeHOLHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	markerPath := os.Getenv(probeHOLMarkerEnv)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var line map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		var method string
		if raw, ok := line["method"]; ok {
			_ = json.Unmarshal(raw, &method)
		}
		var id json.RawMessage
		if raw, ok := line["id"]; ok {
			id = raw
		}
		switch method {
		case "initialize":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: &id,
				Result: InitializeResponse{
					ProtocolVersion:   ProtocolVersion,
					AgentCapabilities: AgentCapabilities{LoadSession: true},
					AgentInfo:         ImplementationInfo{Name: "hol-helper", Version: "1.0"},
				},
			})
		case "session/new":
			_ = encoder.Encode(JSONRPCResponse{
				JSONRPC: "2.0", ID: &id, Result: NewSessionResponse{SessionID: "s-hol"},
			})
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0", "id": 9002, "method": "session/request_permission",
				"params": map[string]any{
					"sessionID": "s-hol",
					"options": []map[string]any{
						{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
					},
				},
			})
		case "":
			continue
		default:
			_ = encoder.Encode(JSONRPCResponse{JSONRPC: "2.0", ID: &id, Result: map[string]any{}})
			if method == "probe/noop" {
				_ = os.WriteFile(markerPath, []byte(fmt.Sprintf("%d", time.Now().UnixNano())), 0o644)
			}
		}
	}
}
