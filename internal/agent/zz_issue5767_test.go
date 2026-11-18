package agent

// Regression pin for #5767: .githooks/pre-commit collected staged Go files
// with the bash>=4 builtin `mapfile`. Stock macOS /bin/bash is 3.2 and has no
// mapfile, so every commit routed through .githooks on such a host failed
// deterministically at hook line 11 (observed as 6-attempt writeback
// exhaustion in the teamauto delivery path). The hook now uses a portable
// while-read loop. This test pins the contract that .githooks scripts stay
// bash-3.2 compatible.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var issue5767Bash4OnlyConstructs = []struct {
	name string
	re   *regexp.Regexp
}{
	{"mapfile builtin", regexp.MustCompile(`(^|[[:space:];])mapfile[[:space:]]`)},
	{"readarray builtin", regexp.MustCompile(`(^|[[:space:];])readarray[[:space:]]`)},
	{"associative array declare -A", regexp.MustCompile(`(^|[[:space:];])declare[[:space:]]+-[A-Za-z]*A`)},
	{"coproc keyword", regexp.MustCompile(`(^|[[:space:];])coproc[[:space:]]`)},
	{"case-modifier expansion ${var,,}/${var^^}", regexp.MustCompile(`\$\{[a-zA-Z_][a-zA-Z0-9_]*(,,|\^\^)`)},
	{"append-both redirect &>>", regexp.MustCompile(`&>>`)},
}

// issue5767StripComments removes full-line comments so explanatory prose
// mentioning a construct name (e.g. "has no mapfile") does not trip the scan.
func issue5767StripComments(script string) string {
	var kept []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func issue5767FindRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if fi, err := os.Stat(filepath.Join(dir, ".githooks")); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip(".githooks not found above test cwd - hook contract does not apply outside the repo checkout")
	return ""
}

func TestIssue5767GitHooksAvoidBash4OnlyConstructs(t *testing.T) {
	root := issue5767FindRepoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, ".githooks"))
	if err != nil {
		t.Fatalf("read .githooks: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, ".githooks", e.Name()))
		if err != nil {
			t.Fatalf("read hook %s: %v", e.Name(), err)
		}
		code := issue5767StripComments(string(raw))
		checked++
		for _, c := range issue5767Bash4OnlyConstructs {
			if loc := c.re.FindStringIndex(code); loc != nil {
				lineNo := 1 + strings.Count(code[:loc[0]], "\n")
				t.Errorf(".githooks/%s:%d uses bash>=4-only construct [%s] - stock macOS /bin/bash is 3.2 and fails here (#5767)",
					e.Name(), lineNo, c.name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no hook scripts found under .githooks")
	}
}

func TestIssue5767HooksParseAndPatternRunsUnderSystemBash(t *testing.T) {
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("/bin/bash not present")
	}
	root := issue5767FindRepoRoot(t)

	verOut, err := exec.Command("/bin/bash", "-c", "echo ${BASH_VERSION%%.*}").Output()
	if err != nil {
		t.Fatalf("probe system bash version: %v", err)
	}
	major, err := strconv.Atoi(strings.TrimSpace(string(verOut)))
	if err != nil {
		t.Fatalf("parse system bash version %q: %v", verOut, err)
	}

	entries, _ := os.ReadDir(filepath.Join(root, ".githooks"))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		hookPath := filepath.Join(root, ".githooks", e.Name())
		if out, err := exec.Command("/bin/bash", "-n", hookPath).CombinedOutput(); err != nil {
			t.Errorf(".githooks/%s fails syntax check under system bash %d: %v\n%s", e.Name(), major, err, out)
		}
	}

	// The portable replacement for `mapfile -t arr < <(cmd)`: prove the exact
	// pattern used by the fixed hook collects every line under the system bash
	// (darwin = 3.2, the version that lacked mapfile and caused #5767).
	script := `set -euo pipefail
staged_go_files=()
while IFS= read -r f; do
  [[ -n "${f}" ]] && staged_go_files+=("${f}")
done < <(printf 'a.go\nb.go\n')
echo "${#staged_go_files[@]}"`
	out, err := exec.Command("/bin/bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("portable staged-file loop under /bin/bash %d: %v\n%s", major, err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Errorf("portable staged-file loop under /bin/bash %d collected %s lines, want 2", major, got)
	}
}
