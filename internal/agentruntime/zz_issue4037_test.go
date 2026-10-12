package agentruntime

// #4037 companion: WriteKeysEnv persist failures must be fatal at all six
// secret-migration sites. The old pattern logged via debug.Log and STILL
// replaced the value with a ${VAR} reference - on restart the reference
// expanded to empty and the secret was gone from disk entirely, while the
// tool had reported success. Source-level guard (config.WriteKeysEnv is a
// package-level function with no failure injection seam; behavioral harness
// would need a refactor - same disclosure as #4015/#4023 guards).

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
	// Each hardened site returns an error naming the env var and refusing
	// the ${VAR} migration. Six sites per the issue: MCP env, MCP headers,
	// IM extra map, IM SetIMAdapterExtra, A2A oauth2 secret, A2A field.
	want := regexp.MustCompile(`NOT migrated to a \$\{.*\} reference`).FindAllStringIndex(text, -1)
	if len(want) != 6 {
		t.Fatalf("fatal migration sites = %d, want 6 (MCP env/headers, IM extra/field, A2A oauth2/field) - a site regressed or was added without the guard", len(want))
	}
}
