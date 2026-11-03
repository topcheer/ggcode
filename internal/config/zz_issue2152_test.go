package config

import (
	"sync"
	"testing"
)

func TestIssue2152_LockedReadsRaceWithWriters(t *testing.T) {
	withTestHome(t)
	c := &Config{}
	c.IM.Adapters = map[string]IMAdapterConfig{}

	c.IM.Adapters["seed"] = IMAdapterConfig{
		Enabled:  true,
		Platform: "qq",
		Args:     []string{"--a", "--b"},
		Env:      map[string]string{"K": "V"},
		Targets:  []IMTargetConfig{{ID: "t1", Channel: "c1"}},
	}

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})

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

	writers.Wait()
	close(stop)
	readers.Wait()
}

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
