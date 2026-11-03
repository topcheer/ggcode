package config

import "testing"

func TestIssue2289VendorInPlaceEditSurvivesReload(t *testing.T) {
	global := &Config{Vendors: map[string]VendorConfig{
		"foo": {Endpoints: map[string]EndpointConfig{"main": {BaseURL: "https://m1"}}},
	}}

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
