package tool

import (
	"encoding/json"
	"testing"
)

func TestIssue4479_CrossInstanceLostUpdateMerged(t *testing.T) {
	a, dir := newKGTool(t)
	b := &KnowledgeGraphTool{WorkingDir: dir}

	sa, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.load()
	if err != nil {
		t.Fatal(err)
	}

	sa.Nodes["node-a"] = &kgNode{Title: "A", Type: "note"}
	if err := a.save(sa); err != nil {
		t.Fatal(err)
	}

	sb.Nodes["node-b"] = &kgNode{Title: "B", Type: "note"}
	sb.Edges = append(sb.Edges, kgEdge{From: "node-b", To: "node-a", Type: "relates-to"})
	if err := b.save(sb); err != nil {
		t.Fatal(err)
	}

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

func TestIssue4479_MergePrefersMemOnCollision(t *testing.T) {
	_, dir := newKGTool(t)
	a := &KnowledgeGraphTool{WorkingDir: dir}
	sa, _ := a.load()
	sa.Nodes["shared"] = &kgNode{Title: "disk version", Type: "note"}
	if err := a.save(sa); err != nil {
		t.Fatal(err)
	}

	b := &KnowledgeGraphTool{WorkingDir: dir}
	sb, _ := b.load()
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

func TestIssue4479_MergedStoreRoundTrips(t *testing.T) {
	disk := &kgStore{Nodes: nil, Edges: nil}
	mem := &kgStore{Nodes: map[string]*kgNode{"x": {Title: "x"}}, Edges: []kgEdge{}}
	out := mergeKGStores(disk, mem)
	if _, err := json.Marshal(out); err != nil {
		t.Fatalf("merged store not serializable: %v", err)
	}
}
