package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func sysMsgFor5812(text string) provider.Message {
	return provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: text}}}
}

// #5812: ctxstats sampler pins.
//  1. append-only traffic keeps head_sha256 constant (drift_event stays false)
//  2. a deliberate system-head rewrite flips drift_event with reason recorded
//  3. unexplained drift (no reason) is classified "unknown"
func TestCtxStatsStableHeadAcrossAppends(t *testing.T) {
	dir := t.TempDir()
	origHome, hadHome := os.LookupEnv("HOME")
	t.Setenv("HOME", dir)
	defer func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
	}()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "ctx-sample"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(100000)
	m.Add(sysMsgFor5812("stable head"))
	m.RecordProviderUsage(100, 10, 200)
	m.RecordTurn("expected-append")
	for i := 0; i < 3; i++ {
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "turn"}}})
		m.RecordProviderUsage(100, 0, 200)
		m.RecordTurn("expected-append")
	}

	path := filepath.Join(dir, ".ggcode", "ctxstats-*.jsonl")
	matches, _ := filepath.Glob(path)
	if len(matches) != 1 {
		t.Fatalf("expected one ctxstats file, got %d", len(matches))
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 records, got %d", len(lines))
	}
	var first, last ctxStatsRecord
	if err := decodeJSONL(lines[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := decodeJSONL(lines[len(lines)-1], &last); err != nil {
		t.Fatal(err)
	}
	if first.DriftEvent {
		t.Fatalf("first record must not drift: %+v", first)
	}
	if last.DriftEvent {
		t.Fatalf("append-only traffic must keep head stable (drift=%v head=%s)", last.DriftEvent, last.HeadSHA256)
	}
	if last.HitRatio != 1.0 {
		t.Fatalf("all-hit usage should yield hit_ratio 1.0, got %.2f", last.HitRatio)
	}
}

func TestCtxStatsDriftOnSystemRewrite(t *testing.T) {
	dir := t.TempDir()
	origHome, hadHome := os.LookupEnv("HOME")
	t.Setenv("HOME", dir)
	defer func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
	}()
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", "ctx-sample"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(100000)
	m.Add(sysMsgFor5812("stable head v1"))
	m.RecordProviderUsage(10, 100, 200)
	m.RecordTurn("")

	m.UpdateFirstSystemMessage(sysMsgFor5812("stable head v2"))
	m.RecordProviderUsage(10, 100, 200)
	m.RecordTurn("") // no reason: must classify unknown + drift

	matches, _ := filepath.Glob(filepath.Join(dir, ".ggcode", "ctxstats-*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("expected one ctxstats file, got %d", len(matches))
	}
	body, _ := os.ReadFile(matches[0])
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 records, got %d", len(lines))
	}
	var rec ctxStatsRecord
	if err := decodeJSONL(lines[1], &rec); err != nil {
		t.Fatal(err)
	}
	if !rec.DriftEvent {
		t.Fatalf("system rewrite must set drift_event, got %+v", rec)
	}
	if rec.DriftReason != "unknown" {
		t.Fatalf("reason-less drift must classify unknown, got %q", rec.DriftReason)
	}
	if rec.HitRatio > 0.1 {
		t.Fatalf("all-write usage should yield near-zero hit ratio, got %.2f", rec.HitRatio)
	}
}

func decodeJSONL(line string, out *ctxStatsRecord) error {
	return json.Unmarshal([]byte(line), out)
}
