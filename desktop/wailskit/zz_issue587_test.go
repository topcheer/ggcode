//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

type fakeIMBindingsMgr struct {
	bindings []im.ChannelBinding
}

func (f *fakeIMBindingsMgr) AllPersistedBindings() []im.ChannelBinding {
	return f.bindings
}

func (f *fakeIMBindingsMgr) IsMuted(adapterName string) bool { return false }

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
