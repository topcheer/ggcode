package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssue2595_LinkDefaultsValidFromToNow(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"depends-on"}`)

	early := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	r := kgMustOK(t, tool, `{"action":"trace","id":"a","as_of":"`+early+`"}`)
	if strings.Contains(r.Content, "depends-on") {
		t.Fatalf("edge linked now must not be visible at as_of=%s, got: %s", early, r.Content)
	}

	r = kgMustOK(t, tool, `{"action":"trace","id":"a"}`)
	if !strings.Contains(r.Content, "depends-on") {
		t.Fatalf("edge must be visible in the current view, got: %s", r.Content)
	}

	bd := kgSetup(t)
	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	kgMustOK(t, bd, `{"action":"link","id":"a","to":"b","type":"depends-on","valid_from":"`+past+`"}`)
	r = kgMustOK(t, bd, `{"action":"trace","id":"a","as_of":"`+past+`"}`)
	if !strings.Contains(r.Content, "depends-on") {
		t.Fatalf("explicitly backdated edge must be visible from its valid_from, got: %s", r.Content)
	}
}

func TestIssue2596_CloneResetsDirPinnedCache(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	orig := &KnowledgeGraphTool{WorkingDir: dirA}

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

	c.WorkingDir = dirB
	kgMustOK(t, c, `{"action":"add","id":"b","type":"entity","title":"B"}`)
	if _, err := os.Stat(filepath.Join(dirB, ".ggcode", kgFileName)); err != nil {
		t.Fatalf("clone must write to dirB after WorkingDir override: %v", err)
	}

	r := kgMustOK(t, c, `{"action":"stats"}`)
	if strings.Contains(r.Content, `"nodes": 2`) || strings.Contains(r.Content, `"nodes":2`) {
		t.Fatalf("dirB graph must not contain dirA's nodes (cross-workspace leak), stats: %s", r.Content)
	}
	_ = json.RawMessage{}
}
