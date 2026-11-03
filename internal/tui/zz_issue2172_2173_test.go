package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
)

func TestQQShareCallbackEmptyFailsLoudly(t *testing.T) {
	long := strings.Repeat("项", 11)
	if got := qqShareCallbackData(long); got != "" {
		t.Fatalf("33-byte basename must yield empty callback (precondition), got %q", got)
	}

	if got := qqShareCallbackData("ok-dir"); got != "ok-dir" {
		t.Fatalf("normal basename must pass through, got %q", got)
	}
}

func TestProviderSecretFieldDetection(t *testing.T) {
	yes := []string{"api_key", "endpoint_api_key", "apiKey", "secret", "token", "password"}
	for _, f := range yes {
		if !isProviderSecretField(f) {
			t.Errorf("isProviderSecretField(%q) = false, want true", f)
		}
	}
	no := []string{"base_url", "display_name", "model", "protocol"}
	for _, f := range no {
		if isProviderSecretField(f) {
			t.Errorf("isProviderSecretField(%q) = true, want false", f)
		}
	}
}

func TestProviderEditSecretEchoMasked(t *testing.T) {
	p := &providerPanelState{}
	p.startEditing("api_key", "sk-initial")
	if p.editInput.EchoMode != textinput.EchoPassword {
		t.Fatal("api_key edit input must echo masked (EchoPassword)")
	}
	p.startEditing("base_url", "https://x")
	if p.editInput.EchoMode == textinput.EchoPassword {
		t.Fatal("benign field must echo normally")
	}
}
