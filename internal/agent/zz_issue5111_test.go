package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Issue #5111: TrainSample.Task (the user prompt) bypassed secret redaction.
// Both entry points (run-stats auto export and ExportSessionTrainingSample)
// redact Messages via redactTrainMessages but wrote Task unredacted, so a
// secret pasted in the user's first prompt landed in plaintext in the
// top-level `task` field of .ggcode/training-samples.jsonl while the same
// secret inside messages[0] showed as [REDACTED:...]. This pin freezes the
// fix: the task field must pass through redactTrainText like every other
// text carrier.
func TestIssue5111TaskFieldRedacted(t *testing.T) {
	dir := t.TempDir()
	key := "sk-abcdefghijklmnopqrstuvwxyz123456"
	task := "我的 OpenAI key 是 " + key + "，请写进 .env 并测试"
	msgs := []provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: task}},
	}}

	if _, err := ExportSessionTrainingSample(dir, task, msgs); err != nil {
		t.Fatalf("manual export failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ".ggcode", "training-samples.jsonl"))
	if err != nil {
		t.Fatalf("read training samples: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatal("no sample line written")
	}

	var sample TrainSample
	if err := json.Unmarshal([]byte(line), &sample); err != nil {
		t.Fatalf("unmarshal sample: %v", err)
	}

	if strings.Contains(sample.Task, key) {
		t.Errorf("task field must not contain the raw secret: %q", sample.Task)
	}
	if !strings.Contains(sample.Task, "[REDACTED:") {
		t.Errorf("task field must be redacted like message blocks: %q", sample.Task)
	}
	// messages[0] must stay redacted too (pre-existing pipeline, pin it
	// so a future "simplification" cannot un-redact either carrier).
	if strings.Contains(jsonMarshalText(t, sample.Messages), key) {
		t.Errorf("messages must not contain the raw secret")
	}
}

func jsonMarshalText(t *testing.T, msgs []provider.Message) string {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return string(b)
}
