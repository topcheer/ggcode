package tool

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestRgCountModeAppliesHeadLimitAndOffset(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 600; i++ {
		sb.WriteString("pkg/file")
		if i < 10 {
			sb.WriteString("00")
		} else if i < 100 {
			sb.WriteString("0")
		}
		sb.WriteString(string(rune('0' + i%10)))
		sb.WriteString(".go: 3\n")
	}
	res, err := formatGrepOutput(sb.String(), grepArgs{OutputMode: "count"})
	if err != nil {
		t.Fatalf("formatGrepOutput: %v", err)
	}
	if !strings.Contains(res.Content, "showing 1-500 of 600 files") {
		t.Fatalf("count mode default cap summary missing or wrong: %q", tailOf(res.Content, 120))
	}
	got := strings.Count(res.Content, ".go:")
	if got < 500 || got > 501 {
		t.Fatalf("count listing not capped at 500: %d file lines", got)
	}

	res2, err := formatGrepOutput(sb.String(), grepArgs{OutputMode: "count", Offset: 500})
	if err != nil {
		t.Fatalf("formatGrepOutput offset: %v", err)
	}
	if !strings.Contains(res2.Content, "showing 501-600 of 600 files") {
		t.Fatalf("count offset summary missing or wrong: %q", tailOf(res2.Content, 120))
	}
	if got := strings.Count(res2.Content, ".go:"); got != 100 {
		t.Fatalf("count offset listing should show exactly 100 files, got %d", got)
	}
	if !strings.Contains(res2.Content, "1800 match(es) total") {
		t.Fatalf("count total must reflect all files, got: %q", tailOf(res2.Content, 120))
	}
}

func TestRgFilesWithMatchesAppliesOffset(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 6; i++ {
		sb.WriteString("dir")
		sb.WriteString(string(rune('0' + i)))
		sb.WriteString("/file.go\n")
	}
	res, err := formatGrepOutput(sb.String(), grepArgs{OutputMode: "files_with_matches", HeadLimit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("formatGrepOutput: %v", err)
	}
	if !strings.Contains(res.Content, "dir2/file.go") || !strings.Contains(res.Content, "dir3/file.go") {
		t.Fatalf("offset=2 should show files 3-4, got: %q", res.Content)
	}
	if strings.Contains(res.Content, "dir0/file.go\n") {
		t.Fatalf("offset must skip the first page, got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "showing 3-4 of 6 files") {
		t.Fatalf("offset summary missing or wrong: %q", tailOf(res.Content, 120))
	}
}

func TestRgExitResultClassifiesExitCode(t *testing.T) {

	shPath, shErr := exec.LookPath("sh")
	if shErr != nil {
		t.Skip("no sh on PATH; exit-code classification covered by build only")
	}

	cmdErr := exec.Command(shPath, "-c", "echo ' error parsing glob ' >&2; exit 2")
	_, err := cmdErr.Output()
	if err == nil {
		t.Fatalf("expected exit error from helper")
	}
	res, handled := rgExitResult(err)
	if !handled {
		t.Fatalf("exit 2 must be handled as an error")
	}
	if !res.IsError {
		t.Fatalf("exit 2 result must be IsError, got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "error parsing glob") || !strings.HasPrefix(res.Content, "ripgrep error:") {
		t.Fatalf("exit 2 result should carry trimmed stderr, got: %q", res.Content)
	}

	cmdNo := exec.Command(shPath, "-c", "exit 1")
	_, err = cmdNo.Output()
	if err == nil {
		t.Fatalf("expected exit error from helper")
	}
	if _, handled := rgExitResult(err); handled {
		t.Fatalf("exit 1 must not be classified as an error")
	}

	res, handled = rgExitResult(errors.New("exec: not found"))
	if !handled || !res.IsError {
		t.Fatalf("spawn failure must surface as IsError, got handled=%v res=%+v", handled, res)
	}
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
