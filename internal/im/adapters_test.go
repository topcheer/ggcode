package im

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestStartCurrentBindingAdapterOnlyStartsCurrentWorkspaceBinding(t *testing.T) {
	mgr := NewManager()
	store := NewMemoryBindingStore()
	if err := store.Save(ChannelBinding{
		Workspace: "/tmp/workspace-current",
		Platform:  PlatformQQ,
		Adapter:   "qq-current",
	}); err != nil {
		t.Fatalf("Save current binding returned error: %v", err)
	}
	if err := store.Save(ChannelBinding{
		Workspace: "/tmp/workspace-other",
		Platform:  PlatformQQ,
		Adapter:   "qq-other",
	}); err != nil {
		t.Fatalf("Save other binding returned error: %v", err)
	}
	if err := mgr.SetBindingStore(store); err != nil {
		t.Fatalf("SetBindingStore returned error: %v", err)
	}
	mgr.BindSession(SessionBinding{Workspace: "/tmp/workspace-current"})

	controller, err := StartCurrentBindingAdapter(context.Background(), config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"qq-current": {Enabled: true, Platform: "qq"},
			"qq-other":   {Enabled: true, Platform: "qq"},
		},
	}, mgr)
	if err != nil {
		t.Fatalf("StartCurrentBindingAdapter returned error: %v", err)
	}
	defer controller.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := mgr.Snapshot()
		foundCurrent := false
		foundOther := false
		for _, state := range snapshot.Adapters {
			switch state.Name {
			case "qq-current":
				foundCurrent = true
			case "qq-other":
				foundOther = true
			}
		}
		if foundOther {
			t.Fatalf("expected other workspace adapter to stay stopped, snapshot=%#v", snapshot.Adapters)
		}
		if foundCurrent {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected current workspace adapter to start")
}
