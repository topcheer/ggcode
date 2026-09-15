package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// resetDisabledCache clears the package-level disabled-state cache so each
// test starts from disk truth.
func resetDisabledCache() {
	mcpDisabledMu.Lock()
	mcpDisabledGlobal = nil
	mcpDisabledWS = nil
	mcpDisabledCacheOK = false
	mcpDisabledMu.Unlock()
}

func disabledTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	resetDisabledCache()
	t.Cleanup(resetDisabledCache)
	return home
}

func writeDisabledFile(t *testing.T, home string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ggcode", "disabled_mcp.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	resetDisabledCache()
}

// TestMCPDisabledLegacyFormatFoldsIntoGlobal verifies the #2390 migration
// contract: a pre-#2390 bare-array file keeps its old disables-everywhere
// semantics - every legacy entry is read as a global-bucket entry.
func TestMCPDisabledLegacyFormatFoldsIntoGlobal(t *testing.T) {
	home := disabledTestHome(t)
	writeDisabledFile(t, home, []string{"legacy-server"})

	// Global semantics preserved: visible from every scope.
	if !MCPDisabledIn("", "legacy-server") {
		t.Fatal("legacy entry not visible in global scope")
	}
	if !MCPDisabledIn("/ws/other", "legacy-server") {
		t.Fatal("legacy entry not visible from another workspace (old semantics broken)")
	}
}

// TestMCPDisabledWorkspaceScopeIsolation is the #2390 repro probe: disabling
// a same-name server in workspace A must not disable it in workspace B or
// in the global scope.
func TestMCPDisabledWorkspaceScopeIsolation(t *testing.T) {
	home := disabledTestHome(t)

	if err := SetMCPDisabledIn("/ws/a", "github", true); err != nil {
		t.Fatalf("disable in ws A: %v", err)
	}
	if !MCPDisabledIn("/ws/a", "github") {
		t.Fatal("disabled in its own workspace")
	}
	if MCPDisabledIn("/ws/b", "github") {
		t.Fatal("#2390 regression: ws-A disable leaked into ws B")
	}
	if MCPDisabledIn("", "github") {
		t.Fatal("#2390 regression: ws-A disable leaked into the global scope")
	}

	// The on-disk shape is the scoped format.
	data, err := os.ReadFile(filepath.Join(home, ".ggcode", "disabled_mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var store disabledStore
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("store not the new object format: %v", err)
	}
	if len(store.Workspaces["/ws/a"]) != 1 || store.Workspaces["/ws/a"][0] != "github" {
		t.Fatalf("unexpected workspace bucket: %+v", store.Workspaces)
	}
}

// TestMCPDisabledManagerScope verifies the manager carries its scope: the
// connection-side gate (m.isDisabled) consults the manager's workspace
// bucket, not a bare global name match.
func TestMCPDisabledManagerScope(t *testing.T) {
	_ = disabledTestHome(t)

	if err := SetMCPDisabledIn("/ws/a", "fetch", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	mA := NewMCPManager(nil, nil, "/ws/a")
	mB := NewMCPManager(nil, nil, "/ws/b")
	if !mA.isDisabled("fetch") {
		t.Fatal("manager for ws A must see the disable")
	}
	if mB.isDisabled("fetch") {
		t.Fatal("#2390 regression: manager for ws B saw ws A's disable")
	}
	if NewMCPManager(nil, nil, "").isDisabled("fetch") {
		t.Fatal("empty-scope manager saw a workspace-only disable")
	}
}

// TestMCPDisabledEnableClearsBothBuckets verifies the toggle is decisive: a
// workspace-scope enable also removes a global-bucket disable, so the server
// the panel just reported as enabled cannot stay disabled after restart.
func TestMCPDisabledEnableClearsBothBuckets(t *testing.T) {
	_ = disabledTestHome(t)

	if err := SetMCPDisabledIn("", "filesystem", true); err != nil {
		t.Fatalf("global disable: %v", err)
	}
	if err := SetMCPDisabledIn("/ws/a", "filesystem", true); err != nil {
		t.Fatalf("workspace disable: %v", err)
	}
	if err := SetMCPDisabledIn("/ws/a", "filesystem", false); err != nil {
		t.Fatalf("workspace enable: %v", err)
	}
	if MCPDisabledIn("/ws/a", "filesystem") {
		t.Fatal("enable left the server disabled (global bucket survived the toggle)")
	}
	if MCPDisabledIn("", "filesystem") {
		t.Fatal("enable left the global-bucket entry in place")
	}
}

// TestMCPDisabledLegacyUpgradeOnWrite verifies lazy migration: after any
// write, a legacy bare-array file is upgraded to the scoped object format
// with the legacy entries preserved in the global bucket.
func TestMCPDisabledLegacyUpgradeOnWrite(t *testing.T) {
	home := disabledTestHome(t)
	writeDisabledFile(t, home, []string{"legacy-server"})

	if err := SetMCPDisabledIn("/ws/a", "github", true); err != nil {
		t.Fatalf("scoped write: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(home, ".ggcode", "disabled_mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var store disabledStore
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("file did not upgrade to the object format: %v", err)
	}
	if len(store.Global) != 1 || store.Global[0] != "legacy-server" {
		t.Fatalf("legacy entry lost on upgrade: %+v", store.Global)
	}
	if len(store.Workspaces["/ws/a"]) != 1 || store.Workspaces["/ws/a"][0] != "github" {
		t.Fatalf("scoped entry missing after upgrade: %+v", store.Workspaces)
	}
}
