package config

import (
	"sync"
	"testing"
)

func TestIssue2911UpsertSemantics(t *testing.T) {
	cfg := &Config{}
	if cfg.UpsertVendorEndpoint("", "ep") {
		t.Fatal("empty vendor must be a no-op")
	}
	if !cfg.UpsertVendorEndpoint("v", "ep") {
		t.Fatal("first upsert must report changed")
	}
	if cfg.UpsertVendorEndpoint("v", "ep") {
		t.Fatal("idempotent upsert must not report changed")
	}
	if !cfg.UpsertVendorEndpoint("v", "ep2") {
		t.Fatal("new endpoint on existing vendor must report changed")
	}
	vc, ok := cfg.Vendors["v"]
	if !ok || len(vc.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %+v", cfg.Vendors["v"])
	}
	var nilCfg *Config
	if nilCfg.UpsertVendorEndpoint("v", "ep") {
		t.Fatal("nil config must be a safe no-op")
	}
}

func TestIssue2911ConcurrentVendorsWriters(t *testing.T) {
	cfg := &Config{Vendors: map[string]VendorConfig{
		"existing": {Endpoints: map[string]EndpointConfig{"api": {}}},
	}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cfg.UpsertVendorEndpoint("new-vendor", "ep")
			}
		}()
		go func() {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				if err := cfg.SetVendorAPIKey("existing", "${TEST_KEY_2911}"); err != nil {
					t.Errorf("SetVendorAPIKey: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	vc, ok := cfg.Vendors["new-vendor"]
	if !ok {
		t.Fatal("new-vendor missing after concurrent upserts")
	}
	if _, ok := vc.Endpoints["ep"]; !ok {
		t.Fatal("endpoint ep missing after concurrent upserts")
	}
}
