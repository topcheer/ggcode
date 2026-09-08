package config

import "testing"

// Regression for #1517: AddEndpoint on an EXISTING endpoint wiped every
// field the incoming call did not carry (Models, SelectedModel, APIKey
// reference) - the comment promised "updated".
func TestAddEndpointExistingMergesNotReplaces(t *testing.T) {
	c := &Config{Vendors: map[string]VendorConfig{
		"v": {Endpoints: map[string]EndpointConfig{
			"e": {Protocol: "openai", BaseURL: "https://old", Models: []string{"m1", "m2"}, SelectedModel: "m1", APIKey: "${K}"},
		}},
	}}
	if err := c.AddEndpoint("v", "e", "openai", "https://new", ""); err != nil {
		t.Fatal(err)
	}
	ep := c.Vendors["v"].Endpoints["e"]
	if ep.BaseURL != "https://new" {
		t.Fatalf("base_url must update, got %q", ep.BaseURL)
	}
	if len(ep.Models) != 2 || ep.SelectedModel != "m1" || ep.APIKey != "${K}" {
		t.Fatalf("existing fields must be preserved: models=%v selected=%q key=%q", ep.Models, ep.SelectedModel, ep.APIKey)
	}
	// Fresh endpoint still works.
	if err := c.AddEndpoint("v", "e2", "", "https://x", ""); err != nil {
		t.Fatal(err)
	}
	if c.Vendors["v"].Endpoints["e2"].BaseURL != "https://x" {
		t.Fatal("new endpoint must be created")
	}
}

// Regression for #1868 case 3: a failed delete must be able to roll the
// tombstone back, or the yaml entry that survived on disk stays hidden by
// the tombstone (split state) and re-adding the name revives stale fields.
func TestMCPTombstoneRollback(t *testing.T) {
	c := &Config{}
	c.RecordMCPDeleted("srv")
	if len(c.DeletedMCPServers) != 1 || c.DeletedMCPServers[0] != "srv" {
		t.Fatalf("tombstone must be recorded, got %v", c.DeletedMCPServers)
	}
	// Idempotent double-record (failure paths may re-record).
	c.RecordMCPDeleted("srv")
	if len(c.DeletedMCPServers) != 1 {
		t.Fatalf("double record must be idempotent, got %v", c.DeletedMCPServers)
	}
	// Rollback removes exactly the name.
	c.RecordMCPDeleted("other")
	c.ClearMCPDeleted("srv")
	if len(c.DeletedMCPServers) != 1 || c.DeletedMCPServers[0] != "other" {
		t.Fatalf("rollback must remove only the target, got %v", c.DeletedMCPServers)
	}
	// Clearing a name that is not tombstoned is a no-op.
	c.ClearMCPDeleted("absent")
	if len(c.DeletedMCPServers) != 1 {
		t.Fatalf("clear of absent name must be a no-op, got %v", c.DeletedMCPServers)
	}
}
