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

// TestIssue1093_SupervisedModeNotBypassedByStalePolicy guards #1093: the
// approval handler must consult the agent's CURRENT permission policy, not
// the AutoMode policy captured at NewAgentLoop time. Before the fix, after
// SetMode("supervised") any tool the original auto-mode policy marked Allow
// still executed with zero client approval.
func TestIssue1093_SupervisedModeNotBypassedByStalePolicy(t *testing.T) {
	cfg := &config.Config{MaxIterations: 10}
	registry := tool.NewRegistry()
	session := NewSession(t.TempDir(), nil)
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	al := NewAgentLoop(cfg, registry, transport, session, ClientCapabilities{}, nil)

	// Capture the handler installed by NewAgentLoop via the agent itself.
	handler := al.agent.ApprovalHandler()
	if handler == nil {
		t.Fatal("no approval handler installed")
	}

	// Switch to supervised BEFORE any call: the stale closure used the
	// captured AutoMode policy, so a write tool the original auto-mode
	// policy marked Allow would short-circuit to Allow without asking the
	// client.
	al.SetMode("supervised")

	// With the fix, the handler reads the CURRENT (supervised) policy: a
	// SENSITIVE-path write must NOT short-circuit - supervised mode asks
	// the client. No client answers (empty transport), so RequestPermission
	// aborts on ctx timeout and the handler must return Deny rather than
	// Allow.
	//
	// two-mode refactor (#4279): the input is a sensitive path
	// (~/.ssh/authorized_keys), not an arbitrary out-of-sandbox write -
	// ordinary out-of-sandbox writes are Allow in supervised now, so they
	// no longer prove the ask path. The stale-policy property (#1093's
	// soul) is unchanged: the handler must consult the CURRENT policy.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	decision := handler(ctx, "edit_file", `{"file_path":"~/.ssh/authorized_keys","old_text":"a","new_text":"b"}`)
	if decision == permission.Allow {
		t.Fatal("supervised mode must not bypass client approval for sensitive-path writes (#1093)")
	}

	// Companion to the new contract: an ordinary out-of-sandbox write IS
	// Allow in supervised (user-friction ruling #4279) - asserted so the
	// sensitive-path case above can't regress back to gating everything.
	if d := handler(ctx, "edit_file", `{"file_path":"/tmp/nonexistent-zz/x.go","old_text":"a","new_text":"b"}`); d != permission.Allow {
		t.Fatalf("supervised mode must allow ordinary out-of-sandbox writes (#4279), got %v", d)
	}
}
