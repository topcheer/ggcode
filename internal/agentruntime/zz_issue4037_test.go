package agentruntime

import (
	"os"
	"regexp"
	"testing"
)

func TestIssue4037_NoSwallowedKeysEnvFailure(t *testing.T) {
	src, err := os.ReadFile("config_access.go")
	if err != nil {
		t.Skipf("config_access.go not readable: %v", err)
	}
	text := string(src)
	if regexp.MustCompile(`debug\.Log\("config", "failed to persist`).MatchString(text) {
		t.Fatal("swallowed WriteKeysEnv failure pattern is back - a failed keys.env persist must return an error, not log-and-continue (#4037 regression)")
	}
}

func TestIssue4037_SixFatalMigrationSites(t *testing.T) {
	src, err := os.ReadFile("config_access.go")
	if err != nil {
		t.Skipf("config_access.go not readable: %v", err)
	}
	text := string(src)

	want := regexp.MustCompile(`NOT migrated to a \$\{.*\} reference`).FindAllStringIndex(text, -1)
	if len(want) != 6 {
		t.Fatalf("fatal migration sites = %d, want 6 (MCP env/headers, IM extra/field, A2A oauth2/field) - a site regressed or was added without the guard", len(want))
	}
}
