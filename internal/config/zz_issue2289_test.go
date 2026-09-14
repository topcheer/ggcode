package config

import "testing"

// #2289: an instance-scoped in-place edit of a globally configured vendor
// must survive reload. diffVendors deliberately persists such deltas
// (marshal-compare branch), but MergeInstance only adopted keys ABSENT
// from global - the persisted change vanished on reload.
func TestIssue2289VendorInPlaceEditSurvivesReload(t *testing.T) {
	global := &Config{Vendors: map[string]VendorConfig{
		"foo": {Endpoints: map[string]EndpointConfig{"main": {BaseURL: "https://m1"}}},
	}}
	// build the instance side INDEPENDENTLY (no map aliasing with global -
	// a shallow copy would share the Endpoints backing map and the edit
	// would leak into global, making the delta invisible)
	instance := &Config{Vendors: map[string]VendorConfig{
		"foo": {Endpoints: map[string]EndpointConfig{"main": {BaseURL: "https://m1", ContextWindow: 200000}}},
	}}

	MergeInstance(global, instance)

	if got := global.Vendors["foo"].Endpoints["main"].ContextWindow; got != 200000 {
		t.Errorf("in-place instance edit must win on reload, got context_window=%d", got)
	}
	found := false
	for _, f := range global.InstanceFields() {
		if f == "vendors" {
			found = true
		}
	}
	if !found {
		t.Error("adopted vendor must be flagged instance-sourced (write-back gate)")
	}
}

// unchanged instance vendors must not flag (no spurious instance sourcing)
func TestIssue2289UnchangedVendorNotFlagged(t *testing.T) {
	v := VendorConfig{Endpoints: map[string]EndpointConfig{"main": {BaseURL: "https://m1"}}}
	global := &Config{Vendors: map[string]VendorConfig{"foo": v}}
	instance := &Config{Vendors: map[string]VendorConfig{"foo": v}}

	MergeInstance(global, instance)

	for _, f := range global.InstanceFields() {
		if f == "vendors" {
			t.Error("identical vendor must not be flagged instance-sourced")
		}
	}
}
