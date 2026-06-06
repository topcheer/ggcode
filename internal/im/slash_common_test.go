package im

import (
	"strings"
	"testing"
)

func TestExecuteCommonIMSlashCommandMuteSelf(t *testing.T) {
	result := ExecuteCommonIMSlashCommand(NewManager(), "telegram", "/muteself", CommonIMSlashOptions{})
	if !result.Handled {
		t.Fatal("expected /muteself to be handled")
	}
	if result.MuteSelfAdapter != "telegram" {
		t.Fatalf("MuteSelfAdapter = %q, want telegram", result.MuteSelfAdapter)
	}
	if result.Response == "" {
		t.Fatal("expected mute self warning text")
	}
}

func TestExecuteCommonIMSlashCommandListIMUsesSharedFormatter(t *testing.T) {
	mgr := NewManager()
	mgr.adapters["tg"] = AdapterState{Name: "tg", Platform: PlatformTelegram, Healthy: true}
	result := ExecuteCommonIMSlashCommand(mgr, "", "/listim", CommonIMSlashOptions{})
	if !result.Handled {
		t.Fatal("expected /listim to be handled")
	}
	if want := "📬 IM Adapters:\n  • tg [telegram] ✅ online\n"; result.Response != want {
		t.Fatalf("unexpected /listim output: %q", result.Response)
	}
}

func TestCommonIMHelpTextIncludesExtras(t *testing.T) {
	result := ExecuteCommonIMSlashCommand(NewManager(), "", "/help", CommonIMSlashOptions{
		HelpExtraLines: []string{"/restart - Restart"},
	})
	if !result.Handled {
		t.Fatal("expected /help to be handled")
	}
	if !strings.HasSuffix(result.Response, "/help - Show this help") {
		t.Fatalf("unexpected /help output: %q", result.Response)
	}
	if !strings.Contains(result.Response, "/restart - Restart") {
		t.Fatalf("expected extra help line in %q", result.Response)
	}
}
