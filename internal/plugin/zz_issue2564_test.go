package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func cacheOKSnapshot() bool {
	mcpDisabledMu.Lock()
	defer mcpDisabledMu.Unlock()
	return mcpDisabledCacheOK
}

func writeRawDisabledBytes(t *testing.T, home string, raw []byte) {
	t.Helper()
	path := filepath.Join(home, ".ggcode", "disabled_mcp.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	resetDisabledCache()
}

func TestV2564CorruptFileColdToggleKeepsCacheCold(t *testing.T) {
	home := disabledTestHome(t)

	writeRawDisabledBytes(t, home, []byte(`{"global": ["context7"`))

	if err := SetMCPDisabledIn("", "alpha", true); err != nil {
		t.Fatalf("SetMCPDisabledIn: %v", err)
	}
	if cacheOKSnapshot() {
		t.Fatal("cacheOK=true after cold toggle against corrupt-but-readable file: empty truth was cached (#2564 regression)")
	}

	if !MCPDisabledIn("", "alpha") {
		t.Fatal("newly toggled entry not visible via read path")
	}
}

func TestV2564RepairClobberChainBroken(t *testing.T) {
	home := disabledTestHome(t)
	writeRawDisabledBytes(t, home, []byte(`{"global": ["context7"`))

	if err := SetMCPDisabledIn("", "alpha", true); err != nil {
		t.Fatalf("toggle #1: %v", err)
	}
	if cacheOKSnapshot() {
		t.Fatal("precondition: cache must be cold after corrupt-file toggle")
	}

	writeDisabledFile(t, home, disabledStore{
		Global:     []string{"context7"},
		Workspaces: map[string][]string{"/ws/proj": {"serena"}},
	})

	if err := SetMCPDisabledIn("", "beta", true); err != nil {
		t.Fatalf("toggle #2: %v", err)
	}

	global, ws := loadMCPDisabledBuckets()

	for _, name := range []string{"context7", "beta"} {
		if !global[name] {
			t.Fatalf("entry %q lost after post-repair toggle (repair-clobber chain): global=%v", name, global)
		}
	}
	if !ws["/ws/proj"]["serena"] {
		t.Fatal("workspace bucket entry serena lost after post-repair toggle")
	}
	if !cacheOKSnapshot() {
		t.Fatal("cache should be warm after successfully decoding the repaired store")
	}
}

func TestV2564MissingFileStillCaches(t *testing.T) {
	home := disabledTestHome(t)
	_ = home

	if err := SetMCPDisabledIn("/ws/x", "first", true); err != nil {
		t.Fatalf("SetMCPDisabledIn: %v", err)
	}
	if !cacheOKSnapshot() {
		t.Fatal("missing file must hydrate as cacheable (legitimately empty)")
	}
	if !MCPDisabledIn("/ws/x", "first") {
		t.Fatal("first toggle not visible")
	}
}

func TestV2564ZeroByteFileKeepsCacheCold(t *testing.T) {
	home := disabledTestHome(t)
	writeRawDisabledBytes(t, home, []byte{})

	if err := SetMCPDisabledIn("", "alpha", true); err != nil {
		t.Fatalf("SetMCPDisabledIn: %v", err)
	}
	if cacheOKSnapshot() {
		t.Fatal("cacheOK=true after cold toggle against 0-byte file: empty truth was cached")
	}
}
