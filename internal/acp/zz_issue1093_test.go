package acp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue1093_SupervisedModeNotBypassedByStalePolicy(t *testing.T) {
	cfg := &config.Config{MaxIterations: 10}
	registry := tool.NewRegistry()
	session := NewSession(t.TempDir(), nil)
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)

	handler := al.agent.ApprovalHandler()
	if handler == nil {
		t.Fatal("no approval handler installed")
	}

	al.SetMode("supervised")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	decision := handler(ctx, "edit_file", `{"file_path":"~/.ssh/authorized_keys","old_text":"a","new_text":"b"}`)
	if decision == permission.Allow {
		t.Fatal("supervised mode must not bypass client approval for sensitive-path writes (#1093)")
	}

	if d := handler(ctx, "edit_file", `{"file_path":"/tmp/nonexistent-zz/x.go","old_text":"a","new_text":"b"}`); d != permission.Allow {
		t.Fatalf("supervised mode must allow ordinary out-of-sandbox writes (#4279), got %v", d)
	}
}
