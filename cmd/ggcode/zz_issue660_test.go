package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestMCPListOutputUsesLF covers #660: `mcp list` must not emit CRLF line
// endings — Unix pipelines get a stray CR on every line otherwise. Both the
// empty listing and the populated listing paths are checked.
func TestMCPListOutputUsesLF(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"empty", ""},
		{"populated", "mcp_servers:\n  demo:\n    type: stdio\n    command: echo\n    args: [\"hi\"]\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "ggcode.yaml")
			if err := os.WriteFile(cfgPath, []byte(tc.yaml), 0o600); err != nil {
				t.Fatalf("write cfg: %v", err)
			}
			var out bytes.Buffer
			cmd := &cobra.Command{Use: "root"}
			cmd.SetOut(&out)
			cfgFile := cfgPath
			listCmd := newMCPCmd(&cfgFile)
			list := findSub(listCmd, "list")
			if list == nil {
				t.Fatal("mcp list subcommand not found")
			}
			list.SetOut(&out)
			if err := list.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if strings.Contains(out.String(), "\r") {
				t.Fatalf("output contains CR (CRLF) — mcp list must use LF only (#660): %q", out.String())
			}
		})
	}
}

func findSub(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
