package tool

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// #4039: PreviewChanges must enforce the executor's prechecks, else it
// returns a "successful" plan the executor will refuse (approval-gate
// fidelity, #1547 contract).
func TestIssue4039PreviewRejectsOutOfRangeGroupRefs(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/x.go"
	if err := osWriteFile4039(file, "alpha-beta gamma-delta\n"); err != nil {
		t.Fatal(err)
	}
	br := BatchReplace{WorkingDir: dir}
	args, _ := json.Marshal(map[string]interface{}{
		"pattern": "(\\w+)-(\\w+)", "replacement": "$5", "is_regex": true, "files": []string{file},
	})
	_, err := br.PreviewChanges(args)
	if err == nil {
		t.Fatal("preview must reject an out-of-range $5 replacement (executor would refuse it; a success plan would show matched text deleted)")
	}
	if !strings.Contains(err.Error(), "capture group 5") {
		t.Fatalf("error should name the offending ref, got: %v", err)
	}
}

func TestIssue4039PreviewKeepsValidRefsAndLimits(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/y.go"
	if err := osWriteFile4039(file, "alpha-beta\n"); err != nil {
		t.Fatal(err)
	}
	br := BatchReplace{WorkingDir: dir}

	// Valid ref previews fine.
	args, _ := json.Marshal(map[string]interface{}{
		"pattern": "(\\w+)-(\\w+)", "replacement": "$2-$1", "is_regex": true, "files": []string{file},
	})
	plans, err := br.PreviewChanges(args)
	if err != nil || len(plans) != 1 || plans[0].NewContent != "beta-alpha\n" {
		t.Fatalf("valid-ref preview broken: plans=%v err=%v", plans, err)
	}

	// Limits mirror the executor.
	big := strings.Repeat("a", maxBatchReplacePatternLen+1)
	args, _ = json.Marshal(map[string]interface{}{
		"pattern": big, "replacement": "r", "files": []string{file},
	})
	if _, err := br.PreviewChanges(args); err == nil {
		t.Fatal("oversized pattern must be rejected at preview time")
	}
	files := make([]string, maxBatchReplaceFiles+1)
	for i := range files {
		files[i] = file
	}
	args, _ = json.Marshal(map[string]interface{}{
		"pattern": "a", "replacement": "r", "files": files,
	})
	if _, err := br.PreviewChanges(args); err == nil {
		t.Fatal("too many files must be rejected at preview time")
	}
}

func osWriteFile4039(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
