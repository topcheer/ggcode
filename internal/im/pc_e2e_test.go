package im

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// TestPCAutoStartE2E simulates the exact root.go startup flow
// when IM is disabled, verifying the PC adapter is available.
func TestPCAutoStartE2E(t *testing.T) {
	cfg := config.IMConfig{Enabled: false}
	mgr := NewManager()

	// This is what root.go does when IM is disabled
	controller, err := StartPCAdapterOnly(context.Background(), cfg, mgr)
	if err != nil {
		t.Fatalf("StartPCAdapterOnly error: %v", err)
	}
	defer controller.Stop()

	// Give the adapter goroutine time to register
	time.Sleep(100 * time.Millisecond)

	// Verify PCAdapter() returns non-nil (this is what TUI checks)
	pc := mgr.PCAdapter()
	if pc == nil {
		t.Fatal("PCAdapter() returned nil — TUI will show 'not configured'")
	}

	t.Logf("PC adapter found: name=%s", pc.(*pcAdapter).name)

	// Verify it shows in snapshot
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

// TestPCStartAfterSetIMManager simulates the exact sequence:
// 1. Create Manager
// 2. StartPCAdapterOnly
// 3. "SetIMManager" (just verify the manager is the same object)
// 4. Check PCAdapter() from the same manager reference
func TestPCStartAfterSetIMManager(t *testing.T) {
	cfg := config.IMConfig{Enabled: false}

	// Step 1: Create Manager (like root.go line 459)
	imMgr := NewManager()

	// Step 2: Start PC adapter (like root.go line 490)
	pcController, err := StartPCAdapterOnly(context.Background(), cfg, imMgr)
	if err != nil {
		t.Fatalf("StartPCAdapterOnly error: %v", err)
	}
	defer pcController.Stop()

	time.Sleep(100 * time.Millisecond)

	// Step 3: Verify via the same manager reference (simulates TUI calling pcAdapter())
	// The TUI holds a *im.Manager pointer, so it should see the same sinks
	pcAdapter := imMgr.PCAdapter()
	if pcAdapter == nil {
		t.Fatal("After StartPCAdapterOnly, imMgr.PCAdapter() is nil")
	}

	// Step 4: Verify ListSessions works (simulates TUI rendering session list)
	sessions := pcAdapter.ListSessions()
	t.Logf("Sessions: %d (expected 0 for fresh adapter)", len(sessions))
}
