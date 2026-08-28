package knight

// Regression test for #1260: isTempDir must not substring-match tmp/temp
// path segments — real project paths with those segments silently disabled
// the workspace filter and let every other project's sessions leak into
// skill extraction.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsTempDirExactPrefixesOnly(t *testing.T) {
	// Real project paths that must NOT be judged as temp dirs.
	real := []string{
		"/home/user/tmp/work",
		"/home/user/temp/app",
		"D:/code/temp/app",
		"/Users/dev/projects/my_tmp_project",
		"/workspace/tmp_project",
		"/workspace/temp_branch",
		"/var/www/site",
	}
	for _, p := range real {
		if isTempDir(p) {
			t.Fatalf("#1260: real project path %q misjudged as temp dir (workspace filter would be silently disabled)", p)
		}
	}

	// Genuine temp locations must still be detected.
	tmp := t.TempDir() // under os.TempDir()
	if !isTempDir(tmp) {
		t.Fatalf("t.TempDir() %q must be detected as temp", tmp)
	}
	if !isTempDir(filepath.Join(tmp, "sub", "dir")) {
		t.Fatalf("path under os.TempDir() must be detected as temp")
	}

	// Deliberate knight test markers keep working.
	if !isTempDir("/workspace/knight-test") {
		t.Fatal("knight- prefix must be detected as temp")
	}
	if !isTempDir("/workspace/test-scenario") {
		t.Fatal("test- prefix must be detected as temp")
	}

	// os.TempDir prefix compare covers /tmp on unix when TMPDIR is unset.
	if os.TempDir() == "/tmp" {
		if !isTempDir("/tmp/foo") {
			t.Fatal("/tmp/foo must be temp when os.TempDir()==/tmp")
		}
	}
}
