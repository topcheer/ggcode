package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/mcptrust"
	"github.com/topcheer/ggcode/internal/plugin"
)

func TestRunMCPTrustListAndReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trust.json")
	t.Setenv("GGCODE_MCP_TRUST", path)

	// Empty state.
	var out bytes.Buffer
	if err := runMCPTrustList(&out); err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if !strings.Contains(out.String(), "No trust baselines yet") {
		t.Fatalf("unexpected empty output: %q", out.String())
	}

	// Seed a baseline by hand.
	store, err := mcptrust.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Apply("github", []mcptrust.ToolFingerprint{{Name: "create_issue", Hash: "abcdef1234567890"}}, time.Now())
	if err := store.Save(path); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runMCPTrustList(&out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "github") || !strings.Contains(text, "create_issue") || !strings.Contains(text, "abcdef12") {
		t.Fatalf("listing missing fields: %q", text)
	}

	// Reset via the plugin wrapper (same path the CLI uses).
	if err := plugin.ResetMCPTrustBaseline("github"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := plugin.ResetMCPTrustBaseline("github"); err == nil {
		t.Fatal("second reset must fail (no baseline)")
	}

	// Disabled mode.
	t.Setenv("GGCODE_MCP_TRUST", "off")
	out.Reset()
	if err := runMCPTrustList(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "disabled") {
		t.Fatalf("disabled note missing: %q", out.String())
	}

	_ = os.RemoveAll(path)
}
