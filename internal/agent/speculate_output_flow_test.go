package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// ofsMockTool is a minimal tool.Tool whose Execute returns canned content
// and records the number of executions, for output-flow speculation tests.
type ofsMockTool struct {
	name    string
	content string
	execN   int
}

func (m *ofsMockTool) Name() string        { return m.name }
func (m *ofsMockTool) Description() string { return "mock " + m.name }
func (m *ofsMockTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (m *ofsMockTool) Execute(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	m.execN++
	return tool.Result{Content: m.content}, nil
}

func TestRecordOutput_StoresAndClears(t *testing.T) {
	s := newSpeculator()

	s.recordOutput("grep", "MATCH", false)
	s.mu.Lock()
	if s.lastOutput != "MATCH" {
		t.Fatalf("expected stored output, got %q", s.lastOutput)
	}
	s.mu.Unlock()

	// Non-source tool clears the stored output.
	s.recordOutput("edit_file", "irrelevant", false)
	s.mu.Lock()
	if s.lastOutput != "" {
		t.Fatalf("expected output cleared for non-source tool, got %q", s.lastOutput)
	}
	s.mu.Unlock()

	// Failed search clears too.
	s.recordOutput("grep", "MATCH", false)
	s.recordOutput("grep", "later", true)
	s.mu.Lock()
	if s.lastOutput != "" {
		t.Fatalf("expected output cleared on error result, got %q", s.lastOutput)
	}
	s.mu.Unlock()

	// Oversized output is truncated to the cap.
	big := make([]byte, specOutputFlowOutputCap+100)
	for i := range big {
		big[i] = 'x'
	}
	s.recordOutput("grep", string(big), false)
	s.mu.Lock()
	got := len(s.lastOutput)
	s.mu.Unlock()
	if got != specOutputFlowOutputCap {
		t.Fatalf("expected truncation to %d bytes, got %d", specOutputFlowOutputCap, got)
	}
}

func TestExtractOutputFlowPaths_Ranking(t *testing.T) {
	output := "/repo/main.go:10:\tfunc A\n" +
		"noise without extension\n" +
		"/repo/util.go:3:\tx\n" +
		"/repo/main.go:22:\tfunc B\n" +
		"https://example.com/skip.go:1:\turl\n" +
		"/repo/main.go\n"

	cands := extractOutputFlowPaths(output)
	if len(cands) == 0 {
		t.Fatal("expected candidates")
	}
	if cands[0].rawPath != "/repo/main.go" {
		t.Fatalf("expected denser /repo/main.go first, got %v", cands[0])
	}
	if cands[0].count != 3 {
		t.Errorf("expected count 3 for /repo/main.go, got %d", cands[0].count)
	}
	for _, c := range cands {
		if c.rawPath == "https://example.com/skip.go" {
			t.Error("URL must not be extracted as a read candidate")
		}
		if !filepath.IsAbs(c.rawPath) && filepath.Ext(c.rawPath) == "" {
			t.Errorf("extension-less candidate leaked: %q", c.rawPath)
		}
	}
}

func TestMaybeSpeculateFromOutput_PreReadsTopCandidate(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hit.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newSpeculator()
	s.ttl = 30 * time.Second

	// One bigram demonstration: grep → read_file.
	s.recordObservation("grep")
	s.recordObservation("read_file")

	grepOut := target + ":1:\tpackage main\n" + target + ":2:\tfunc x\n"
	s.recordOutput("grep", grepOut, false)

	registry := tool.NewRegistry()
	reader := &ofsMockTool{name: "read_file", content: "package main"}
	if err := registry.Register(reader); err != nil {
		t.Fatal(err)
	}

	s.maybeSpeculateFromOutput(context.Background(), registry, "grep")

	// Wait (bounded) for the background goroutine to populate the cache.
	args, _ := json.Marshal(map[string]string{"path": target})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if s.hasCached("read_file", args) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected speculative read_file cache entry for grep output target")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reader.execN != 1 {
		t.Fatalf("expected exactly 1 speculative execution, got %d", reader.execN)
	}
}

func TestMaybeSpeculateFromOutput_GatesAndSkips(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.go")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Case 1: no bigram evidence → no speculation.
	s := newSpeculator()
	s.recordOutput("grep", target+":1:\tx\n", false)
	registry := tool.NewRegistry()
	reader := &ofsMockTool{name: "read_file"}
	_ = registry.Register(reader)
	s.maybeSpeculateFromOutput(context.Background(), registry, "grep")
	time.Sleep(50 * time.Millisecond)
	if reader.execN != 0 {
		t.Fatalf("no-evidence case must not speculate, got %d executions", reader.execN)
	}

	// Case 2: non-source last tool → no speculation even with evidence.
	s2 := newSpeculator()
	s2.recordObservation("grep")
	s2.recordObservation("read_file")
	s2.recordOutput("edit_file", target+":1:\tx\n", false)
	s2.maybeSpeculateFromOutput(context.Background(), registry, "edit_file")
	time.Sleep(50 * time.Millisecond)
	if reader.execN != 0 {
		t.Fatalf("non-source tool must not speculate, got %d executions", reader.execN)
	}

	// Case 3: empty stored output → no speculation.
	s3 := newSpeculator()
	s3.recordObservation("grep")
	s3.recordObservation("read_file")
	s3.maybeSpeculateFromOutput(context.Background(), registry, "grep")
	time.Sleep(50 * time.Millisecond)
	if reader.execN != 0 {
		t.Fatalf("empty output must not speculate, got %d executions", reader.execN)
	}

	// Case 4: missing path on disk is skipped without error.
	s4 := newSpeculator()
	s4.recordObservation("grep")
	s4.recordObservation("read_file")
	s4.recordOutput("grep", "/definitely/not/here.go:1:\tx\n", false)
	s4.maybeSpeculateFromOutput(context.Background(), registry, "grep")
	time.Sleep(50 * time.Millisecond)
	if reader.execN != 0 {
		t.Fatalf("missing path must be skipped, got %d executions", reader.execN)
	}
}

func TestMaybeSpeculateFromOutput_SizeCapAndCandidateLimit(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.go")
	if err := os.WriteFile(big, make([]byte, specOutputFlowMaxFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	smallA := filepath.Join(dir, "a.go")
	smallB := filepath.Join(dir, "b.go")
	for _, p := range []string{smallA, smallB} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s := newSpeculator()
	s.recordObservation("grep")
	s.recordObservation("read_file")
	// Big file ranked first (2 mentions) but must be skipped by the size cap;
	// the two small files fill the candidate limit instead.
	s.recordOutput("grep", big+":1:\tx\n"+big+":2:\ty\n"+smallA+":1:\tz\n"+smallB+":1:\tw\n", false)

	registry := tool.NewRegistry()
	reader := &ofsMockTool{name: "read_file"}
	_ = registry.Register(reader)

	s.maybeSpeculateFromOutput(context.Background(), registry, "grep")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		n := s.activeSpeculations
		s.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reader.execN != specOutputFlowMaxCandidates {
		t.Fatalf("expected %d speculative reads (big file skipped), got %d", specOutputFlowMaxCandidates, reader.execN)
	}
	argsBig, _ := json.Marshal(map[string]string{"path": big})
	if s.hasCached("read_file", argsBig) {
		t.Fatal("oversized file must not be speculated")
	}
}

func TestResetSequenceClearsOutputFlowEvidence(t *testing.T) {
	s := newSpeculator()
	s.recordOutput("grep", "/x/y.go:1:\tz\n", false)
	s.resetSequence()
	s.mu.Lock()
	got := s.lastOutput
	s.mu.Unlock()
	if got != "" {
		t.Fatalf("resetSequence must clear output-flow evidence, got %q", got)
	}
}
