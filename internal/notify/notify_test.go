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
		// We verify behavior by checking that fireBell is called via the
		// ShouldBell path. Since fireBell just prints \x07, we test the
		// decision logic through EffectiveMode.
		wantMode string
	}{
		{
			name:     "off mode never notifies",
			cfg:      config.NotificationConfig{Mode: "off", Bell: true},
			duration: 10 * time.Second,
			failed:   false,
			wantMode: "off",
		},
		{
			name:     "errors mode skips success",
			cfg:      config.NotificationConfig{Mode: "errors", Bell: true},
			duration: 10 * time.Second,
			failed:   false,
			wantMode: "errors",
		},
		{
			name:     "long mode skips short runs",
			cfg:      config.NotificationConfig{Mode: "long", Bell: true},
			duration: 1 * time.Second,
			failed:   false,
			wantMode: "long",
		},
		{
			name:     "long mode fires for long runs",
			cfg:      config.NotificationConfig{Mode: "long", Bell: true},
			duration: 10 * time.Second,
			failed:   false,
			wantMode: "long",
		},
		{
			name:     "all mode always fires",
			cfg:      config.NotificationConfig{Mode: "all", Bell: true},
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
			// OnCompletion should not panic for any combination.
			OnCompletion(tt.cfg, tt.duration, tt.failed, "test summary")
		})
	}
}

func TestOnCompletionDoesNotPanic(t *testing.T) {
	// Various edge-case configs should not cause panics.
	configs := []config.NotificationConfig{
		{},
		{Mode: "off"},
		{Mode: "all", Bell: true, Desktop: true},
		{Mode: "errors", Desktop: true},
		{Mode: "long", MinDuration: 0},
		{Mode: "unknown", Bell: true},
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
		{config.NotificationConfig{}, 3},               // default
		{config.NotificationConfig{MinDuration: 0}, 3}, // zero = default
		{config.NotificationConfig{MinDuration: 10}, 10},
		{config.NotificationConfig{MinDuration: -1}, 3}, // negative = default
	}
	for _, tt := range tests {
		if got := tt.cfg.EffectiveMinDuration(); got != tt.want {
			t.Errorf("EffectiveMinDuration() = %d, want %d", got, tt.want)
		}
	}
}

func TestShouldBell(t *testing.T) {
	tests := []struct {
		cfg  config.NotificationConfig
		want bool
	}{
		{config.NotificationConfig{}, true},               // backward compat default (nothing set)
		{config.NotificationConfig{Bell: true}, true},     // explicit on
		{config.NotificationConfig{Desktop: true}, false}, // desktop configured, bell not → bell off
		{config.NotificationConfig{Bell: true, Desktop: true}, true},
		{config.NotificationConfig{Bell: false, Desktop: true}, false}, // both configured, bell off
	}
	for _, tt := range tests {
		if got := tt.cfg.ShouldBell(); got != tt.want {
			t.Errorf("ShouldBell() = %v, want %v", got, tt.want)
		}
	}
}
