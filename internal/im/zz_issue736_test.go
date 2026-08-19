package im

// Regression tests for GitHub issue #736: the IM adapter startup switch
// (startConfiguredAdapter) matched platform IDs exactly with no default
// branch — an unknown or legacy non-canonical platform (e.g. "Telegram"
// persisted before #648) returned nil silently: no error, no log, the
// adapter just never started. The default branch now logs via debug.Log.

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// TestIssue736_UnknownPlatformLogsAndSkips probes the default branch: an
// enabled adapter with an unrecognized platform must (a) not error — one bad
// adapter must not block the others — and (b) leave a traceable debug log
// line, replacing the previous silent nil.
func TestIssue736_UnknownPlatformLogsAndSkips(t *testing.T) {
	debug.EnableForTest(t, "im")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()

	cfg := config.IMConfig{
		Adapters: map[string]config.IMAdapterConfig{
			"bad": {
				Enabled:  true,
				Platform: "telegarm", // typo — matches no case
			},
		},
	}

	// Must not error: the other adapters still start.
	if err := startConfiguredAdapter(ctx, cfg, "bad", cfg.Adapters["bad"], mgr); err != nil {
		t.Fatalf("#736: unknown platform must skip without error, got %v", err)
	}

	// The default branch must have logged — the diagnostic trail that was
	// previously missing entirely.
	found := false
	// Full-suite runs flood the ring with "im" entries from other adapter
	// tests; the 50-entry RingHistory window can evict ours. Use the wide
	// RingHistoryMax window instead.
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

// TestIssue736_LegacyCasingPlatformLogsAndSkips pins the exact #648/#736
// symptom: platform "Telegram" (registry DisplayName casing persisted by
// pre-#648 saves). With load-time canonicalization (#736 config fix) this
// value never reaches the switch anymore, but if it somehow does (hand-edited
// YAML, external im.yaml), the skip is now observable instead of silent.
func TestIssue736_LegacyCasingPlatformLogsAndSkips(t *testing.T) {
	debug.EnableForTest(t, "im")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager()

	cfg := config.IMConfig{
		Adapters: map[string]config.IMAdapterConfig{
			"tg": {
				Enabled:  true,
				Platform: "Telegram", // pre-#648 casing — not matched by PlatformTelegram
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

// TestIssue736_DisabledAdapterStillNoop: the pre-existing early return for
// disabled adapters must keep its silent behavior (not an error, no log).
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
	// The ring buffer is package-global and not reset per test, so scope the
	// assertion to this adapter's name: the early return must not log about "off".
	for _, entry := range debug.RingHistoryMax(2000, "im") {
		if strings.Contains(entry.Message, "unknown platform") && strings.Contains(entry.Message, `"off"`) {
			t.Fatalf("disabled adapter must not hit the default branch log, got: %s", entry.Message)
		}
	}
}
