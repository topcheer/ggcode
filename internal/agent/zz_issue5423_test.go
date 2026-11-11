package agent

import (
	"encoding/json"
	"testing"
)

// TestIrrevVersionOrHelpOnly pins #5423(a): repo-local or PATH-oddity
// binaries invoked with purely introspective flags are read-only, so
// smoke checks like `bin/ggcode --version` stop tripping the
// medium-impact/zero-grounding gate every round.
func TestIrrevVersionOrHelpOnly(t *testing.T) {
	readOnly := []string{
		`bin/ggcode --version`,
		`./bin/ggcode --version -q`,
		`/usr/local/bin/sometool -V`,
		`some/tool --help --color=never`,
		`weird-binary --usage --no-pager --quiet`,
	}
	for _, cmd := range readOnly {
		if !irrevIsReadOnlyCommand(mustMarshalCmd(t, cmd)) {
			t.Errorf("expected read-only: %q", cmd)
		}
	}

	// Anything beyond introspection flags disqualifies: unknown binaries
	// with real arguments must stay gated (medium).
	gated := []string{
		`bin/ggcode agent start`,
		`some/tool --config /etc/passwd`,
		`./deploy --version --force`, // mixed: force flag present
	}
	for _, cmd := range gated {
		if irrevIsReadOnlyCommand(mustMarshalCmd(t, cmd)) {
			t.Errorf("expected gated (not read-only): %q", cmd)
		}
	}

	// Unit surface: bare flag list classification.
	if !irrevIsVersionOrHelpOnly([]string{"--version"}) {
		t.Error("--version only should be true")
	}
	if irrevIsVersionOrHelpOnly(nil) || irrevIsVersionOrHelpOnly([]string{}) {
		t.Error("no flags must be false (falls through to binary table)")
	}
	if irrevIsVersionOrHelpOnly([]string{"--version", "/etc/hosts"}) {
		t.Error("trailing path argument must disqualify")
	}
}

// TestIrrevSmokeChainReadOnly: the exact post-restart smoke shape that
// motivated #5423 - cd + covered gh call + version-flag binary in one
// && chain - must classify read-only end to end.
func TestIrrevSmokeChainReadOnly(t *testing.T) {
	cmd := `cd /Volumes/new/ggai/ggcode && gh pr list --repo topcheer/ggcode --state open --json number --jq 'length' && bin/ggcode --version 2>/dev/null | head -1`
	if !irrevIsReadOnlyCommand(mustMarshalCmd(t, cmd)) {
		t.Fatal("post-restart smoke chain must classify read-only (#5423 live evidence)")
	}
}

func mustMarshalCmd(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
