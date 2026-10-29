//go:build goolm

package config

// #4407/#4408 regression tests (config review round).

import (
	"testing"
	"time"
)

// #4407: removing the ACTIVE vendor/endpoint must pick a DETERMINISTIC
// successor (lowest vendor name, then lowest endpoint name) per the
// #1422-B2 principle - bare map iteration picked a random survivor and
// the caller persisted the random pick as permanent config.
func TestIssue4407_RemoveVendorDeterministicSuccessor(t *testing.T) {
	c := &Config{
		Vendors: map[string]VendorConfig{
			"mid":   {Endpoints: map[string]EndpointConfig{"mid-main": {BaseURL: "https://m"}}},
			"zeta":  {Endpoints: map[string]EndpointConfig{"z-main": {BaseURL: "https://z"}}},
			"alpha": {Endpoints: map[string]EndpointConfig{"z-endpoint": {BaseURL: "https://a1"}, "a-first": {BaseURL: "https://a2"}}},
		},
		Vendor:   "mid",
		Endpoint: "mid-main",
	}
	if err := c.RemoveVendor("mid"); err != nil {
		t.Fatalf("RemoveVendor: %v", err)
	}
	if c.Vendor != "alpha" || c.Endpoint != "a-first" {
		t.Fatalf("successor must be lowest vendor then lowest endpoint, got %s/%s (#4407)", c.Vendor, c.Endpoint)
	}
}

func TestIssue4407_RemoveEndpointCrossVendorDeterministic(t *testing.T) {
	c := &Config{
		Vendors: map[string]VendorConfig{
			"mid":   {Endpoints: map[string]EndpointConfig{"mid-main": {BaseURL: "https://m"}}},
			"beta":  {Endpoints: map[string]EndpointConfig{"b-endpoint": {BaseURL: "https://b"}}},
			"alpha": {Endpoints: map[string]EndpointConfig{"a-main": {BaseURL: "https://a"}}},
		},
		Vendor:   "mid",
		Endpoint: "mid-main",
	}
	if err := c.RemoveEndpoint("mid", "mid-main"); err != nil {
		t.Fatalf("RemoveEndpoint: %v", err)
	}
	if c.Vendor != "alpha" || c.Endpoint != "a-main" {
		t.Fatalf("cross-vendor successor must be lowest vendor/endpoint, got %s/%s (#4407)", c.Vendor, c.Endpoint)
	}
}

// #4408-C2: a model_limits entry takes priority with "0 = none" - an
// explicit per-model 0 must not be overridden by the endpoint-level
// timeout, else there is no way to disable the timeout for one model.
func TestIssue4408C2_ModelLimitsExplicitZeroWins(t *testing.T) {
	c := &Config{
		Vendors: map[string]VendorConfig{
			"v": {
				APIKey: "k",
				Endpoints: map[string]EndpointConfig{
					"e": {
						BaseURL:        "https://api.example.com/v1",
						RequestTimeout: 45 * time.Second,
						MaxRetries:     4,
						ModelLimits: map[string]ModelLimitConfig{
							"model-x": {RequestTimeout: 0, MaxRetries: 0},
						},
					},
				},
			},
		},
		Vendor: "v", Model: "model-x",
	}
	re, err := c.ResolveEndpointSelection("v", "e", "model-x")
	if err != nil {
		t.Fatalf("ResolveEndpointSelection: %v", err)
	}
	if re.RequestTimeout != 0 {
		t.Fatalf("explicit per-model 0 timeout must not inherit endpoint 45s, got %v (#4408-C2)", re.RequestTimeout)
	}
	// No model_limits entry at all: endpoint values inherit as before.
	re2, err := c.ResolveEndpointSelection("v", "e", "model-other")
	if err != nil {
		t.Fatalf("ResolveEndpointSelection(other): %v", err)
	}
	if re2.RequestTimeout != 45*time.Second {
		t.Fatalf("model without limits must inherit endpoint timeout, got %v (#4408-C2)", re2.RequestTimeout)
	}
}

// #4408-C1: a second legacy gateway vendor key (no matching endpoint,
// vendor-level slot already taken) must be preserved on a per-vendor
// endpoint stub, never silently dropped.
func TestIssue4408C1_SecondLegacyKeyPreserved(t *testing.T) {
	agVC := &VendorConfig{
		APIKey: "first-key",
		Endpoints: map[string]EndpointConfig{
			"shared": {BaseURL: "https://gw"},
		},
	}
	carryGatewayVendorKey(agVC, "second-vendor", "second-key")
	ep, ok := agVC.Endpoints["second-vendor"]
	if !ok || ep.APIKey != "second-key" {
		t.Fatalf("second legacy key must be preserved on an endpoint stub, endpoints: %+v (#4408-C1)", agVC.Endpoints)
	}
	// Existing keys are never overwritten: a third key with the same stub
	// name does not clobber it.
	carryGatewayVendorKey(agVC, "second-vendor", "third-key")
	if agVC.Endpoints["second-vendor"].APIKey != "second-key" {
		t.Fatalf("existing stub key must not be overwritten (#4408-C1)")
	}
}
