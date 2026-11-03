package tui

import (
	"strings"
	"testing"
)

func TestMaskedEditValueSecrets(t *testing.T) {
	cases := map[string]string{
		"env.BOT_TOKEN": "secret-token-value",
		"env.API_KEY":   "sk-12345",
		"private_key":   "nsec1abc",
		"password":      "hunter2",
	}
	for field, val := range cases {
		got := maskedEditValue(field, val)
		if strings.Contains(got, val) {
			t.Fatalf("maskedEditValue(%q) leaked the plaintext: %q", field, got)
		}
		if got == "" {
			t.Fatalf("maskedEditValue(%q) must keep a non-empty echo", field)
		}
	}

	if got := maskedEditValue("env.APP_ID", "123456"); got != "123456" {
		t.Fatalf("benign field must echo verbatim, got %q", got)
	}
	if got := maskedEditValue("display_name", "my adapter"); got != "my adapter" {
		t.Fatalf("benign field must echo verbatim, got %q", got)
	}
}

func TestRenderIMEditInputMasksSecretEcho(t *testing.T) {
	m := newTestModel()
	s := &imAdapterEditState{
		adapterName: "x",
		editField:   "env.BOT_TOKEN",
		editInput:   "super-secret-token",
		mode:        imEditInput,
	}
	out := m.renderIMEditInput(s)
	if strings.Contains(out, "super-secret-token") {
		t.Fatalf("edit-input frame leaked the secret into scrollback: %q", out)
	}
}
