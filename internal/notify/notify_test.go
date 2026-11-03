package notify

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestOnCompletionMode(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.NotificationConfig
		duration time.Duration
		failed   bool

		wantMode string
	}{
		{
			name:     "off mode never notifies",
			cfg:      config.NotificationConfig{Mode: "off", Bell: bellPtr(true)},
			duration: 10 * time.Second,
			failed:   false,
			wantMode: "off",
		},
		{
			name:     "errors mode skips success",
			cfg:      config.NotificationConfig{Mode: "errors", Bell: bellPtr(true)},
			duration: 10 * time.Second,
			failed:   false,
			wantMode: "errors",
		},
		{
			name:     "long mode skips short runs",
			cfg:      config.NotificationConfig{Mode: "long", Bell: bellPtr(true)},
			duration: 1 * time.Second,
			failed:   false,
			wantMode: "long",
		},
		{
			name:     "long mode fires for long runs",
			cfg:      config.NotificationConfig{Mode: "long", Bell: bellPtr(true)},
			duration: 10 * time.Second,
			failed:   false,
			wantMode: "long",
		},
		{
			name:     "all mode always fires",
			cfg:      config.NotificationConfig{Mode: "all", Bell: bellPtr(true)},
			duration: 0,
			failed:   false,
			wantMode: "all",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveMode(); got != tt.wantMode {
				t.Errorf("EffectiveMode() = %q, want %q", got, tt.wantMode)
			}

			OnCompletion(tt.cfg, tt.duration, tt.failed, "test summary")
		})
	}
}

func TestOnCompletionDoesNotPanic(t *testing.T) {

	configs := []config.NotificationConfig{
		{},
		{Mode: "off"},
		{Mode: "all", Bell: bellPtr(true), Desktop: true},
		{Mode: "errors", Desktop: true},
		{Mode: "long", MinDuration: 0},
		{Mode: "unknown", Bell: bellPtr(true)},
	}
	for _, cfg := range configs {
		OnCompletion(cfg, 5*time.Second, true, "test")
		OnCompletion(cfg, 5*time.Second, false, "test")
	}
}

func TestEffectiveMinDuration(t *testing.T) {
	tests := []struct {
		cfg  config.NotificationConfig
		want int
	}{
		{config.NotificationConfig{}, 3},
		{config.NotificationConfig{MinDuration: 0}, 3},
		{config.NotificationConfig{MinDuration: 10}, 10},
		{config.NotificationConfig{MinDuration: -1}, 3},
	}
	for _, tt := range tests {
		if got := tt.cfg.EffectiveMinDuration(); got != tt.want {
			t.Errorf("EffectiveMinDuration() = %d, want %d", got, tt.want)
		}
	}
}

func TestEffectiveInputBellDelay(t *testing.T) {
	tests := []struct {
		cfg  config.NotificationConfig
		want int
	}{
		{config.NotificationConfig{}, 5},
		{config.NotificationConfig{InputBellDelay: 0}, 5},
		{config.NotificationConfig{InputBellDelay: 10}, 10},
		{config.NotificationConfig{InputBellDelay: -1}, 0},
	}
	for _, tt := range tests {
		if got := tt.cfg.EffectiveInputBellDelay(); got != tt.want {
			t.Errorf("EffectiveInputBellDelay() = %d, want %d", got, tt.want)
		}
	}
}

func TestShouldBell(t *testing.T) {
	tests := []struct {
		cfg  config.NotificationConfig
		want bool
	}{
		{config.NotificationConfig{}, true},
		{config.NotificationConfig{Bell: bellPtr(true)}, true},
		{config.NotificationConfig{Bell: bellPtr(false)}, false},
		{config.NotificationConfig{Desktop: true}, true},
		{config.NotificationConfig{Bell: bellPtr(true), Desktop: true}, true},
		{config.NotificationConfig{Bell: bellPtr(false), Desktop: true}, false},
	}
	for _, tt := range tests {
		if got := tt.cfg.ShouldBell(); got != tt.want {
			t.Errorf("ShouldBell() = %v, want %v", got, tt.want)
		}
	}
}
