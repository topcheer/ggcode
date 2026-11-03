//go:build integration_local

package im

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestPCAutoStartE2E(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := config.IMConfig{Enabled: false}
	mgr := NewManager()

	controller, err := StartPCAdapterOnly(context.Background(), cfg, mgr)
	if err != nil {
		t.Fatalf("StartPCAdapterOnly error: %v", err)
	}
	defer controller.Stop()

	time.Sleep(100 * time.Millisecond)

	pc := mgr.PCAdapter()
	if pc == nil {
		t.Fatal("PCAdapter() returned nil — TUI will show 'not configured'")
	}

	t.Logf("PC adapter found: name=%s", pc.(*pcAdapter).name)

	snap := mgr.Snapshot()
	found := false
	for _, state := range snap.Adapters {
		if state.Platform == PlatformPrivateClaw {
			found = true
			t.Logf("PC adapter in snapshot: healthy=%v status=%s", state.Healthy, state.Status)
		}
	}
	if !found {
		t.Error("PC adapter not found in Manager snapshot")
	}
}

func TestPCStartAfterSetIMManager(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := config.IMConfig{Enabled: false}

	imMgr := NewManager()

	pcController, err := StartPCAdapterOnly(context.Background(), cfg, imMgr)
	if err != nil {
		t.Fatalf("StartPCAdapterOnly error: %v", err)
	}
	defer pcController.Stop()

	time.Sleep(100 * time.Millisecond)

	pcAdapter := imMgr.PCAdapter()
	if pcAdapter == nil {
		t.Fatal("After StartPCAdapterOnly, imMgr.PCAdapter() is nil")
	}

	sessions := pcAdapter.ListSessions()
	t.Logf("Sessions: %d (expected 0 for fresh adapter)", len(sessions))
}
