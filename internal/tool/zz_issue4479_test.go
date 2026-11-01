package tool

import (
	"encoding/json"
	"testing"
)

// #4479 bug 1: per-instance caches on one shared store file. Two tool
// instances (Registry.Clone hands each teammate its own) that both load
// before either saves used to silently drop the other's writes: the second
// whole-file rename overwrote the first. save must re-read disk fresh and
// merge (union).
func TestIssue4479_CrossInstanceLostUpdateMerged(t *testing.T) {
	a, dir := newKGTool(t)
	b := &KnowledgeGraphTool{WorkingDir: dir} // second "seat"

	// Both seats load while the disk is still empty - each now holds an
	// empty snapshot and never re-reads on its own.
	sa, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.load()
	if err != nil {
		t.Fatal(err)
	}

	// Seat A writes first.
	sa.Nodes["node-a"] = &kgNode{Title: "A", Type: "note"}
	if err := a.save(sa); err != nil {
		t.Fatal(err)
	}

	// Seat B (stale, empty cache) writes second: without merge-on-save this
	// rename silently dropped node-a.
	sb.Nodes["node-b"] = &kgNode{Title: "B", Type: "note"}
	sb.Edges = append(sb.Edges, kgEdge{From: "node-b", To: "node-a", Type: "relates-to"})
	if err := b.save(sb); err != nil {
		t.Fatal(err)
	}

	// A FRESH instance (no stale cache) must see both seats' work.
	c := &KnowledgeGraphTool{WorkingDir: dir}
	sc, err := c.load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sc.Nodes["node-a"]; !ok {
		t.Fatalf("seat A's node lost by seat B's save: nodes=%v", sc.Nodes)
	}
	if _, ok := sc.Nodes["node-b"]; !ok {
		t.Fatalf("seat B's node missing: nodes=%v", sc.Nodes)
	}
	if len(sc.Edges) != 1 || sc.Edges[0].From != "node-b" {
		t.Fatalf("seat B's edge lost or mangled: %+v", sc.Edges)
	}
}

// Same-id collision: the saving seat's version wins (it was built on the
// latest state that seat saw), disk-only nodes are preserved.
func TestIssue4479_MergePrefersMemOnCollision(t *testing.T) {
	_, dir := newKGTool(t)
	a := &KnowledgeGraphTool{WorkingDir: dir}
	sa, _ := a.load()
	sa.Nodes["shared"] = &kgNode{Title: "disk version", Type: "note"}
	if err := a.save(sa); err != nil {
		t.Fatal(err)
	}

	// Seat B never saw that write and re-creates the id with new content.
	b := &KnowledgeGraphTool{WorkingDir: dir}
	sb, _ := b.load() // reads disk: sees "disk version"
	sb.Nodes["shared"] = &kgNode{Title: "mem version", Type: "note"}
	sb.Nodes["disk-only"] = &kgNode{Title: "kept", Type: "note"}
	if err := b.save(sb); err != nil {
		t.Fatal(err)
	}

	c := &KnowledgeGraphTool{WorkingDir: dir}
	sc, _ := c.load()
	if sc.Nodes["shared"].Title != "mem version" {
		t.Fatalf("saving seat's same-id write must win, got %q", sc.Nodes["shared"].Title)
	}
	if _, ok := sc.Nodes["disk-only"]; !ok {
		t.Fatalf("disk-only node must survive merge: %v", sc.Nodes)
	}
}

// Guard: the merged store must round-trip through JSON with no nil maps
// (load's own migration relies on this shape).
func TestIssue4479_MergedStoreRoundTrips(t *testing.T) {
	disk := &kgStore{Nodes: nil, Edges: nil}
	mem := &kgStore{Nodes: map[string]*kgNode{"x": {Title: "x"}}, Edges: []kgEdge{}}
	out := mergeKGStores(disk, mem)
	if _, err := json.Marshal(out); err != nil {
		t.Fatalf("merged store not serializable: %v", err)
	}
}
