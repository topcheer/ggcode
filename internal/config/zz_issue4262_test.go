package config

import "testing"

// #4262: globalOnlyVendors must write the SNAPSHOT's original struct for a
// same-named vendor that the instance merge overrode (#2289 instance-wins) -
// writing the merged struct would leak the workspace-private api_key into the
// global vendors.yaml/keys.env every other workspace loads.
func TestIssue4262_InstanceOverrideKeepsSnapshotOriginal(t *testing.T) {
	global := VendorConfig{APIKey: "sk-global", DisplayName: "global-original"}
	instance := VendorConfig{APIKey: "sk-instance-private", DisplayName: "instance-override"}
	out := globalOnlyVendors(map[string]VendorConfig{"testv": instance}, map[string]VendorConfig{"testv": global})
	got, ok := out["testv"]
	if !ok {
		t.Fatal("same-named global vendor dropped from global-only set")
	}
	if got.APIKey != "sk-global" || got.DisplayName != "global-original" {
		t.Fatalf("instance override leaked to global save: got %+v, want snapshot original %+v", got, global)
	}
}

// A vendor that exists ONLY in the merge (newly added at runtime via
// SyncVendorToGlobalSnapshot-updated snapshot or instance-less flow) is not
// named in the snapshot and stays excluded - unchanged behavior, pinned.
func TestIssue4262_InstanceOnlyVendorStillExcluded(t *testing.T) {
	out := globalOnlyVendors(map[string]VendorConfig{"onlyinst": {APIKey: "x"}}, map[string]VendorConfig{"other": {}})
	if _, present := out["onlyinst"]; present {
		t.Fatal("instance-only vendor must stay excluded from global save")
	}
}
