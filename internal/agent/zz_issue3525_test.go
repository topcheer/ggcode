package agent

import "testing"

// #3525: error_classifier substring misclassification — three regression pins.
func TestIssue3525AuthErrorNotPermissionDenied(t *testing.T) {
	// 401 from MCP/web_fetch-style tools must land in the new auth_error
	// category, not permission_denied (which injects filesystem guidance
	// and a blanket "do not retry").
	for _, content := range []string{
		"HTTP 401: 401 Unauthorized",
		"Unauthorized",
		"authentication required",
		"invalid api key",
		"token expired",
	} {
		cat := classifyErrorContent("mcp__github__create_issue", content)
		if cat.Name != "auth_error" {
			t.Errorf("for %q: expected auth_error, got %s", content, cat.Name)
		}
		if cat.Guidance == "" {
			t.Error("guidance should not be empty")
		}
	}
	// Genuine filesystem permission errors keep permission_denied semantics.
	if cat := classifyErrorContent("write_file", "open /file: permission denied"); cat.Name != "permission_denied" {
		t.Errorf("permission denied should stay permission_denied, got %s", cat.Name)
	}
}

func TestIssue3525CompileMissingFileIsBuildError(t *testing.T) {
	// go build missing-source form: path comes from package declaration
	// resolution, not agent typing — must be build_error, not file_not_found.
	cat := classifyErrorContent("run_command", "open /w/repo/internal/a/x.go: no such file or directory")
	if cat.Name != "build_error" {
		t.Errorf("expected build_error, got %s", cat.Name)
	}
	// Non-command tools (real path typos) keep file_not_found.
	if cat := classifyErrorContent("read_file", "open /w/repo/nope/x.go: no such file or directory"); cat.Name != "file_not_found" {
		t.Errorf("read_file path typo should stay file_not_found, got %s", cat.Name)
	}
	// Non-.go missing files from commands also stay file_not_found.
	if cat := classifyErrorContent("run_command", "cat /w/repo/missing.txt: no such file or directory"); cat.Name != "file_not_found" {
		t.Errorf("non-.go missing file should stay file_not_found, got %s", cat.Name)
	}
}

func TestIssue3525GitLockContentionNoBareExistsAlready(t *testing.T) {
	// git's real lock error says "File exists" (already covered by the
	// "index.lock" prefix); bare "exists already" must not fire the
	// rm-index.lock guidance for unrelated tools.
	if cat := classifyErrorContent("run_command", "Error: release v1.0 exists already"); cat.Name == "git_lock_contention" {
		t.Error("bare 'exists already' must not classify as git_lock_contention")
	}
	// Real lock contention still fires.
	if cat := classifyErrorContent("git_commit", "fatal: Unable to create '/repo/.git/index.lock': File exists."); cat.Name != "git_lock_contention" {
		t.Errorf("real index.lock error should stay git_lock_contention, got %s", cat.Name)
	}
}
