package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// --- Fix 1 (#981, problem 1): migration must hold the config lock and use
// read-merge-delete-write semantics so a concurrent writer's field changes
// are not silently dropped by a stale full-file rewrite. ---

// TestMigrateSectionToExternalHoldsLock verifies that migrateSectionToExternal
// acquires lockConfigFile(mainConfigPath) before reading the file. While the
// test holds the lock, the migration goroutine must not complete (it blocks on
// the same in-process mutex).
func TestMigrateSectionToExternalHoldsLock(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "config.yaml")
	writeTestFile(t, mainPath, "im:\n  output_mode: quiet\nmodel: glm-4.6\n")

	unlock := lockConfigFile(mainPath)

	done := make(chan struct{})
	go func() {
		migrateSectionToExternal(mainPath, dir, "im")
		close(done)
	}()

	select {
	case <-done:
		unlock()
		t.Fatal("migrateSectionToExternal completed while the config lock was held; it does not acquire lockConfigFile")
	case <-time.After(300 * time.Millisecond):
		// Blocked as expected.
	}

	unlock()

	select {
	case <-done:
		// Completed after the lock was released.
	case <-time.After(5 * time.Second):
		t.Fatal("migrateSectionToExternal did not complete after the lock was released")
	}
}

// TestMigrateSectionToExternalFreshReadPreservesConcurrentWrites verifies the
// read-merge-delete-write behavior: modifications written to the main file
// while the migration waits on the lock must survive the migration's rewrite
// of the main file. With the old behavior (read the raw map first, then write
// twice without locking), a concurrent addition was lost because the later
// rewrite used the stale snapshot taken before the lock window.
func TestMigrateSectionToExternalFreshReadPreservesConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "config.yaml")
	writeTestFile(t, mainPath, "im:\n  output_mode: quiet\nmodel: glm-4.6\n")

	unlock := lockConfigFile(mainPath)

	// Concurrent writer adds a field while the migration is blocked on the lock.
	concurrentData := "im:\n  output_mode: quiet\nmodel: glm-4.6\nvendor: zai\n"
	if err := os.WriteFile(mainPath, []byte(concurrentData), 0o600); err != nil {
		unlock()
		t.Fatalf("writing concurrent update: %v", err)
	}

	done := make(chan struct{})
	go func() {
		migrateSectionToExternal(mainPath, dir, "im")
		close(done)
	}()

	select {
	case <-done:
		unlock()
		t.Fatal("migration ran without waiting for the lock")
	case <-time.After(300 * time.Millisecond):
	}

	unlock()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not complete after lock release")
	}

	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("reading main config: %v", err)
	}
	assertYAMLHasKey(t, data, "model", "pre-existing field must survive migration cleanup")
	assertYAMLHasKey(t, data, "vendor", "concurrently added field must survive migration rewrite (read-merge-delete-write)")
	assertYAMLNotHasKey(t, data, "im", "migrated section must be removed from main config")

	imData, err := os.ReadFile(filepath.Join(dir, "im.yaml"))
	if err != nil {
		t.Fatalf("im.yaml was not created: %v", err)
	}
	if len(imData) == 0 {
		t.Fatal("im.yaml is empty")
	}
}

// TestMigrateSectionToExternalPreservesOtherFields is the basic non-concurrent
// regression check: migration removes only its own section key.
func TestMigrateSectionToExternalPreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "config.yaml")
	writeTestFile(t, mainPath, "model: glm-4.6\noutput_style: detailed\nim:\n  output_mode: quiet\n")

	migrateSectionToExternal(mainPath, dir, "im")

	data, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("reading main config: %v", err)
	}
	assertYAMLHasKey(t, data, "model", "")
	assertYAMLHasKey(t, data, "output_style", "")
	assertYAMLNotHasKey(t, data, "im", "")
}

// --- Fix 2 (#981, problem 2): an empty / comment-only im.yaml must not wipe
// an IM section that is still present in the main config (pre-migration
// layouts); external IM values merge field-level, like mergeVendors (#559). ---

// TestLoadExternalSectionsEmptyIMFilePreservesIM verifies the reported scenario:
// the main config still has an im section (old layout) and the user manually
// touched an empty im.yaml. Previously cfg.IM = *im replaced the populated
// IMConfig with the zero value parsed from the empty file, silently disabling
// IM with no log.
func TestLoadExternalSectionsEmptyIMFilePreservesIM(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "config.yaml")
	writeTestFile(t, mainPath, "model: glm-4.6\nim:\n  enabled: true\n  output_mode: quiet\n")

	cfg := &Config{}
	cfg.Model = "glm-4.6"
	cfg.IM = IMConfig{
		Enabled:    true,
		OutputMode: "quiet",
		Adapters: map[string]IMAdapterConfig{
			"telegram": {Enabled: true, Platform: "telegram", Command: "/usr/bin/td"},
		},
	}

	// Empty file (user ran `touch im.yaml`).
	writeTestFile(t, filepath.Join(dir, "im.yaml"), "")

	loadExternalSections(cfg, mainPath)

	if !cfg.IM.Enabled {
		t.Error("IM.Enabled was wiped by an empty im.yaml")
	}
	if cfg.IM.OutputMode != "quiet" {
		t.Errorf("IM.OutputMode = %q, want %q", cfg.IM.OutputMode, "quiet")
	}
	if a, ok := cfg.IM.Adapters["telegram"]; !ok || a.Command != "/usr/bin/td" {
		t.Errorf("IM.Adapters[telegram] lost: %+v (ok=%v)", a, ok)
	}

	// Comment-only variant (parses to zero-value too).
	writeTestFile(t, filepath.Join(dir, "im.yaml"), "# intentionally empty\n")
	cfg2 := &Config{}
	cfg2.IM = cfg.IM
	loadExternalSections(cfg2, mainPath)
	if !cfg2.IM.Enabled {
		t.Error("IM.Enabled was wiped by a comment-only im.yaml")
	}
}

// TestMergeIMConfigFieldPrecedence locks in the field-level precedence rules:
// non-zero external fields win, zero-value external fields keep the base value.
func TestMergeIMConfigFieldPrecedence(t *testing.T) {
	requireLocal := true
	base := IMConfig{
		Enabled:             true,
		ActiveSessionPolicy: "reject",
		RequireLocalSession: &requireLocal,
		OutputMode:          "verbose",
		Streaming:           IMStreamingConfig{Enabled: true, Transport: "sse", EditIntervalSec: 2},
		STT:                 IMSTTConfig{Provider: "openai", Model: "whisper"},
		Adapters: map[string]IMAdapterConfig{
			"telegram": {Enabled: true, Platform: "telegram", Command: "/base/cmd", Args: []string{"--base"}, AllowFrom: []string{"u1"}},
		},
	}

	ext := IMConfig{
		OutputMode: "quiet",                              // override wins
		Streaming:  IMStreamingConfig{Transport: "http"}, // one field only
		STT:        IMSTTConfig{Model: "gemini"},         // one field only
		Adapters: map[string]IMAdapterConfig{
			"telegram": {Command: "/ext/cmd"},                // partial override
			"discord":  {Enabled: true, Platform: "discord"}, // new adapter
		},
	}

	got := mergeIMExternal(base, ext)

	if !got.Enabled {
		t.Error("zero-value ext.Enabled must keep base.Enabled=true")
	}
	if got.ActiveSessionPolicy != "reject" {
		t.Errorf("ActiveSessionPolicy = %q, want kept %q", got.ActiveSessionPolicy, "reject")
	}
	if got.RequireLocalSession == nil || !*got.RequireLocalSession {
		t.Error("RequireLocalSession pointer must be kept")
	}
	if got.OutputMode != "quiet" {
		t.Errorf("OutputMode = %q, want ext %q", got.OutputMode, "quiet")
	}
	if !got.Streaming.Enabled || got.Streaming.EditIntervalSec != 2 {
		t.Error("streaming base fields must be kept")
	}
	if got.Streaming.Transport != "http" {
		t.Errorf("Streaming.Transport = %q, want ext %q", got.Streaming.Transport, "http")
	}
	if got.STT.Provider != "openai" || got.STT.Model != "gemini" {
		t.Errorf("STT merge wrong: %+v", got.STT)
	}
	tg := got.Adapters["telegram"]
	if !tg.Enabled || tg.Platform != "telegram" {
		t.Errorf("telegram adapter base fields lost: %+v", tg)
	}
	if tg.Command != "/ext/cmd" {
		t.Errorf("telegram Command = %q, want ext %q", tg.Command, "/ext/cmd")
	}
	if len(tg.Args) != 1 || tg.Args[0] != "--base" {
		t.Errorf("telegram Args lost: %v", tg.Args)
	}
	dc := got.Adapters["discord"]
	if !dc.Enabled || dc.Platform != "discord" {
		t.Errorf("new discord adapter missing: %+v", dc)
	}
}

// TestMergeIMConfigZeroExternalKeepsBase verifies that a fully zero external
// value (what an empty im.yaml parses to) leaves the base untouched.
func TestMergeIMConfigZeroExternalKeepsBase(t *testing.T) {
	base := IMConfig{
		Enabled:    true,
		OutputMode: "summary",
		Adapters:   map[string]IMAdapterConfig{"slack": {Enabled: true}},
	}
	got := mergeIMExternal(base, IMConfig{})
	if !reflect.DeepEqual(got, base) {
		t.Errorf("zero external changed base: %+v", got)
	}
}

// --- helpers ---

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func assertYAMLHasKey(t *testing.T, data []byte, key, msg string) {
	t.Helper()
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parsing yaml: %v", err)
	}
	if _, ok := raw[key]; !ok {
		if msg == "" {
			t.Errorf("key %q missing from yaml", key)
		} else {
			t.Errorf("key %q missing from yaml: %s", key, msg)
		}
	}
}

func assertYAMLNotHasKey(t *testing.T, data []byte, key, msg string) {
	t.Helper()
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parsing yaml: %v", err)
	}
	if _, ok := raw[key]; ok {
		if msg == "" {
			t.Errorf("key %q unexpectedly present in yaml", key)
		} else {
			t.Errorf("key %q unexpectedly present in yaml: %s", key, msg)
		}
	}
}
