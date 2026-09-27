package im

import (
	"context"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// Pin tests for the StartCurrentBindingAdapter decomposition seams
// (r174). These capture the pre-refactor behavior of the extracted pure
// predicates and branch helpers so future edits cannot silently change
// binding start semantics.

func TestBindingSessionOwnedElsewhere(t *testing.T) {
	cases := []struct {
		name      string
		last      string
		current   string
		wantOwned bool
	}{
		{"empty current session claims nothing", "s1", "", true},
		{"mismatched session is foreign", "s1", "s2", true},
		{"matching session owns binding", "s1", "s1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bindingSessionOwnedElsewhere(tc.last, tc.current); got != tc.wantOwned {
				t.Fatalf("bindingSessionOwnedElsewhere(%q, %q) = %v, want %v", tc.last, tc.current, got, tc.wantOwned)
			}
		})
	}
}

func TestIsPCBindingAdapter(t *testing.T) {
	cases := []struct {
		adapter string
		want    bool
	}{
		{"_pc_builtin", true},
		{"privateclaw", true},
		{"PrivateClaw", true}, // EqualFold arm
		{"PRIVATECLAW", true},
		{" telegram", false},
		{"Telegram", false}, // non-canonical spelling must NOT take PC branch
		{"", false},
		{"qq", false},
	}
	for _, tc := range cases {
		if got := isPCBindingAdapter(tc.adapter); got != tc.want {
			t.Fatalf("isPCBindingAdapter(%q) = %v, want %v", tc.adapter, got, tc.want)
		}
	}
}

func TestSkipBindingStartGuardOrder(t *testing.T) {
	t.Run("empty adapter name skips even when muted+foreign", func(t *testing.T) {
		mgr := NewManager()
		b := ChannelBinding{Adapter: "  ", Muted: true, LastSessionID: "s1"}
		if !skipBindingStart(b, mgr) {
			t.Fatal("expected empty-adapter binding to be skipped")
		}
	})
	t.Run("muted binding is skipped", func(t *testing.T) {
		mgr := NewManager()
		b := ChannelBinding{Adapter: "qq-current", Muted: true}
		if !skipBindingStart(b, mgr) {
			t.Fatal("expected muted binding to be skipped")
		}
	})
	t.Run("foreign session with empty current session is skipped", func(t *testing.T) {
		mgr := NewManager()
		b := ChannelBinding{Adapter: "qq-current", LastSessionID: "s1"}
		if !skipBindingStart(b, mgr) {
			t.Fatal("expected foreign-session binding to be skipped when no session bound")
		}
	})
	t.Run("mismatched session is skipped", func(t *testing.T) {
		mgr := NewManager()
		mgr.BindSession(SessionBinding{SessionID: "s2"})
		b := ChannelBinding{Adapter: "qq-current", LastSessionID: "s1"}
		if !skipBindingStart(b, mgr) {
			t.Fatal("expected mismatched-session binding to be skipped")
		}
	})
	t.Run("matching session is not skipped", func(t *testing.T) {
		mgr := NewManager()
		mgr.BindSession(SessionBinding{SessionID: "s1"})
		b := ChannelBinding{Adapter: "qq-current", LastSessionID: "s1"}
		if skipBindingStart(b, mgr) {
			t.Fatal("expected matching-session binding to start")
		}
	})
	t.Run("legacy empty LastSessionID claims start without session", func(t *testing.T) {
		mgr := NewManager()
		b := ChannelBinding{Adapter: "qq-current"}
		if skipBindingStart(b, mgr) {
			t.Fatal("expected legacy binding (empty LastSessionID) to start")
		}
	})
}

func TestStartBindingAdapterSkipPaths(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	t.Run("unconfigured adapter is a silent no-op", func(t *testing.T) {
		if err := startBindingAdapter(ctx, config.IMConfig{}, "missing", mgr); err != nil {
			t.Fatalf("expected nil error for unconfigured adapter, got %v", err)
		}
	})
	t.Run("disabled adapter is a silent no-op", func(t *testing.T) {
		cfg := config.IMConfig{
			Adapters: map[string]config.IMAdapterConfig{
				"qq-off": {Enabled: false, Platform: "qq"},
			},
		}
		if err := startBindingAdapter(ctx, cfg, "qq-off", mgr); err != nil {
			t.Fatalf("expected nil error for disabled adapter, got %v", err)
		}
	})
}

func TestStartPCBindingUserNamedNoConfig(t *testing.T) {
	mgr := NewManager()
	ctx := context.Background()

	// A user-named PC binding without any config entry must be a silent
	// no-op — no panic, no adapter started.
	startPCBinding(ctx, config.IMConfig{}, ChannelBinding{Adapter: "privateclaw"}, mgr)
	if n := len(mgr.Snapshot().Adapters); n != 0 {
		t.Fatalf("expected no adapters started, got %d", n)
	}

	// Disabled config entry: still a no-op.
	cfg := config.IMConfig{
		Adapters: map[string]config.IMAdapterConfig{
			"privateclaw": {Enabled: false, Platform: "privateclaw"},
		},
	}
	startPCBinding(ctx, cfg, ChannelBinding{Adapter: "privateclaw"}, mgr)
	if n := len(mgr.Snapshot().Adapters); n != 0 {
		t.Fatalf("expected no adapters started for disabled entry, got %d", n)
	}
}
