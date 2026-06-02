package acp

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestOutputTailKeepsRecentBytes(t *testing.T) {
	var tail outputTail
	chunk := strings.Repeat("a", outputTailLimit/2)
	if _, err := tail.Write([]byte(chunk)); err != nil {
		t.Fatalf("Write first chunk: %v", err)
	}
	if _, err := tail.Write([]byte(strings.Repeat("b", outputTailLimit))); err != nil {
		t.Fatalf("Write second chunk: %v", err)
	}
	got := tail.Snapshot()
	if len(got) != outputTailLimit {
		t.Fatalf("expected snapshot length %d, got %d", outputTailLimit, len(got))
	}
	if strings.Contains(got, "a") {
		t.Fatalf("expected old bytes to be trimmed, got prefix %q", got[:16])
	}
}

func TestClientSendRequestTimeoutIncludesRecentStderr(t *testing.T) {
	client := NewClient(DiscoveredAgent{Def: AgentDef{Name: "opencode"}}, t.TempDir(), nil, nil)
	client.transport = NewTransport(strings.NewReader(""), io.Discard)
	if _, err := client.stderrTail.Write([]byte("network timeout\nstack line")); err != nil {
		t.Fatalf("Write stderr tail: %v", err)
	}
	_, err := client.sendRequest("session/prompt", nil, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout waiting for client response to session/prompt") {
		t.Fatalf("expected timeout in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "Recent agent stderr:\nnetwork timeout\nstack line") {
		t.Fatalf("expected stderr context in error, got %v", err)
	}
}

func TestClientSendRequestTimeoutIncludesRecentActivity(t *testing.T) {
	client := NewClient(DiscoveredAgent{Def: AgentDef{Name: "opencode"}}, t.TempDir(), nil, nil)
	client.transport = NewTransport(strings.NewReader(""), io.Discard)
	client.activity.Add("recv session/request_permission title=Run kind=execute options=2")
	client.activity.Add("session/update tool_call id=call-5 title=Run kind=execute")

	_, err := client.sendRequest("session/prompt", nil, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "Recent ACP activity:\nrecv session/request_permission title=Run kind=execute options=2\nsession/update tool_call id=call-5 title=Run kind=execute") {
		t.Fatalf("expected ACP activity context in error, got %v", err)
	}
}
