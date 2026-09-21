package agentruntime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// seedHotReloadBaselines mirrors what ConfigHotReload.Start does before its
// first poll, without launching the poll goroutine (tests call pollOnce
// directly, matching the existing config_hotreload_test.go style).
func seedHotReloadBaselines(w *ConfigHotReload) {
	for _, p := range w.watchedFiles() {
		w.baselines[p] = snapshotFile(p)
	}
}

func writeDriftConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// primeHotReload absorbs config.Load's normalize write-back (Load persists
// default keys like a2a/model/vendor into ggcode.yaml on first load) so that
// later in-place edits produce a clean key diff.
func primeHotReload(t *testing.T, w *ConfigHotReload) {
	t.Helper()
	w.pollOnce()
	seedHotReloadBaselines(w)
}

func TestConfigDriftNotice_ClassifiesAppliedAndRestartKeys(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeDriftConfig(t, cfgPath, "language: en\nmax_iterations: 20\n")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)
	seedHotReloadBaselines(w)
	primeHotReload(t, w)

	notices := make(chan ConfigDriftNotice, 4)
	w.SetDriftNotifier(func(n ConfigDriftNotice) { notices <- n })

	// Edit the normalized on-disk content in place so only the two target
	// keys change (Load may have added default keys we must preserve).
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	modified := regexp.MustCompile(`(?m)^(language:).*$`).ReplaceAllLiteralString(string(data), "language: zh")
	modified = regexp.MustCompile(`(?m)^(max_iterations:).*$`).ReplaceAllLiteralString(modified, "max_iterations: 50")
	writeDriftConfig(t, cfgPath, modified)
	w.pollOnce()

	select {
	case n := <-notices:
		if n.File != cfgPath {
			t.Fatalf("file = %q, want %q", n.File, cfgPath)
		}
		if got := strings.Join(n.ChangedKeys, ","); got != "language,max_iterations" {
			t.Fatalf("changed keys = %q", got)
		}
		if len(n.AppliedKeys) != 1 || n.AppliedKeys[0] != "max_iterations" {
			t.Fatalf("applied = %v", n.AppliedKeys)
		}
		if len(n.RestartKeys) != 1 || n.RestartKeys[0] != "language" {
			t.Fatalf("restart = %v", n.RestartKeys)
		}
		if access.cfg.MaxIterations != 50 {
			t.Fatalf("MaxIterations = %d, want 50 (live-applied)", access.cfg.MaxIterations)
		}
		msg := n.Message()
		if !strings.Contains(msg, "max_iterations") || !strings.Contains(msg, "language") || !strings.Contains(msg, "restart") {
			t.Fatalf("message missing key info: %q", msg)
		}
	default:
		t.Fatal("expected a drift notice, got none")
	}
}

func TestConfigDriftNotice_InvalidYAMLNoticed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeDriftConfig(t, cfgPath, "language: en\n")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)
	seedHotReloadBaselines(w)
	primeHotReload(t, w)

	notices := make(chan ConfigDriftNotice, 4)
	w.SetDriftNotifier(func(n ConfigDriftNotice) { notices <- n })

	// Break the file mid-edit (editors do this transiently).
	writeDriftConfig(t, cfgPath, "vendor: [broken")
	w.pollOnce()

	select {
	case n := <-notices:
		if n.InvalidYAML == "" {
			t.Fatal("expected InvalidYAML to be set")
		}
		if n.File != cfgPath {
			t.Fatalf("file = %q, want %q", n.File, cfgPath)
		}
		if len(n.ChangedKeys) != 0 {
			t.Fatalf("changed keys = %v, want none for a skipped reload", n.ChangedKeys)
		}
	default:
		t.Fatal("expected an invalid-YAML notice, got none")
	}
}

func TestConfigDriftNotice_CommentOnlyEditSilent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeDriftConfig(t, cfgPath, "language: en\n")

	access := newTestAccess(t, cfgPath)
	w := NewConfigHotReload(cfgPath, access)
	seedHotReloadBaselines(w)
	primeHotReload(t, w)

	notices := make(chan ConfigDriftNotice, 4)
	w.SetDriftNotifier(func(n ConfigDriftNotice) { notices <- n })

	// Hash changes, parsed top-level keys do not: no user-facing drift.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	writeDriftConfig(t, cfgPath, "# a comment\n"+string(data))
	w.pollOnce()

	if len(notices) != 0 {
		n := <-notices
		t.Fatalf("expected no notice for comment-only edit, got %+v", n)
	}
}

func TestConfigDriftNotice_VendorsFileFullyApplied(t *testing.T) {
	applied, restart := classifyDriftKeys(filepath.Join("some", "dir", "vendors.yaml"), []string{"anthropic", "openai"})
	if len(applied) != 2 || applied[0] != "anthropic" || applied[1] != "openai" {
		t.Fatalf("applied = %v", applied)
	}
	if len(restart) != 0 {
		t.Fatalf("restart = %v, want empty for vendors.yaml", restart)
	}
}

func TestConfigDriftNotice_MessageInvalidYAML(t *testing.T) {
	n := ConfigDriftNotice{File: "x.yaml", InvalidYAML: "did not find expected key"}
	msg := n.Message()
	if !strings.Contains(msg, "NOT applied") || !strings.Contains(msg, "x.yaml") {
		t.Fatalf("unexpected message: %q", msg)
	}
}
