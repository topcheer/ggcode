package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue5575WiringPins anchors the #5575 P0 production wiring: the CLI
// A2A server construction must forward the config-backed introspection mode.
// A regression that drops the wiring leaves a2a.introspection inert (flag
// parsed but never applied), reproducing the #5527-style wiring gap.
func TestIssue5575WiringPins(t *testing.T) {
	anchors := []struct {
		file string
		need string
		what string
	}{
		{"cmd/ggcode/root.go", "IntrospectionMode: cfg.A2A.Introspection", "startA2AServer must wire a2a.introspection mode into the server"},
		{"internal/a2a/server.go", "ValidIntrospectionMode(cfg.IntrospectionMode)", "NewServer must arm the introspection gate from ServerConfig"},
		{"internal/a2a/server.go", "LoadOrCreateDomainKey(handler.workspace)", "NewServer must resolve the workspace-family domain key (.git/a2a.key)"},
		{"internal/a2a/server.go", "IntrospectionMethodPrefix", "routeRPC default branch must route runtime/* when armed"},
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd)) // cmd/ggcode -> repo root
	for _, a := range anchors {
		data, rerr := os.ReadFile(filepath.Join(root, a.file))
		if rerr != nil {
			t.Fatalf("read %s: %v", a.file, rerr)
		}
		if !strings.Contains(string(data), a.need) {
			t.Errorf("#5575 wiring pin broken: %s (%s): %q not found", a.file, a.what, a.need)
		}
	}
}
