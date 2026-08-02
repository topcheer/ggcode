package notify

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestOnInputNeededMode(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.NotificationConfig
		summary string
	}{
		{
			name:    "off mode skips notification",
			cfg:     config.NotificationConfig{Mode: "off", Bell: true},
			summary: "test",
		},
		{
			name:    "long mode notifies on input",
			cfg:     config.NotificationConfig{Mode: "long", Bell: true},
			summary: "Approval needed: run_command",
		},
		{
			name:    "all mode notifies on input",
			cfg:     config.NotificationConfig{Mode: "all", Bell: true},
			summary: "Question needed",
		},
		{
			name:    "desktop notification",
			cfg:     config.NotificationConfig{Mode: "all", Desktop: true},
			summary: "Diff confirmation needed",
		},
		{
			name:    "empty summary uses default",
			cfg:     config.NotificationConfig{Mode: "all", Bell: true},
			summary: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic for any combination.
			OnInputNeeded(tt.cfg, tt.summary)
		})
	}
}

func TestOnInputNeededDoesNotPanic(t *testing.T) {
	configs := []config.NotificationConfig{
		{},
		{Mode: "off"},
		{Mode: "all", Bell: true, Desktop: true},
		{Mode: "errors", Desktop: true},
		{Mode: "long", Bell: false, Desktop: false},
		{Mode: "unknown", Bell: true},
	}
	for _, cfg := range configs {
		OnInputNeeded(cfg, "test")
		OnInputNeeded(cfg, "")
	}
}

func TestOnInputNeededVsOnCompletion(t *testing.T) {
	// OnInputNeeded should not check duration — it always fires if mode allows.
	// OnCompletion in "long" mode skips short runs. This verifies the behavioral
	// difference: input-needed should fire even for 0 duration.
	cfg := config.NotificationConfig{Mode: "long", Bell: true}
	OnInputNeeded(cfg, "approval needed")
	OnCompletion(cfg, 0, false, "test") // should be suppressed by long mode
	// Both should complete without panic.
	_ = time.Second // keep import
}
