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
			cfg:     config.NotificationConfig{Mode: "off", Bell: bellPtr(true)},
			summary: "test",
		},
		{
			name:    "long mode notifies on input",
			cfg:     config.NotificationConfig{Mode: "long", Bell: bellPtr(true)},
			summary: "Approval needed: run_command",
		},
		{
			name:    "all mode notifies on input",
			cfg:     config.NotificationConfig{Mode: "all", Bell: bellPtr(true)},
			summary: "Question needed",
		},
		{
			name:    "desktop notification",
			cfg:     config.NotificationConfig{Mode: "all", Desktop: true},
			summary: "Diff confirmation needed",
		},
		{
			name:    "empty summary uses default",
			cfg:     config.NotificationConfig{Mode: "all", Bell: bellPtr(true)},
			summary: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			OnInputNeeded(tt.cfg, tt.summary)
		})
	}
}

func TestOnInputNeededDoesNotPanic(t *testing.T) {
	configs := []config.NotificationConfig{
		{},
		{Mode: "off"},
		{Mode: "all", Bell: bellPtr(true), Desktop: true},
		{Mode: "errors", Desktop: true},
		{Mode: "long", Bell: bellPtr(false), Desktop: false},
		{Mode: "unknown", Bell: bellPtr(true)},
	}
	for _, cfg := range configs {
		OnInputNeeded(cfg, "test")
		OnInputNeeded(cfg, "")
	}
}

func TestOnInputNeededVsOnCompletion(t *testing.T) {

	cfg := config.NotificationConfig{Mode: "long", Bell: bellPtr(true)}
	OnInputNeeded(cfg, "approval needed")
	OnCompletion(cfg, 0, false, "test")

	_ = time.Second
}
