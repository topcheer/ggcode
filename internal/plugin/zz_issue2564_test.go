package plugin

// Regression tests for #2564: the cold-cache write path in SetMCPDisabledIn
// bound mcpDisabledCacheOK to the readability of a second bare os.ReadFile
// instead of decodeDisabledStore's `cacheable` verdict, so a corrupt-but-
// readable disabled_mcp.json was cached as empty truth and one toggle then
// persisted that empty truth over the store (and later clobbered external
// repairs from the warm branch). The fix binds cacheOK to `cacheable`,
// mirroring the read path (loadMCPDisabledBuckets).

import (
	"os"
	"path/filepath"
	"testing"
)

// cacheOKSnapshot reads mcpDisabledCacheOK under the lock (test-only helper).
func cacheOKSnapshot() bool {
	mcpDisabledMu.Lock()
	defer mcpDisabledMu.Unlock()
	return mcpDisabledCacheOK
}

// writeRawDisabledBytes writes raw bytes to disabled_mcp.json and resets the
// cache. Unlike writeDisabledFile it does not marshal, so corrupt payloads
// can be planted.
func writeRawDisabledBytes(t *testing.T, home string, raw []byte) {
	t.Helper()
	path := filepath.Join(home, ".ggcode", "disabled_mcp.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	resetDisabledCache()
}

// TestV2564CorruptFileColdToggleKeepsCacheCold pins the core regression: a
// corrupt-but-readable store must NOT flip cacheOK after a cold-toggle. The
// write itself proceeding is the documented pre-#2390 design choice; the bug
// was caching the empty map as authoritative.
func TestV2564CorruptFileColdToggleKeepsCacheCold(t *testing.T) {
	home := disabledTestHome(t)
	// Truncated JSON: readable on disk, decodable = no.
	writeRawDisabledBytes(t, home, []byte(`{"global": ["context7"`))

	if err := SetMCPDisabledIn("", "alpha", true); err != nil {
		t.Fatalf("SetMCPDisabledIn: %v", err)
	}
	if cacheOKSnapshot() {
		t.Fatal("cacheOK=true after cold toggle against corrupt-but-readable file: empty truth was cached (#2564 regression)")
	}
	// The freshly written entry must still be visible via the read path
	// (which re-reads disk because the cache stayed cold).
	if !MCPDisabledIn("", "alpha") {
		t.Fatal("newly toggled entry not visible via read path")
	}
}

// TestV2564RepairClobberChainBroken pins the derived harm chain: after a
// toggle against a corrupt file, an external repair of the file must survive
// a SECOND toggle in the same session. With the fix, the cache stayed cold,
// so the second toggle re-decodes the repaired store and preserves it.
func TestV2564RepairClobberChainBroken(t *testing.T) {
	home := disabledTestHome(t)
	writeRawDisabledBytes(t, home, []byte(`{"global": ["context7"`))

	// Toggle #1 against the corrupt file (cache stays cold after fix).
	if err := SetMCPDisabledIn("", "alpha", true); err != nil {
		t.Fatalf("toggle #1: %v", err)
	}
	if cacheOKSnapshot() {
		t.Fatal("precondition: cache must be cold after corrupt-file toggle")
	}

	// External repair: full store restored from backup while ggcode runs.
	writeDisabledFile(t, home, disabledStore{
		Global:     []string{"context7"},
		Workspaces: map[string][]string{"/ws/proj": {"serena"}},
	})

	// Toggle #2 in the same session: cold cache re-decodes the repaired
	// store, so the persisted result must contain repair + both toggles.
	if err := SetMCPDisabledIn("", "beta", true); err != nil {
		t.Fatalf("toggle #2: %v", err)
	}

	global, ws := loadMCPDisabledBuckets()
	// alpha (toggle #1) was legitimately REPLACED by the external repair's
	// full-store overwrite; what must survive is the repair itself plus the
	// second toggle's entry.
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

// TestV2564MissingFileStillCaches is the control: a missing file is
// legitimately empty and MUST hydrate as cacheable (readability verdict and
// cacheable verdict agree here; the fix must not break the first-ever-toggle
// path).
func TestV2564MissingFileStillCaches(t *testing.T) {
	home := disabledTestHome(t)
	_ = home // no file written

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

// TestV2564ZeroByteFileKeepsCacheCold pins the 0-byte variant of the corrupt
// file (a realistic damage form: editor crash, sync-tool placeholder).
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

// Guard against accidental test-pollution: the package-level cache is shared,
// so every test here goes through disabledTestHome/writeRawDisabledBytes which
// reset it (and re-reset on cleanup).
