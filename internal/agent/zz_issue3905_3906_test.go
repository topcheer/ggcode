package agent

// Companion tests for #3905 + #3906.

import (
	"strings"
	"testing"
)

// #3906: env-assignment prefixed bare `go test` must still yield the "."
// scope - the extract side now strips env prefixes exactly like the
// recognition side (coverageIsVerifyCommand).
func TestIssue3906_EnvPrefixedBareGoTestGetsDotScope(t *testing.T) {
	for _, cmd := range []string{
		`GOFLAGS="-p=1" go test`,
		"CGO_ENABLED=0 go build",
		"GOFLAGS=-p=1 GOMEMLIMIT=2GiB go vet",
	} {
		scopes := coverageExtractVerifyScopes(cmd)
		if len(scopes) != 1 || scopes[0] != "." {
			t.Errorf("%s: scopes=%v want [.] (env prefix must not break the bare-Go cwd fallback)", cmd, scopes)
		}
	}
	// Explicit paths with env prefix keep working.
	scopes := coverageExtractVerifyScopes("GOFLAGS=x go test ./internal/agent/")
	// Explicit paths with env prefix keep working (scope is normalized:
	// "./pkg/" -> "pkg").
	if len(scopes) != 1 || scopes[0] != "internal/agent" {
		t.Errorf("explicit path with env prefix: scopes=%v", scopes)
	}
}

// #3905: warning lines starting with a relative file path that happens to
// begin with ok/pass must NOT be swallowed by the bare-prefix noise filter.
func TestIssue3905_PassOkFilenameWarningsNotSwallowed(t *testing.T) {
	out := "password.go:12: printf ... has arguments but no formatting directives\nokapi.py:3:1: F401 'os' imported but unused\nok\tgithub.com/topcheer/ggcode/internal/agent\t1.2s\nPASS\nrunning golangci-lint...\n"
	warnings := extractLintWarnings(out)
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "password.go:12:") {
		t.Errorf("password.go warning swallowed: %q", warnings)
	}
	if !strings.Contains(joined, "okapi.py:3:1:") {
		t.Errorf("okapi.py warning swallowed: %q", warnings)
	}
	for _, w := range warnings {
		if w == "ok\tgithub.com/topcheer/ggcode/internal/agent\t1.2s" || w == "PASS" || strings.HasPrefix(w, "running ") {
			t.Errorf("plain status line kept as warning: %q", w)
		}
	}
}
