package install

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertPathBlockSecondRunPreservesPath(t *testing.T) {
	rc := filepath.Join("home", "u", ".zshrc")
	first, err := upsertPathBlockFor(rc, "set -x EDITOR vim\n", "/usr/local/bin")
	if err != nil {
		t.Fatalf("first upsert error = %v", err)
	}
	if !strings.Contains(first, "$PATH") {
		t.Fatalf("first run must keep $PATH in the block, got:\n%s", first)
	}

	second, err := upsertPathBlockFor(rc, first, "/usr/local/bin")
	if err != nil {
		t.Fatalf("second upsert error = %v", err)
	}
	if !strings.Contains(second, "$PATH") {
		t.Fatalf("second run destroyed $PATH (PATH truncation bug #1827):\n%s", second)
	}
	if second != first {
		t.Fatalf("upsert not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}

	third, err := upsertPathBlockFor(rc, first, "/opt/ggcode/bin")
	if err != nil {
		t.Fatalf("third upsert error = %v", err)
	}
	if !strings.Contains(third, "/opt/ggcode/bin") || !strings.Contains(third, "$PATH") {
		t.Fatalf("dir change must update block and keep $PATH, got:\n%s", third)
	}
	if strings.Count(third, pathMarkerStart) != 1 {
		t.Fatalf("dir change must not duplicate the managed block, got:\n%s", third)
	}
}

func TestUpsertFishBlockSecondRunIdempotent(t *testing.T) {
	fish := filepath.Join("home", "u", ".config", "fish", "config.fish")
	first, err := upsertPathBlockFor(fish, "set -x EDITOR vim\n", "/usr/local/bin")
	if err != nil {
		t.Fatalf("first upsert error = %v", err)
	}
	second, err := upsertPathBlockFor(fish, first, "/usr/local/bin")
	if err != nil {
		t.Fatalf("second upsert error = %v", err)
	}
	if second != first {
		t.Fatalf("fish upsert not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
