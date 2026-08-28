package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Panic-guard regression test: offset/head_limit arrive as model-generated
// JSON. A negative offset used to reach lines[start:end] with only an upper
// clamp and panicked the CLI process ("slice bounds out of range"). The
// Execute entry point now normalizes both to 0, so negative values must
// behave exactly like their zero values in every output mode.
func TestGrepNegativeOffsetNoPanic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := Grep{}

	for _, mode := range []string{"content", "count", "files_with_matches"} {
		neg := fmt.Sprintf(`{"pattern":"a","path":%q,"output_mode":%q,"offset":-7,"head_limit":-3}`, dir, mode)
		res, err := g.Execute(context.Background(), json.RawMessage(neg))
		if err != nil {
			t.Fatalf("mode %s: unexpected error: %v", mode, err)
		}
		if res.IsError {
			t.Fatalf("mode %s: unexpected tool error: %s", mode, res.Content)
		}

		zero := fmt.Sprintf(`{"pattern":"a","path":%q,"output_mode":%q,"offset":0,"head_limit":0}`, dir, mode)
		want, err := g.Execute(context.Background(), json.RawMessage(zero))
		if err != nil {
			t.Fatalf("mode %s: baseline error: %v", mode, err)
		}
		if res.Content != want.Content {
			t.Fatalf("mode %s: negative offset/head_limit must behave like 0:\nneg:  %q\nzero: %q",
				mode, res.Content, want.Content)
		}
	}
}
