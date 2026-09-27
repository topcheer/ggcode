package im

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// adapterBuilderRegistryPlatforms is the exact set of platforms the previous
// inline switch in startConfiguredAdapter dispatched on. The registry must
// cover exactly these keys: pinning the set proves the refactor preserved
// dispatch behavior, including deliberate omissions (synology has no
// constructor and keeps taking the unknown-platform skip path).
var adapterBuilderRegistryPlatforms = []Platform{
	PlatformQQ,
	PlatformTelegram,
	PlatformPrivateClaw,
	PlatformDiscord,
	PlatformFeishu,
	PlatformDingTalk,
	PlatformSlack,
	PlatformDummy,
	PlatformWechat,
	PlatformWeCom,
	PlatformMattermost,
	PlatformMatrix,
	PlatformSignal,
	PlatformIRC,
	PlatformNostr,
	PlatformTwitch,
	PlatformWhatsApp,
}

func TestAdapterBuildersRegistryMatchesCanonicalPlatforms(t *testing.T) {
	registry := adapterBuilders("pin", config.IMConfig{}, config.IMAdapterConfig{}, NewManager())
	if len(registry) != len(adapterBuilderRegistryPlatforms) {
		t.Fatalf("registry size = %d, want %d", len(registry), len(adapterBuilderRegistryPlatforms))
	}
	for _, p := range adapterBuilderRegistryPlatforms {
		build, ok := registry[p]
		if !ok {
			t.Errorf("canonical platform %q missing from registry", p)
			continue
		}
		if build == nil {
			t.Errorf("registry entry for %q is nil", p)
		}
	}
	// Deliberate omissions: these must keep taking the unknown-platform path.
	for _, p := range []Platform{PlatformSynology, PlatformUnknown} {
		if _, ok := registry[p]; ok {
			t.Errorf("platform %q must NOT be registered (unknown-platform path preserved)", p)
		}
	}
}

func TestCanonicalPlatformNormalization(t *testing.T) {
	cases := []struct {
		raw  string
		want Platform
	}{
		{"qq", PlatformQQ},
		{"  telegram  ", PlatformTelegram}, // surrounding whitespace trimmed
		{"", PlatformUnknown},
		{"   ", PlatformUnknown},
		// Case is preserved: legacy non-canonical spellings (#736) must stay
		// unrecognized so they keep taking the skip path.
		{"Telegram", Platform("Telegram")},
		{"QQ", Platform("QQ")},
	}
	for _, tc := range cases {
		if got := canonicalPlatform(tc.raw); got != tc.want {
			t.Errorf("canonicalPlatform(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestStartConfiguredAdapterDisabledIsNoop(t *testing.T) {
	mgr := NewManager()
	err := startConfiguredAdapter(context.Background(), config.IMConfig{}, "off", config.IMAdapterConfig{
		Enabled:  false,
		Platform: string(PlatformDummy),
	}, mgr)
	if err != nil {
		t.Fatalf("disabled adapter must be a noop, got error: %v", err)
	}
	if got := len(mgr.Snapshot().Adapters); got != 0 {
		t.Fatalf("disabled adapter must not register any adapter, snapshot has %d", got)
	}
}

// TestStartConfiguredAdapterUnknownPlatformSkips pins the #736 behavior: a
// legacy non-canonical platform string (e.g. "Telegram") is skipped with no
// error so one bad adapter cannot block the others.
func TestStartConfiguredAdapterUnknownPlatformSkips(t *testing.T) {
	mgr := NewManager()
	err := startConfiguredAdapter(context.Background(), config.IMConfig{}, "legacy", config.IMAdapterConfig{
		Enabled:  true,
		Platform: "Telegram", // legacy casing: not a canonical registry ID
	}, mgr)
	if err != nil {
		t.Fatalf("unknown platform must be skipped without error, got: %v", err)
	}
	if got := len(mgr.Snapshot().Adapters); got != 0 {
		t.Fatalf("unknown platform must not start an adapter, snapshot has %d", got)
	}
}

// TestStartConfiguredAdapterPropagatesConstructorError pins that a failing
// platform constructor surfaces its error verbatim (telegram without a bot
// token fails deterministically, with no network involved).
func TestStartConfiguredAdapterPropagatesConstructorError(t *testing.T) {
	mgr := NewManager()
	err := startConfiguredAdapter(context.Background(), config.IMConfig{}, "tg", config.IMAdapterConfig{
		Enabled:  true,
		Platform: string(PlatformTelegram),
	}, mgr)
	if err == nil {
		t.Fatal("expected constructor error for telegram adapter without bot_token")
	}
	if !strings.Contains(err.Error(), "bot_token") {
		t.Fatalf("expected verbatim constructor error mentioning bot_token, got: %v", err)
	}
}

// TestStartConfiguredAdaptersStartsQQAdapter exercises the public start path
// through the flattened orchestrator: a single enabled qq adapter must end up
// registered in the manager snapshot. (dummy is avoided here on purpose: its
// Start blocks on ctx.Done by design, which would hang the caller.)
func TestStartConfiguredAdaptersStartsQQAdapter(t *testing.T) {
	mgr := NewManager()
	controller, err := StartConfiguredAdapters(context.Background(), config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"qq-pin": {Enabled: true, Platform: string(PlatformQQ)},
		},
	}, mgr)
	if err != nil {
		t.Fatalf("StartConfiguredAdapters returned error: %v", err)
	}
	defer controller.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, state := range mgr.Snapshot().Adapters {
			if state.Name == "qq-pin" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected qq adapter to be registered in manager snapshot")
}
