package config

import (
	"sync"
	"testing"
)

// TestIssue2152_LockedReadsRaceWithWriters pins the IM accessors against
// concurrent map access: N writers hammer the runtime writer methods while
// M readers use GetIMAdapter / IMAdapterEnabled / IMSnapshot. Before #2152
// the readers read c.IM.Adapters directly (TUI Cmd goroutines) while the
// Update loop wrote it — `go test -race` flags it, and production hit the
// unrecoverable "concurrent map read and map write" fatal.
func TestIssue2152_LockedReadsRaceWithWriters(t *testing.T) {
	withTestHome(t)
	c := &Config{}
	c.IM.Adapters = map[string]IMAdapterConfig{}

	// Seed one adapter directly (load-time path, single-threaded).
	c.IM.Adapters["seed"] = IMAdapterConfig{
		Enabled:  true,
		Platform: "qq",
		Args:     []string{"--a", "--b"},
		Env:      map[string]string{"K": "V"},
		Targets:  []IMTargetConfig{{ID: "t1", Channel: "c1"}},
	}

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

	// Writers: toggle enabled + set env (runtime writers from config_save.go).
	// Iterations kept small: each write round-trips a YAML patch to disk;
	// the race detector needs interleaving, not volume.
	for w := 0; w < 2; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 50; i++ {
				_ = c.SetIMAdapterEnabled("seed", i%2 == 0)
				_ = c.SetIMAdapterEnv("seed", "K", "V2")
			}
		}(w)
	}

	// Readers: the locked accessors TUI Cmd goroutines now use.
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if a, ok := c.GetIMAdapter("seed"); ok {
					_ = a.Platform
					_ = a.Args[0]
					_ = a.Env["K"]
					_ = a.Targets[0].ID
				}
				_ = c.IMAdapterEnabled("seed")
				snap := c.IMSnapshot()
				if n := len(snap.Adapters); n != 1 {
					t.Errorf("IMSnapshot adapters = %d, want 1", n)
					return
				}
			}
		}()
	}

	// Writers finish first, then readers are released and joined —
	// readers must NOT share the writers' WaitGroup or close(stop) would
	// deadlock behind Wait() (this exact hang killed the first version).
	writers.Wait()
	close(stop)
	readers.Wait()
}

// TestIssue2152_SnapshotIsDeepCopy verifies IMSnapshot hands out an
// independent copy: mutating the snapshot (or its nested slices/maps) must
// never leak into live config.
func TestIssue2152_SnapshotIsDeepCopy(t *testing.T) {
	c := &Config{}
	c.IM.Adapters = map[string]IMAdapterConfig{
		"a1": {
			Enabled:  true,
			Platform: "feishu",
			Args:     []string{"x"},
			Env:      map[string]string{"K": "V"},
			Targets:  []IMTargetConfig{{ID: "t", Metadata: map[string]string{"m": "1"}}},
		},
	}

	snap := c.IMSnapshot()
	mut := snap.Adapters["a1"]
	mut.Enabled = false
	mut.Args[0] = "mutated"
	mut.Env["K"] = "mutated"
	mut.Targets[0].Metadata["m"] = "mutated"
	delete(snap.Adapters, "a1")

	live, ok := c.GetIMAdapter("a1")
	if !ok {
		t.Fatal("live adapter vanished after snapshot mutation")
	}
	if !live.Enabled {
		t.Error("snapshot Enabled mutation leaked into live config")
	}
	if live.Args[0] != "x" || live.Env["K"] != "V" || live.Targets[0].Metadata["m"] != "1" {
		t.Error("snapshot nested mutation leaked into live config")
	}
}
