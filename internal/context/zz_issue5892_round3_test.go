package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestAddBareSystemHeadInstrumented: #5892 round-3 - a system-role Add on
// an empty message list lands at the head position (a bare head write).
// This previously bypassed drift instrumentation entirely, which is why
// the archive showed rewrites whose OLD head had no matching insert
// record. The Add path must now surface these as insert-bare-add events.
func TestAddBareSystemHeadInstrumented(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	anchor := filepath.Join(home, ".ggcode", "prefixdrifting")
	if err := os.WriteFile(anchor, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(128000)
	m.Add(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "bare head"}},
	})
	msgs := m.Messages()
	if len(msgs) != 1 || msgs[0].Role != "system" {
		t.Fatalf("expected single system head, got %+v", msgs)
	}

	sinks, err := filepath.Glob(filepath.Join(home, ".ggcode", "prefix-drift", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) == 0 {
		t.Fatal("expected a drift sink file after bare system Add")
	}
	data, err := os.ReadFile(sinks[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "insert-bare-add") {
		t.Fatalf("bare system Add on empty list must record insert-bare-add event; sink:\n%s", data)
	}
}
