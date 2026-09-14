package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #2347: the export closure must resolve display names against a SNAPSHOT,
// not the live config maps - mutating config after the snapshot (provider
// switch serialized in Update) must not leak into an export already in
// flight, and the closure must never touch the live maps at all.
func TestSnapshotDisplayNameResolverIsolation(t *testing.T) {
	m := &Model{config: &config.Config{
		Language: "en",
		Vendors: map[string]config.VendorConfig{
			"zai": {
				DisplayName: "Z.AI",
				Endpoints: map[string]config.EndpointConfig{
					"api": {DisplayName: "Z.AI API"},
				},
			},
		},
	}}

	resolve := m.snapshotDisplayNameResolver()

	// Flip the live maps AFTER the snapshot.
	m.config.Vendors["zai"] = config.VendorConfig{
		DisplayName: "MUTATED",
		Endpoints:   map[string]config.EndpointConfig{"api": {DisplayName: "MUTATED-EP"}},
	}

	vd, ed := resolve("zai", "api")
	if vd != "Z.AI" || ed != "Z.AI API" {
		t.Fatalf("resolver must use the snapshot, got vendor=%q endpoint=%q", vd, ed)
	}
	// Unknown ids fall back to raw values.
	vd2, ed2 := resolve("other", "ep")
	if vd2 != "other" || ed2 != "ep" {
		t.Fatalf("unknown ids must pass through raw, got %q/%q", vd2, ed2)
	}
}

func TestSnapshotDisplayNameResolverNilConfig(t *testing.T) {
	m := &Model{}
	resolve := m.snapshotDisplayNameResolver()
	vd, ed := resolve("zai", "api")
	if vd != "zai" || ed != "api" {
		t.Fatalf("nil config must pass through raw ids, got %q/%q", vd, ed)
	}
}
