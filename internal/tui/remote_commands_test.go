package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestExecuteRemoteSlashCommandProviderSummary(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())

	resp, handled := m.ExecuteRemoteSlashCommand("/provider")
	if !handled {
		t.Fatal("expected /provider to be handled")
	}
	if !strings.Contains(resp, "Available vendors") && !strings.Contains(resp, "可用供应商") {
		t.Fatalf("expected provider summary in response, got %q", resp)
	}
}

func TestExecuteRemoteSlashCommandModelSummary(t *testing.T) {
	m := NewModel(nil, nil)
	m.SetConfig(config.DefaultConfig())

	resp, handled := m.ExecuteRemoteSlashCommand("/model")
	if !handled {
		t.Fatal("expected /model to be handled")
	}
	if !strings.Contains(resp, "Available models") && !strings.Contains(resp, "可用模型") {
		t.Fatalf("expected model summary in response, got %q", resp)
	}
}
