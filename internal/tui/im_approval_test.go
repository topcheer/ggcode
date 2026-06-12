package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
)

// --- im.ParseApprovalReply / im.IsApprovalAlwaysReply ---

func TestParseApprovalReply(t *testing.T) {
	tests := []struct {
		input      string
		want       permission.Decision
		wantOK     bool
		wantAlways bool
	}{
		// Allow
		{"y", permission.Allow, true, false},
		{"Y", permission.Allow, true, false},
		{"yes", permission.Allow, true, false},
		{"ok", permission.Allow, true, false},
		{"好", permission.Allow, true, false},
		{"好的", permission.Allow, true, false},
		{"允许", permission.Allow, true, false},
		{"同意", permission.Allow, true, false},
		{"确认", permission.Allow, true, false},

		// Always allow
		{"a", permission.Allow, true, true},
		{"always", permission.Allow, true, true},
		{"总是允许", permission.Allow, true, true},
		{"总是", permission.Allow, true, true},
		{"始终允许", permission.Allow, true, true},

		// Deny
		{"n", permission.Deny, true, false},
		{"no", permission.Deny, true, false},
		{"nope", permission.Deny, true, false},
		{"拒绝", permission.Deny, true, false},
		{"取消", permission.Deny, true, false},
		{"deny", permission.Deny, true, false},

		// Prefix match
		{"ye", permission.Allow, true, false},
		{"noo", permission.Deny, true, false},

		// Invalid
		{"hello", permission.Deny, false, false},
		{"", permission.Deny, false, false},
		{"maybe", permission.Deny, false, false},
		{"帮我执行", permission.Deny, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := im.ParseApprovalReply(tt.input)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("decision = %v, want %v", got, tt.want)
			}
			always := im.IsApprovalAlwaysReply(tt.input)
			if always != tt.wantAlways {
				t.Errorf("always = %v, want %v", always, tt.wantAlways)
			}
		})
	}
}

func TestParseApprovalReply_TrimSpace(t *testing.T) {
	decision, ok := im.ParseApprovalReply("  y  ")
	if !ok || decision != permission.Allow {
		t.Errorf("expected Allow ok=true, got %v ok=%v", decision, ok)
	}
}

// --- emitIMApproval / emitIMApprovalResult ---

func newTestModelWithIM(t *testing.T) (Model, *tuiTestSink) {
	t.Helper()
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())
	imMgr := im.NewManager()
	if err := imMgr.SetBindingStore(im.NewMemoryBindingStore()); err != nil {
		t.Fatal(err)
	}
	sink := &tuiTestSink{name: "test-sink"}
	imMgr.RegisterSink(sink)
	m.SetIMManager(imMgr)
	store, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ses := session.NewSession("test", "test-endpoint", "test-model")
	m.SetSession(ses, store)

	// Bind a channel so emitter has a target
	if _, err := imMgr.BindChannel(im.ChannelBinding{
		Platform:  im.PlatformQQ,
		Adapter:   "test-sink",
		TargetID:  "user",
		ChannelID: "ch1",
	}); err != nil {
		t.Fatal(err)
	}
	return m, sink
}

// waitForEvents polls sink until at least n events arrive or timeout.
func waitForEvents(sink *tuiTestSink, n int) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(sink.Events()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEmitIMApproval_NoPanicWithoutEmitter(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())
	// Should not panic
	m.emitIMApproval("run_command", "ls")
	m.emitIMApprovalResult("run_command", "allow")
}

func TestEmitIMApproval_English(t *testing.T) {
	m, sink := newTestModelWithIM(t)
	m.emitIMApproval("run_command", `{"command":"rm -rf /"}`)
	waitForEvents(sink, 1)
	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("expected IM event")
	}
	text := events[len(events)-1].Text
	if !strings.Contains(text, "Approval required") {
		t.Errorf("missing 'Approval required', got: %s", text)
	}
	if !strings.Contains(text, "rm -rf") {
		t.Errorf("missing command detail, got: %s", text)
	}
	if !strings.Contains(text, "y allow") {
		t.Errorf("missing reply instructions, got: %s", text)
	}
}

func TestEmitIMApproval_Chinese(t *testing.T) {
	m, sink := newTestModelWithIM(t)
	m.language = LangZhCN
	m.emitIMApproval("write_file", `{"path":"/etc/hosts"}`)
	waitForEvents(sink, 1)
	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("expected IM event")
	}
	text := events[len(events)-1].Text
	if !strings.Contains(text, "需要审批") {
		t.Errorf("missing '需要审批', got: %s", text)
	}
	if !strings.Contains(text, "y 允许") {
		t.Errorf("missing reply instructions, got: %s", text)
	}
}

func TestEmitIMApprovalResult(t *testing.T) {
	m, sink := newTestModelWithIM(t)

	tests := []struct {
		decision string
		want     string
	}{
		{"allow", "✅ Allowed"},
		{"always", "✅ Always allowed"},
		{"deny", "❌ Denied"},
	}

	for _, tt := range tests {
		t.Run(tt.decision, func(t *testing.T) {
			sink.events = nil
			m.emitIMApprovalResult("run_command", tt.decision)
			waitForEvents(sink, 1)
			events := sink.Events()
			if len(events) == 0 {
				t.Fatal("expected result event")
			}
			if !strings.Contains(events[0].Text, tt.want) {
				t.Errorf("got %q, want %q", events[0].Text, tt.want)
			}
		})
	}

	// Chinese
	t.Run("zh_allow", func(t *testing.T) {
		sink.events = nil
		m.language = LangZhCN
		m.emitIMApprovalResult("run_command", "allow")
		waitForEvents(sink, 1)
		events := sink.Events()
		if len(events) == 0 {
			t.Fatal("expected result event")
		}
		if !strings.Contains(events[0].Text, "已允许") {
			t.Errorf("got: %s", events[0].Text)
		}
	})

	t.Run("zh_deny", func(t *testing.T) {
		sink.events = nil
		m.language = LangZhCN
		m.emitIMApprovalResult("run_command", "deny")
		waitForEvents(sink, 1)
		events := sink.Events()
		if len(events) == 0 {
			t.Fatal("expected result event")
		}
		if !strings.Contains(events[0].Text, "已拒绝") {
			t.Errorf("got: %s", events[0].Text)
		}
	})
}

func TestApprovalNotifiedIMField(t *testing.T) {
	m := NewModel(nil, nil)
	if m.approvalNotifiedIM {
		t.Error("should default to false")
	}
	m.approvalNotifiedIM = true
	if !m.approvalNotifiedIM {
		t.Error("should be settable")
	}
}
