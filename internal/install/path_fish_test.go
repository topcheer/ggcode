package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// Regression for #1648-1: fish targets get fish syntax (fish_add_path),
// not POSIX export fish cannot parse.
func TestUpsertPathBlockFishSyntax(t *testing.T) {
	out, err := upsertPathBlockFor("/home/u/.config/fish/config.fish", "set -x EDITOR vim\n", "/usr/local/bin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fish_add_path") {
		t.Fatalf("config.fish target must use fish_add_path, got: %q", out)
	}
	if strings.Contains(out, "export PATH=") {
		t.Fatalf("POSIX export must not land in config.fish, got: %q", out)
	}
	posix, err := upsertPathBlockFor(filepath.Join("home", "u", ".zshrc"), "", "/usr/local/bin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(posix, "export PATH=") {
		t.Fatalf("POSIX rc must keep export syntax, got: %q", posix)
	}
}
