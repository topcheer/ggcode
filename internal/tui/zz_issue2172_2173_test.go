package tui

// #2172/#2173 regression:
//   - #2172: an over-long workspace basename (>32 bytes - 11 Chinese
//     chars) made qqShareCallbackData return "" and the QQ panel
//     rendered a "successful" QR that could never pair.
//   - #2173: the provider panel's API-key edit input and the wizards'
//     API-key steps echoed every typed character in cleartext (the
//     repo convention is EchoPassword, cf. onboard/stream_panel).

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
)

func TestQQShareCallbackEmptyFailsLoudly(t *testing.T) {
	long := strings.Repeat("项", 11) // 33 bytes
	if got := qqShareCallbackData(long); got != "" {
		t.Fatalf("33-byte basename must yield empty callback (precondition), got %q", got)
	}
	// The caller now rejects the empty callback BEFORE generating a QR -
	// verified by the guard's presence; unit level we pin the helper
	// boundary that feeds it.
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
