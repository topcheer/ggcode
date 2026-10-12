package plugin

import (
	"os"
	"strings"
	"testing"
)

// #4042: (false, 0) used to mean BOTH "ListTools errored" and "succeeded
// with zero tools" (a legal resources/prompts-only server), so a successful
// zero-tool refresh was misreported as RefreshFailed. refreshTools must
// carry the error explicitly and refreshToolsNow must judge by it.
func TestIssue4042ZeroToolRefreshNotFailed(t *testing.T) {
	src, err := os.ReadFile("mcp_loader.go")
	if err != nil {
		t.Fatalf("read mcp_loader.go: %v", err)
	}
	text := string(src)

	// Signature: three-value return.
	sig := strings.Index(text, "func (m *MCPPlugin) refreshTools(client *mcp.Client) (changed bool, count int, err error)")
	if sig < 0 {
		t.Fatal("refreshTools must return (changed, count, err) explicitly (#4042)")
	}
	// Error path returns the error.
	errPath := strings.Index(text, "return false, 0, err")
	if errPath < 0 {
		t.Fatal("refreshTools error path must propagate err (#4042)")
	}
	// Caller judges by err, not by the ambiguous zero count.
	caller := strings.Index(text, "changed, count, err := m.refreshTools(client)")
	if caller < 0 {
		t.Fatal("refreshToolsNow must receive the explicit error (#4042)")
	}
	window := text[caller : caller+600]
	if !strings.Contains(window, "if err != nil {\n\t\treturn RefreshFailed, 0") && !strings.Contains(window, "if err != nil {") {
		t.Fatal("refreshToolsNow must gate RefreshFailed on err != nil (#4042)")
	}
	for _, line := range strings.Split(window, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "//") {
			continue // rationale comments may cite the old predicate verbatim
		}
		if strings.Contains(line, "count == 0 && !changed") {
			t.Fatal("ambiguous zero-count failure predicate still present (#4042 regression)")
		}
	}
}
