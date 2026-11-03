package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
