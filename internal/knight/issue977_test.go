package knight

// Tests for issue #977 fixes:
//  1. In-process mutual exclusion for analyzeRecentSessions (tick vs
//     PerformSkillAnalysis double-analysis TOCTOU).
//  2. Symmetric A/B replay fingerprints (baseline includes name/description).
//  3. Eval log hardening: RawOutput truncation, oversized-line skipping,
//     size-based rotation.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// barrierStore widens the eligibility-check race window deterministically:
// the first List() blocks until a second concurrent List() arrives (or a
// short timeout expires), so a missing in-flight guard reliably leads to a
// double analysis.
type barrierStore struct {
	session.Store
	lists atomic.Int32
	loads atomic.Int32
}

func (s *barrierStore) List() ([]*session.Session, error) {
	s.lists.Add(1)
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		if s.loads.Load() >= 0 && s.lists.Load() >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	return s.Store.List()
}

func (s *barrierStore) Load(id string) (*session.Session, error) {
	s.loads.Add(1)
	return s.Store.Load(id)
}

// TestAnalyzeRecentSessionsInFlightMutualExclusion verifies that concurrent
// entry points (the tick path via analyzeRecentSessions and the daemon
// command path via PerformSkillAnalysis) never analyze the same session
// twice: the in-flight guard must make exactly one of them do the work.
func TestAnalyzeRecentSessionsInFlightMutualExclusion(t *testing.T) {
	dir := t.TempDir()
	homeDir := filepath.Join(dir, "home")
	projDir := filepath.Join(dir, "project")
	storeDir := filepath.Join(homeDir, ".ggcode", "sessions")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	base, err := session.NewJSONLStore(storeDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ses := session.NewSession("zai", "test", "test-model")
	base.AppendMessage(ses, provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "run the build"}},
	})
	base.AppendMessage(ses, provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "I will create a debug binary"},
			{Type: "tool_use", ToolName: "run_command", ToolID: "b1"},
		},
	})
	base.AppendMessage(ses, provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "你需要编译的是正式的 ggcode 二进制而不是什么 debug 二进制，用 make build"},
		},
	})
	base.AppendMessage(ses, provider.Message{
		Role:    "assistant",
		Content: []provider.ContentBlock{{Type: "text", Text: "Understood, using make build"}},
	})

	store := &barrierStore{Store: base}
	cfg := config.DefaultKnightConfig()
	cfg.Enabled = true
	k := New(cfg, homeDir, projDir, store)
	// Set running before spawning goroutines so the unsynchronized read in
	// CanPerformTask has a happens-before edge (mirrors production Start).
	k.mu.Lock()
	k.running = true
	k.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := k.analyzeRecentSessions(context.Background()); err != nil {
			t.Errorf("tick-path analyzeRecentSessions: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := k.PerformSkillAnalysis(context.Background()); err != nil {
			t.Errorf("daemon-path PerformSkillAnalysis: %v", err)
		}
	}()
	wg.Wait()

	if got := store.loads.Load(); got != 1 {
		t.Fatalf("session analyzed %d times across concurrent entry points; want exactly 1 (in-flight guard missing or ineffective)", got)
	}
}

// TestBaselineReplayBodyIncludesNameAndDescription verifies the baseline
// fingerprint carries the entry's name and description alongside the body,
// keeping the A/B replay comparison symmetric with the candidate side.
func TestBaselineReplayBodyIncludesNameAndDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.md")
	body := "run the standard release checklist"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	e := &SkillEntry{
		Name: "github-actions",
		Meta: SkillMeta{Description: "github actions workflow deployment"},
		Path: path,
	}
	got := baselineReplayBody(e)
	for _, want := range []string{e.Name, e.Meta.Description, body} {
		if !strings.Contains(got, want) {
			t.Fatalf("baselineReplayBody(%+v) = %q, missing %q", e, got, want)
		}
	}
	// Unreadable body still fingerprints name+description.
	e2 := &SkillEntry{Name: "n", Meta: SkillMeta{Description: "d"}, Path: filepath.Join(dir, "missing.md")}
	if got2 := baselineReplayBody(e2); !strings.Contains(got2, "n") || !strings.Contains(got2, "d") {
		t.Fatalf("baselineReplayBody with unreadable path = %q, want name+description", got2)
	}
	if baselineReplayBody(nil) != "" {
		t.Fatal("baselineReplayBody(nil) should be empty")
	}
}

// TestABReplayBaselineFingerprintSymmetric verifies through computeABReplayScore
// that a baseline whose name/description match recent scenarios scores higher
// than a body-only fingerprint - i.e., baseline trigger words now count.
func TestABReplayBaselineFingerprintSymmetric(t *testing.T) {
	scenarios := []SkillScenarioLogEntry{
		{Task: "deploy via github actions workflow", Success: true},
	}
	cand := &SkillEntry{Name: "ci-deploy", Meta: SkillMeta{Description: "deploy the service"}}
	base := &SkillEntry{
		Name: "github-actions",
		Meta: SkillMeta{Description: "github actions workflow deployment"},
	}
	body := "unrelated checklist body text"
	withMeta := computeABReplayScore(cand, body, baselineReplayBody(base), scenarios)
	withoutMeta := computeABReplayScore(cand, body, body, scenarios)
	if withMeta.BaselineScore <= withoutMeta.BaselineScore {
		t.Fatalf("baseline fingerprint including name/desc should raise BaselineScore: got %v (with meta) vs %v (body only)",
			withMeta.BaselineScore, withoutMeta.BaselineScore)
	}
}

// TestParseAutoPromoteEvalDecisionTruncatesRawOutput verifies oversized LLM
// output is truncated at parse time while structured fields still parse.
func TestParseAutoPromoteEvalDecisionTruncatesRawOutput(t *testing.T) {
	out := strings.Repeat("a", 200*1024) + "\npromote: yes\nreplay: pass\nrationale: good enough\n"
	d := parseAutoPromoteEvalDecision(out)
	if !d.Promote {
		t.Fatal("promote field should still parse")
	}
	if !d.ReplayPassed {
		t.Fatal("replay field should still parse")
	}
	if !strings.Contains(d.Rationale, "good enough") {
		t.Fatalf("rationale = %q, want parsed value", d.Rationale)
	}
	if max := evalLogMaxRawOutput + 64; len(d.RawOutput) > max {
		t.Fatalf("RawOutput len = %d, want <= %d", len(d.RawOutput), max)
	}
}

// TestAppendAutoPromoteEvalTruncatesRawOutput verifies the append choke point
// truncates RawOutput even when the decision struct carries a huge value.
func TestAppendAutoPromoteEvalTruncatesRawOutput(t *testing.T) {
	dir := t.TempDir()
	k := New(config.DefaultKnightConfig(), dir, dir, nil)
	entry := &SkillEntry{Name: "s", Scope: "project", Path: "p"}
	decision := autoPromoteEvalDecision{Promote: true, RawOutput: strings.Repeat("x", 300*1024)}
	k.appendAutoPromoteEval(entry, decision)
	ev, err := k.RecentAutoPromoteEvals(10)
	if err != nil {
		t.Fatalf("RecentAutoPromoteEvals: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("entries = %d, want 1", len(ev))
	}
	if max := evalLogMaxRawOutput + 64; len(ev[0].RawOutput) > max {
		t.Fatalf("persisted RawOutput len = %d, want <= %d", len(ev[0].RawOutput), max)
	}
}

// TestRecentAutoPromoteEvalsSkipsOversizedLines verifies an oversized line in
// the middle of the log is skipped individually without failing the whole
// read, so surrounding valid entries are still returned.
func TestRecentAutoPromoteEvalsSkipsOversizedLines(t *testing.T) {
	dir := t.TempDir()
	k := New(config.DefaultKnightConfig(), dir, dir, nil)
	logPath := filepath.Join(dir, ".ggcode", "skill-auto-promote-evals.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	good1 := `{"time":"2026-01-01T00:00:00Z","skill":"a"}`
	good2 := `{"time":"2026-01-02T00:00:00Z","skill":"b"}`
	content := good1 + "\n" + strings.Repeat("z", evalLogMaxLineLen+4096) + "\n" + good2 + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev, err := k.RecentAutoPromoteEvals(0)
	if err != nil {
		t.Fatalf("oversized line must not fail the whole read: %v", err)
	}
	if len(ev) != 2 {
		t.Fatalf("entries = %d, want 2 (oversized line skipped)", len(ev))
	}
}

// TestAutoPromoteEvalLogRotation verifies the log rotates to ".1" once it
// exceeds the configured size threshold, keeping one previous generation.
func TestAutoPromoteEvalLogRotation(t *testing.T) {
	dir := t.TempDir()
	k := New(config.DefaultKnightConfig(), dir, dir, nil)
	logPath := filepath.Join(dir, ".ggcode", "skill-auto-promote-evals.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	old := strings.Repeat("{\"skill\":\"old\"}\n", 32)
	if err := os.WriteFile(logPath, []byte(old), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	origThreshold := evalLogRotateBytes
	evalLogRotateBytes = 64
	defer func() { evalLogRotateBytes = origThreshold }()

	k.appendAutoPromoteEval(&SkillEntry{Name: "s", Scope: "project"}, autoPromoteEvalDecision{Promote: true})

	rotated, err := os.ReadFile(logPath + ".1")
	if err != nil {
		t.Fatalf("rotated generation missing: %v", err)
	}
	if !strings.Contains(string(rotated), `"old"`) {
		t.Fatal("rotated file should hold the previous entries")
	}
	cur, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile current log: %v", err)
	}
	if strings.Contains(string(cur), `"old"`) {
		t.Fatal("current log should contain only the new entry after rotation")
	}
	if !strings.Contains(string(cur), `"skill":"s"`) {
		t.Fatal("current log should contain the newly appended entry")
	}
}
