package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #2595: link without valid_from must anchor the edge's ValidFrom at the link
// moment (schema: "defaults to now"), NOT leave it nil (= negative infinity).
// Before the fix, trace as_of=<any early time> showed edges that were linked
// much later -- the fact appeared true before it existed.
func TestIssue2595_LinkDefaultsValidFromToNow(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on"}`)

	// Far-early as_of must NOT see the edge anymore.
	early := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	r := kgMustOK(t, tool, `{"action":"trace","id":"a","as_of":"`+early+`"}`)
	if strings.Contains(r.Content, "depends-on") {
		t.Fatalf("edge linked now must not be visible at as_of=%s, got: %s", early, r.Content)
	}

	// Present-day view still sees it (the link is valid from now on).
	r = kgMustOK(t, tool, `{"action":"trace","id":"a"}`)
	if !strings.Contains(r.Content, "depends-on") {
		t.Fatalf("edge must be visible in the current view, got: %s", r.Content)
	}

	// Explicit valid_from keeps its backdated semantics (existing behavior).
	bd := kgSetup(t)
	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	kgMustOK(t, bd, `{"action":"link","id":"a","to":"b","type":"depends-on","valid_from":"`+past+`"}`)
	r = kgMustOK(t, bd, `{"action":"trace","id":"a","as_of":"`+past+`"}`)
	if !strings.Contains(r.Content, "depends-on") {
		t.Fatalf("explicitly backdated edge must be visible from its valid_from, got: %s", r.Content)
	}
}

// #2596: KnowledgeGraphTool holds WorkingDir + a path/cache pinned to it, so
// it MUST implement Cloner. Without Clone, the registry shared one instance:
// the first agent to touch the graph pinned filePath/cache/loaded forever and
// every later agent (sub-agent, teammate, or the main agent after
// enter_worktree) silently wrote into the wrong workspace.
func TestIssue2596_CloneResetsDirPinnedCache(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	orig := &KnowledgeGraphTool{WorkingDir: dirA}
	// Pin the cache to dirA: add a node, which resolves and caches filePath.
	kgMustOK(t, orig, `{"action":"add","id":"a","type":"decision","title":"A"}`)
	if orig.filePath == "" || !orig.loaded {
		t.Fatal("precondition: original instance must have pinned filePath/cache")
	}
	if _, err := os.Stat(filepath.Join(dirA, ".ggcode", kgFileName)); err != nil {
		t.Fatalf("graph file must exist in dirA: %v", err)
	}

	c, ok := orig.Clone().(*KnowledgeGraphTool)
	if !ok {
		t.Fatal("Clone must return a *KnowledgeGraphTool")
	}
	if c.filePath != "" || c.loaded || c.cache != nil {
		t.Fatalf("clone must start with a clean cache (filePath=%q loaded=%v cache=%v)", c.filePath, c.loaded, c.cache)
	}

	// The clone re-resolves against ITS WorkingDir, not the pinned dirA.
	c.WorkingDir = dirB
	kgMustOK(t, c, `{"action":"add","id":"b","type":"entity","title":"B"}`)
	if _, err := os.Stat(filepath.Join(dirB, ".ggcode", kgFileName)); err != nil {
		t.Fatalf("clone must write to dirB after WorkingDir override: %v", err)
	}

	// And dirB's graph holds only the clone's node (no cross-pollination
	// from dirA's "a"): total node count is 1, not 2.
	r := kgMustOK(t, c, `{"action":"stats"}`)
	if strings.Contains(r.Content, `"nodes": 2`) || strings.Contains(r.Content, `"nodes":2`) {
		t.Fatalf("dirB graph must not contain dirA's nodes (cross-workspace leak), stats: %s", r.Content)
	}
	_ = json.RawMessage{}
}
