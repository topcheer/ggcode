package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// #1827 case 1: the marker-exists branch of upsertBlockWith used
// ReplaceAllString, whose replacement argument is a TEMPLATE - $PATH in the
// POSIX block was parsed as a capture-group reference, and since
// pathBlockPattern has no groups it expanded to an empty string. A second
// installer run (version upgrade - the normal case) rewrote the managed
// block to `export PATH="/usr/local/bin":` and truncated the user's shell
// PATH to a single directory, reproduced independently on two machines.
// The replacement is a literal block; upsert must also be idempotent
// (second call produces the same content as the first).

func TestUpsertPathBlockSecondRunPreservesPath(t *testing.T) {
	rc := filepath.Join("home", "u", ".zshrc")
	first, err := upsertPathBlockFor(rc, "set -x EDITOR vim\n", "/usr/local/bin")
	if err != nil {
		t.Fatalf("first upsert error = %v", err)
	}
	if !strings.Contains(first, "$PATH") {
		t.Fatalf("first run must keep $PATH in the block, got:\n%s", first)
	}
	// Second run takes the marker-exists replacement branch - the exact
	// path that destroyed $PATH before the fix.
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
	// A re-run with a DIFFERENT dir must swap the dir but still keep $PATH.
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

// The fish block carries no $-syntax, but pin the same idempotency contract
// for the fish branch so a future template regression is caught either way.
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
