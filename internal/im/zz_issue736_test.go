package im

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

func TestIssue736_UnknownPlatformLogsAndSkips(t *testing.T) {
	debug.EnableForTest(t, "im")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()

	cfg := config.IMConfig{
		Adapters: map[string]config.IMAdapterConfig{
			"bad": {
				Enabled:  true,
				Platform: "telegarm",
			},
		},
	}

	if err := startConfiguredAdapter(ctx, cfg, "bad", cfg.Adapters["bad"], mgr); err != nil {
		t.Fatalf("#736: unknown platform must skip without error, got %v", err)
	}

	found := false

	for _, entry := range debug.RingHistoryMax(2000, "im") {
		if strings.Contains(entry.Message, "unknown platform") && strings.Contains(entry.Message, "telegarm") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("#736: no debug log for unknown platform \"telegarm\" — default branch still silent; ring tail: %+v", debug.RingHistoryMax(5, "im"))
	}
}

func TestIssue736_LegacyCasingPlatformLogsAndSkips(t *testing.T) {
	debug.EnableForTest(t, "im")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()

	cfg := config.IMConfig{
		Adapters: map[string]config.IMAdapterConfig{
			"tg": {
				Enabled:  true,
				Platform: "Telegram",
			},
		},
	}

	if err := startConfiguredAdapter(ctx, cfg, "tg", cfg.Adapters["tg"], mgr); err != nil {
		t.Fatalf("#736: legacy-cased platform must skip without error, got %v", err)
	}

	found := false
	for _, entry := range debug.RingHistoryMax(2000, "im") {
		if strings.Contains(entry.Message, "unknown platform") && strings.Contains(entry.Message, "Telegram") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("#736: no debug log for legacy-cased platform \"Telegram\" — default branch still silent")
	}
}

func TestIssue736_DisabledAdapterStillNoop(t *testing.T) {
	debug.EnableForTest(t, "im")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()

	cfg := config.IMConfig{
		Adapters: map[string]config.IMAdapterConfig{
			"off": {Enabled: false, Platform: "no-such-platform"},
		},
	}
	if err := startConfiguredAdapter(ctx, cfg, "off", cfg.Adapters["off"], mgr); err != nil {
		t.Fatalf("disabled adapter must be a silent noop, got %v", err)
	}

	for _, entry := range debug.RingHistoryMax(2000, "im") {
		if strings.Contains(entry.Message, "unknown platform") && strings.Contains(entry.Message, `"off"`) {
			t.Fatalf("disabled adapter must not hit the default branch log, got: %s", entry.Message)
		}
	}
}
