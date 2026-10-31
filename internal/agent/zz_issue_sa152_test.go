package agent

// sa-152 probes: trajectory→training-data export (RLVR/SFT outlet).
// P1 reward bounds, P2 secret masking, P3 JSONL round-trip + rolling cap,
// P4 opt-in gate + substance gate, P5 message redaction shape.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// fakeKey builds a syntactically valid secret-shaped string from halves so
// the pattern detector has a real target to mask, without ever writing a
// literal secret-shaped token into the source file.
func fakeKey(prefix, body string) string { return prefix + body }

// redactedMarker builds the expected mask token in two pieces so this test
// source never contains a literal redaction placeholder.
func redactedMarker(name string) string {
	return "[" + strings.ToUpper("redacted") + ":" + name + "]"
}

func sa152Stats(success bool, tools map[string]int, errCount, compact int) *RunStats {
	s := newRunStats("sa-152 probe task")
	s.Success = success
	s.ToolCalls = tools
	s.ErrorCount = errCount
	s.CompactionCount = compact
	s.Iterations = 4
	return s
}

// P1a: a clean diverse run scores high; P1b: an error-heavy failed run
// scores below the success-only baseline.
func TestSA152_RewardBounds(t *testing.T) {
	good := sa152Stats(true, map[string]int{"read_file": 2, "edit_file": 1, "run_command": 1}, 0, 0)
	rg := computeTrainReward(good)
	if rg < 0.85 || rg > 1.0 {
		t.Fatalf("clean run reward out of [0.85,1.0]: %.3f", rg)
	}
	bad := sa152Stats(false, map[string]int{"grep": 9}, 9, 5)
	rb := computeTrainReward(bad)
	if rb >= 0.3 {
		t.Fatalf("error-heavy failed run reward too high: %.3f", rb)
	}
	if computeTrainReward(nil) != 0 {
		t.Fatal("nil stats must yield reward 0")
	}
}

// P2: secret values in any text field are masked.
func TestSA152_RedactTrainText(t *testing.T) {
	anthKey := fakeKey("sk-ant-", "abc123def456ghi789jkl012")
	ghKey := fakeKey("ghp_", strings.Repeat("A", 40))
	in := "config: api_key = \"" + anthKey + "\" and token " + ghKey
	out := redactTrainText(in)
	if out == in {
		t.Fatal("expected masking, got unchanged text")
	}
	if strings.Contains(out, anthKey) || strings.Contains(out, ghKey) {
		t.Fatalf("secret leaked: %s", out)
	}
	if !strings.Contains(out, redactedMarker("openai_or_anthropic_key")) {
		t.Fatalf("expected anthropic-family mask marker: %s", out)
	}
}

// P3: JSONL append round-trip and the 200-entry rolling cap.
func TestSA152_JSONLRoundTripAndCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "training-samples.jsonl")
	sample := TrainSample{
		SchemaVer: trainSampleSchemaVer,
		RunID:     "run-1",
		Task:      "fix the bug",
		Messages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "please fix"}}},
			{Role: "assistant", Content: []provider.ContentBlock{{Type: "tool_use", ToolName: "edit_file", Input: json.RawMessage(`{"file_path":"a.go"}`)}}},
		},
		Reward:     0.9,
		Outcome:    "success",
		ExportedAt: time.Now().UTC(),
	}
	for i := 0; i < trainExportMaxEntries+7; i++ {
		if err := appendTrainingSample(path, sample); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	var last TrainSample
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines++
		if err := json.Unmarshal([]byte(l), &last); err != nil {
			t.Fatalf("bad JSONL line: %v", err)
		}
	}
	if lines != trainExportMaxEntries {
		t.Fatalf("expected cap %d, got %d", trainExportMaxEntries, lines)
	}
	if last.RunID != "run-1" || last.Outcome != "success" || last.Reward != 0.9 {
		t.Fatalf("round-trip mismatch: %+v", last)
	}
	if len(last.Messages) != 2 || last.Messages[1].Content[0].ToolName != "edit_file" {
		t.Fatalf("messages lost in round-trip: %+v", last.Messages)
	}
}

// P4: the opt-in gate and the substance gate.
func TestSA152_Gates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(trainExportEnv, "0")
	a := &Agent{}
	stats := sa152Stats(true, map[string]int{"grep": 4}, 0, 0)
	a.maybeExportTrainingSample(dir, stats)
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", trainExportFile)); err == nil {
		t.Fatal("export must be OFF without explicit opt-in")
	}

	t.Setenv(trainExportEnv, "1")
	// Substance gate: 2 tool calls < 3 minimum.
	thin := sa152Stats(true, map[string]int{"grep": 2}, 0, 0)
	a.maybeExportTrainingSample(dir, thin)
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", trainExportFile)); err == nil {
		t.Fatal("trivial run must not be exported")
	}
	// No contextManager: silent no-op.
	a.maybeExportTrainingSample(dir, stats)
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", trainExportFile)); err == nil {
		t.Fatal("nil contextManager must not export")
	}
}

// P5: image blocks dropped, secrets masked, message count preserved.
func TestSA152_RedactTrainMessages(t *testing.T) {
	msgs := make([]provider.Message, trainExportMaxMessages+10)
	for i := range msgs {
		msgs[i] = provider.Message{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: "keep this text"},
		}}
	}
	secret := provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "text", Text: "key " + fakeKey("sk-proj-", "abcdefghijklmnopqrstuvwx") + " is here"},
		{Type: "image", ImageMIME: "image/png", ImageData: "base64junk"},
	}}
	msgs = append(msgs, secret)

	out := redactTrainMessages(msgs)
	if len(out) != trainExportMaxMessages+11 {
		t.Fatalf("message count mismatch: %d", len(out))
	}
	last := out[len(out)-1]
	hasImage := false
	for _, b := range last.Content {
		if b.Type == "image" {
			hasImage = true
		}
		if b.Type == "text" && strings.Contains(b.Text, "sk-proj-") {
			t.Fatal("secret survived training redaction")
		}
	}
	if hasImage {
		t.Fatal("image blocks must be dropped from training samples")
	}
}
