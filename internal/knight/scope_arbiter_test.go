package knight

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeDowngradeReason(t *testing.T) {
	projDir := filepath.Join("/tmp", "ggcode")

	cases := []struct {
		name     string
		body     string
		wantHit  bool
		wantSubs string
	}{
		{
			name: "generic-go-rule-stays-global",
			body: `# Always run go vet

When changing exported APIs, run ` + "`go vet ./...`" + ` before pushing.`,
			wantHit: false,
		},
		{
			name:     "project-basename-flagged",
			body:     "When extending the ggcode CLI, register tools in builtin.go.",
			wantHit:  true,
			wantSubs: "basename",
		},
		{
			name:     "internal-path-flagged",
			body:     "Edit internal/knight/scheduler.go to register the new policy.",
			wantHit:  true,
			wantSubs: "internal/",
		},
		{
			name:     "make-target-flagged",
			body:     "Run `make verify-ci` before declaring success.",
			wantHit:  true,
			wantSubs: "make",
		},
		{
			name:     "absolute-project-path-flagged",
			body:     "See /tmp/ggcode/cmd/main.go for the entry point.",
			wantHit:  true,
			wantSubs: "absolute",
		},
		{
			name:     "local-script-flagged",
			body:     "Execute `./scripts/dev/verify.sh` to run the suite.",
			wantHit:  true,
			wantSubs: "script",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := scopeDowngradeReason(projDir, tc.body)
			if tc.wantHit && reason == "" {
				t.Fatalf("expected downgrade reason, got empty")
			}
			if !tc.wantHit && reason != "" {
				t.Fatalf("expected no downgrade, got %q", reason)
			}
			if tc.wantSubs != "" && !strings.Contains(reason, tc.wantSubs) {
				t.Fatalf("reason %q missing substring %q", reason, tc.wantSubs)
			}
		})
	}
}

func TestScopeDowngradeStripsFrontmatter(t *testing.T) {
	projDir := filepath.Join("/tmp", "ggcode")
	content := `---
name: ggcode-style
description: a generic style rule
scope: global
---

# Generic rule

Always prefer composition over inheritance.`
	// frontmatter mentions "ggcode" but body does not — should NOT downgrade.
	if reason := scopeDowngradeReason(projDir, content); reason != "" {
		t.Fatalf("frontmatter-only mention should not downgrade, got %q", reason)
	}
}

func TestScopeDowngradeIgnoresGenericBasename(t *testing.T) {
	// "src" is too generic to be a project signal.
	projDir := filepath.Join("/tmp", "src")
	body := "Always wrap errors with fmt.Errorf for src maintainability."
	if reason := scopeDowngradeReason(projDir, body); reason != "" {
		t.Fatalf("generic basename 'src' should not downgrade, got %q", reason)
	}
}
