package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestBareAddRateLimited: #5892 round-4 - ephemeral per-request Managers
// storm the sink with insert-bare-add records (112 records in 47s from
// two non-leader PIDs). The per-process rate gate must keep the burst to
// a single record and carry the suppressed count forward on the next
// written bare-add record, so volume stays observable without the storm.
func TestBareAddRateLimited(t *testing.T) {
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
	defer ResetPrefixDriftForTest()
	ResetPrefixDriftForTest()

	origWindow := prefixDriftBareAddWindow
	defer func() { prefixDriftBareAddWindow = origWindow }()

	bareAdd := func(text string) {
		m := NewManager(128000)
		m.Add(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: text}},
		})
	}

	readSinks := func() string {
		sinks, err := filepath.Glob(filepath.Join(home, ".ggcode", "prefix-drift", "*.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		for _, s := range sinks {
			data, err := os.ReadFile(s)
			if err != nil {
				t.Fatalf("read sink %s: %v", s, err)
			}
			sb.Write(data)
		}
		return sb.String()
	}

	// Leg 1: 10 rapid bare-adds (one per ephemeral Manager, all inside
	// the window) collapse into exactly one record.
	prefixDriftBareAddWindow = 500 * time.Millisecond
	for i := 0; i < 10; i++ {
		bareAdd("storm")
	}
	raw := readSinks()
	lines := nonEmptyLines(raw)
	if len(lines) != 1 {
		t.Fatalf("10 rapid bare-adds must collapse to 1 record, got %d:\n%s", len(lines), raw)
	}
	var first prefixDriftRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("record not valid JSON: %v", err)
	}
	if first.Event != prefixDriftBareAddEvent {
		t.Fatalf("expected event=%s, got %s", prefixDriftBareAddEvent, first.Event)
	}
	if first.Sampled || first.Suppressed != 0 {
		t.Fatalf("first record of a burst must not carry sampled/suppressed, got %+v", first)
	}

	// Leg 2: past the window, the next bare-add writes its own record and
	// carries the suppressed count of the burst.
	prefixDriftBareAddWindow = 20 * time.Millisecond
	time.Sleep(50 * time.Millisecond)
	bareAdd("after-window")

	raw = readSinks()
	lines = nonEmptyLines(raw)
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 records after window elapse, got %d:\n%s", len(lines), raw)
	}
	var second prefixDriftRecord
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("record not valid JSON: %v", err)
	}
	if !second.Sampled {
		t.Fatal("second record must carry sampled:true")
	}
	if second.Suppressed != 9 {
		t.Fatalf("second record must carry suppressed:9, got %d", second.Suppressed)
	}
}
