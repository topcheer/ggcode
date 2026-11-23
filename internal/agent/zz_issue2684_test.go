package agent

import (
	"testing"
)

func TestExtractNewCommitHash(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			"standard-commit-line",
			"[main 5c2b1a3] fix A\n 3 files changed, 10 insertions(+)",
			"5c2b1a3",
		},
		{
			"long-abbrev",
			"[feat/x 9f8e7d6a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e] work\n 1 file changed",
			"9f8e7d6a9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e",
		},

		{
			"detached-head",
			"[detached HEAD 466523b] detach\n 1 file changed, 1 insertion(+)",
			"466523b",
		},
		{
			"root-commit",
			"[master (root-commit) d329121] root\n 2 files changed, 5 insertions(+)",
			"d329121",
		},
		{
			"prefixed-by-advisory-output",
			"Committed successfully.\n\n[main 1a2b3c4] msg\n 2 files changed",
			"1a2b3c4",
		},
		{"nothing-to-commit", "On branch main\nnothing to commit, working tree clean", ""},
		{"bare-success", "Committed successfully.", ""},
		{"empty", "", ""},
		{"non-git-vcs", "Committed successfully. (jj)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractNewCommitHash(c.content)
			if got != c.want {
				t.Fatalf("extractNewCommitHash(%q) = %q, want %q", c.content, got, c.want)
			}
		})
	}
}
