package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue1287_GLM4ExactOnly(t *testing.T) {
	if got := LookupKnownModelContextWindow("glm-4"); got != 128_000 {
		t.Fatalf("bare glm-4 must keep its own window, got %d", got)
	}
	for _, victim := range []string{"glm-4.5", "glm-4.5-air", "glm-4.6", "glm-4.7", "glm-5"} {
		if got := LookupKnownModelContextWindow(victim); got != 0 {
			t.Fatalf("#1287: %s hijacked into legacy glm-4 window %d - must fall through to probe (0)", victim, got)
		}
	}

	if got := LookupKnownModelContextWindow("glm-4-plus"); got != 128_000 {
		t.Fatalf("glm-4-plus lost its entry: %d", got)
	}
	if got := LookupKnownModelContextWindow("glm-4-long"); got != 1_000_000 {
		t.Fatalf("glm-4-long lost its entry: %d", got)
	}
}

func TestIssue1287_QwenCoderProbed(t *testing.T) {

	if got := LookupKnownModelContextWindow("qwen3-coder-plus"); got != 0 {
		t.Fatalf("#1287: qwen3-coder-plus adopted %d - must be probed, not pinned to the 131K family entry", got)
	}

	if got := LookupKnownModelContextWindow("qwen3-235b-a22b"); got != 131_072 {
		t.Fatalf("qwen3-235b-a22b must keep the family window, got %d", got)
	}
}

func TestIssue1287_DeepSeekExactOnly(t *testing.T) {
	for _, bare := range []string{"deepseek-chat", "deepseek-v3"} {
		if got := LookupKnownModelContextWindow(bare); got != 64_000 {
			t.Fatalf("bare %s must keep its window, got %d", bare, got)
		}
	}
	for _, victim := range []string{"deepseek-chat-v3.2", "deepseek-v3.2"} {
		if got := LookupKnownModelContextWindow(victim); got != 0 {
			t.Fatalf("#1287: %s hijacked into the 64K legacy window %d - must fall through to probe", victim, got)
		}
	}

	if got := LookupKnownModelContextWindow("deepseek-r1-0528"); got != 64_000 {
		t.Fatalf("deepseek-r1-0528 lost its family window: %d", got)
	}
}

func TestIssue1287_GPT4ExactMigration(t *testing.T) {

	if got := LookupKnownModelContextWindow("gpt-4"); got != 8_192 {
		t.Fatalf("bare gpt-4 exact window changed: %d", got)
	}
	if got := LookupKnownModelContextWindow("gpt-4o-2024-08-06"); got != 128_000 {
		t.Fatalf("gpt-4o dated variant must keep prefix match: %d", got)
	}
}

func TestIssue1287_ProbeCachePoisonPurged(t *testing.T) {

	for _, poisoned := range []string{"qwen3-coder-plus", "deepseek-chat-v3.2", "glm-4.6", "gpt-4-32k"} {
		if !lookupKnownRetiredFamilyHijack(poisoned) {
			t.Fatalf("%s must be treated as a retired-family hijack victim", poisoned)
		}
	}
	for _, keep := range []string{"qwen3-235b-a22b", "glm-4-plus", "gpt-4o", "deepseek-r1", "claude-sonnet-4", "mystery-model"} {
		if lookupKnownRetiredFamilyHijack(keep) {
			t.Fatalf("%s must NOT be dropped (table-covered or unrelated)", keep)
		}
	}

	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	_ = dir
	v2JSON := `{
  "version": 2,
  "entries": {
    "zhipu|https://api.z.ai|glm-4.6": 128000,
    "zhipu|https://api.z.ai|glm-4-plus": 128000,
    "alibaba|https://x|qwen3-coder-plus": 131072,
    "alibaba|https://x|qwen3-235b-a22b": 131072,
    "deepseek|https://x|deepseek-chat-v3.2": 64000,
    "openai|https://x|gpt-4o": 128000
  }
}`
	path := filepath.Join(config.ConfigDir(), "state", "context_windows.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(v2JSON), 0o644); err != nil {
		t.Fatal(err)
	}
	probeCacheMu.Lock()
	probeCache = nil
	probeLoaded = false
	probeCacheMu.Unlock()

	if got := LookupProbeCache("zhipu|https://api.z.ai|glm-4.6"); got != 0 {
		t.Fatalf("poisoned glm-4.6 entry survived v3 migration: %d", got)
	}
	if got := LookupProbeCache("alibaba|https://x|qwen3-coder-plus"); got != 0 {
		t.Fatalf("poisoned qwen3-coder-plus entry survived: %d", got)
	}
	if got := LookupProbeCache("deepseek|https://x|deepseek-chat-v3.2"); got != 0 {
		t.Fatalf("poisoned deepseek-chat-v3.2 entry survived: %d", got)
	}
	for _, keepKey := range []string{
		"zhipu|https://api.z.ai|glm-4-plus",
		"alibaba|https://x|qwen3-235b-a22b",
		"openai|https://x|gpt-4o",
	} {
		if got := LookupProbeCache(keepKey); got == 0 {
			t.Fatalf("legitimate entry %s was wrongly dropped by migration", keepKey)
		}
	}
}
