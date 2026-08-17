//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

// fakeIMBindingsMgr implements the narrow interface ListIMAdapters accepts,
// with a canned binding list ordered by the test.
type fakeIMBindingsMgr struct {
	bindings []im.ChannelBinding
}

func (f *fakeIMBindingsMgr) AllPersistedBindings() []im.ChannelBinding {
	return f.bindings
}

func (f *fakeIMBindingsMgr) IsMuted(adapterName string) bool { return false }

// setupIssue587Config writes a minimal config with one enabled adapter and
// returns the working directory it should consider "current".
func setupIssue587Config(t *testing.T, workingDir string) {
	t.Helper()
	tmpDir := t.TempDir()
	ggcodeDir := filepath.Join(tmpDir, ".ggcode")
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)

	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.IM.Adapters == nil {
		cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
	}
	cfg.IM.Adapters["multi-bound"] = config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// TestIssue587_MultiBindingIsCurrent_TrueInEitherOrder verifies that an
// adapter carrying a legacy orphan binding PLUS the current-workspace
// binding reports IsCurrent=true regardless of slice order. The old
// map[string]string fold kept only the last entry (last-write-wins), so
// when the orphan came last, the UI said the adapter was not bound to the
// current workspace even though it was.
func TestIssue587_MultiBindingIsCurrent_TrueInEitherOrder(t *testing.T) {
	workingDir := "/Users/test/proj"
	setupIssue587Config(t, workingDir)

	orphan := im.ChannelBinding{Adapter: "multi-bound", Workspace: "/Users/test/old-orphan"}
	current := im.ChannelBinding{Adapter: "multi-bound", Workspace: workingDir}

	for _, tc := range []struct {
		name     string
		bindings []im.ChannelBinding
	}{
		{"current first, orphan last (old code failed here)", []im.ChannelBinding{current, orphan}},
		{"orphan first, current last", []im.ChannelBinding{orphan, current}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapters, err := ListIMAdapters(workingDir, &fakeIMBindingsMgr{bindings: tc.bindings})
			if err != nil {
				t.Fatalf("ListIMAdapters: %v", err)
			}
			var found *IMAdapterInfo
			for i := range adapters {
				if adapters[i].Name == "multi-bound" {
					found = &adapters[i]
				}
			}
			if found == nil {
				t.Fatal("adapter missing from result")
			}
			if !found.IsCurrent {
				t.Fatal("IsCurrent=false despite current workspace being bound (last-write-wins fold regression)")
			}
		})
	}
}

// TestIssue587_OrphanOnlyBinding_NotCurrent verifies single-binding
// semantics are unchanged: an adapter bound only to some other workspace
// is still IsCurrent=false.
func TestIssue587_OrphanOnlyBinding_NotCurrent(t *testing.T) {
	workingDir := "/Users/test/proj"
	setupIssue587Config(t, workingDir)

	mgr := &fakeIMBindingsMgr{bindings: []im.ChannelBinding{
		{Adapter: "multi-bound", Workspace: "/Users/test/elsewhere"},
	}}
	adapters, err := ListIMAdapters(workingDir, mgr)
	if err != nil {
		t.Fatalf("ListIMAdapters: %v", err)
	}
	for _, a := range adapters {
		if a.Name == "multi-bound" && a.IsCurrent {
			t.Fatal("IsCurrent=true for a binding to another workspace (single-binding semantics regressed)")
		}
	}
}
