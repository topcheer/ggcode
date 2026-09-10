package config

import (
	"testing"
)

// #1517 case C: removing the ACTIVE endpoint/vendor must fall the selection
// back to a remaining one instead of leaving it dangling (which made
// NeedsOnboard pop onboarding on every launch).
func Test1517RemoveActiveSelectionFallback(t *testing.T) {
	// Active endpoint removed -> first remaining endpoint of same vendor.
	c := &Config{
		Vendor:   "v1",
		Endpoint: "e2",
		Vendors: map[string]VendorConfig{
			"v1": {Endpoints: map[string]EndpointConfig{
				"e1": {}, "e2": {}, "e3": {},
			}},
		},
	}
	if err := c.RemoveEndpoint("v1", "e2"); err != nil {
		t.Fatal(err)
	}
	if c.Vendor != "v1" || c.Endpoint != "e1" {
		t.Fatalf("expected fallback to v1/e1, got %s/%s", c.Vendor, c.Endpoint)
	}

	// Last endpoint of active vendor removed -> another vendor takes over.
	c2 := &Config{
		Vendor:   "v1",
		Endpoint: "only",
		Vendors: map[string]VendorConfig{
			"v1": {Endpoints: map[string]EndpointConfig{"only": {}}},
			"v2": {Endpoints: map[string]EndpointConfig{"a": {}}},
		},
	}
	if err := c2.RemoveEndpoint("v1", "only"); err != nil {
		t.Fatal(err)
	}
	if c2.Vendor != "v2" || c2.Endpoint != "a" {
		t.Fatalf("expected fallback to v2/a, got %s/%s", c2.Vendor, c2.Endpoint)
	}

	// Active vendor removed -> another vendor takes over.
	c3 := &Config{
		Vendor:   "v1",
		Endpoint: "x",
		Vendors: map[string]VendorConfig{
			"v1": {Endpoints: map[string]EndpointConfig{"x": {}}},
			"v2": {Endpoints: map[string]EndpointConfig{"b": {}}},
		},
	}
	if err := c3.RemoveVendor("v1"); err != nil {
		t.Fatal(err)
	}
	if c3.Vendor != "v2" || c3.Endpoint != "b" {
		t.Fatalf("expected fallback to v2/b, got %s/%s", c3.Vendor, c3.Endpoint)
	}

	// Non-active removal must not touch the selection.
	c4 := &Config{
		Vendor:   "v1",
		Endpoint: "e1",
		Vendors: map[string]VendorConfig{
			"v1": {Endpoints: map[string]EndpointConfig{"e1": {}, "e2": {}}},
		},
	}
	if err := c4.RemoveEndpoint("v1", "e2"); err != nil {
		t.Fatal(err)
	}
	if c4.Vendor != "v1" || c4.Endpoint != "e1" {
		t.Fatal("non-active removal must not change the selection")
	}
}
