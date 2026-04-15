package im

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestStartPCAdapterOnly(t *testing.T) {
	cfg := config.IMConfig{Enabled: false}
	mgr := NewManager()
	controller, err := StartPCAdapterOnly(context.Background(), cfg, mgr)
	if err != nil {
		t.Fatalf("StartPCAdapterOnly error: %v", err)
	}
	defer controller.Stop()

	time.Sleep(200 * time.Millisecond)

	pc := mgr.PCAdapter()
	if pc == nil {
		t.Fatal("PCAdapter() returned nil after StartPCAdapterOnly")
	}

	sessions := pc.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}

	// Verify adapter shows up in snapshot
	snap := mgr.Snapshot()
	found := false
	for _, state := range snap.Adapters {
		if state.Platform == PlatformPrivateClaw {
			found = true
			t.Logf("PC adapter state: healthy=%v status=%s", state.Healthy, state.Status)
		}
	}
	if !found {
		t.Error("PC adapter not found in Manager snapshot")
	}
}

func TestStartPCAdapterWithExplicitConfig(t *testing.T) {
	cfg := config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"my-pc": {
				Enabled:  true,
				Platform: string(PlatformPrivateClaw),
				Extra:    map[string]interface{}{},
			},
		},
	}
	mgr := NewManager()
	controller, err := StartConfiguredAdapters(context.Background(), cfg, mgr)
	if err != nil {
		t.Fatalf("StartConfiguredAdapters error: %v", err)
	}
	defer controller.Stop()

	time.Sleep(200 * time.Millisecond)

	pc := mgr.PCAdapter()
	if pc == nil {
		t.Fatal("PCAdapter() returned nil after StartConfiguredAdapters")
	}
}
