package agent

import (
	"strings"
	"testing"
)

func TestOrphanFileNewSourceFile(t *testing.T) {
	o := newOrphanFileState()

	// Create a new Go file
	warn := o.recordToolCall("write_file", `{"path":"newfile.go"}`, 1)
	if warn != "" {
		t.Fatalf("expected no warning on creation, got: %s", warn)
	}
	if len(o.newFiles) != 1 {
		t.Fatalf("expected 1 new file tracked, got %d", len(o.newFiles))
	}
}

func TestOrphanFileThresholdWarning(t *testing.T) {
	o := newOrphanFileState()

	// Create a new Go file
	o.recordToolCall("write_file", `{"path":"newfile.go"}`, 1)

	// 2 subsequent non-integration calls (below threshold)
	o.recordToolCall("read_file", `{"path":"other.go"}`, 2)
	o.recordToolCall("grep", `{"pattern":"test"}`, 3)

	// 3rd call: threshold reached, should warn
	warn := o.recordToolCall("list_directory", `{"path":"."}`, 4)
	if warn == "" {
		t.Fatal("expected orphan warning after 3 non-integration calls")
	}
	if !strings.Contains(warn, "Orphaned File") {
		t.Fatalf("warning should contain 'Orphaned File', got: %s", warn)
	}
}

func TestOrphanFileEditIntegration(t *testing.T) {
	o := newOrphanFileState()

	// Create a new Go file
	o.recordToolCall("write_file", `{"path":"newfile.go"}`, 1)

	// Edit an existing file -- should mark as integrated
	warn := o.recordToolCall("edit_file", `{"file_path":"main.go"}`, 2)
	if warn != "" {
		t.Fatalf("expected no warning after integration edit, got: %s", warn)
	}
	if len(o.newFiles) != 0 {
		t.Fatal("new files should be cleared after integration")
	}
}

func TestOrphanFileBuildIntegration(t *testing.T) {
	o := newOrphanFileState()

	o.recordToolCall("write_file", `{"path":"helper.go"}`, 1)

	// Run a build -- should mark as integrated
	warn := o.recordToolCall("run_command", `{"command":"go build ./..."}`, 2)
	if warn != "" {
		t.Fatalf("expected no warning after build, got: %s", warn)
	}
}

func TestOrphanFileNonSourceIgnored(t *testing.T) {
	o := newOrphanFileState()

	// Create a markdown file -- should NOT be tracked as source
	o.recordToolCall("write_file", `{"path":"README.md"}`, 1)
	if len(o.newFiles) != 0 {
		t.Fatalf("non-source files should not be tracked, got %d", len(o.newFiles))
	}
}

func TestOrphanFileSourceExtensions(t *testing.T) {
	exts := []string{".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".kt", ".rb", ".c", ".cpp"}
	for _, ext := range exts {
		if !isOrphanSourceFile("test" + ext) {
			t.Errorf("isOrphanSourceFile should return true for %s", ext)
		}
	}
}

func TestOrphanFileNonSourceExtensions(t *testing.T) {
	exts := []string{".md", ".txt", ".json", ".yaml", ".yml", ".html", ".css", ".sql", ".sh", ".env"}
	for _, ext := range exts {
		if isOrphanSourceFile("test" + ext) {
			t.Errorf("isOrphanSourceFile should return false for %s", ext)
		}
	}
}

func TestOrphanFileMaxWarnings(t *testing.T) {
	o := newOrphanFileState()

	warnCount := 0
	// Create file, then many non-integration calls
	o.recordToolCall("write_file", `{"path":"f.go"}`, 1)
	for i := 0; i < 20; i++ {
		w := o.recordToolCall("read_file", `{"path":"x"}`, i+2)
		if w != "" {
			warnCount++
		}
	}

	if warnCount != orphanFileMaxWarnings {
		t.Fatalf("expected %d warnings, got %d", orphanFileMaxWarnings, warnCount)
	}
}

func TestOrphanFileReset(t *testing.T) {
	o := newOrphanFileState()
	o.recordToolCall("write_file", `{"path":"f.go"}`, 1)
	o.reset()
	if len(o.newFiles) != 0 || o.callsSince != 0 || o.warnings != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestOrphanFileMultiFileWrite(t *testing.T) {
	args := `{"files":[{"path":"a.go","content":"x"},{"path":"b.go","content":"y"}]}`
	path := extractFilePathOrArg(args, "multi_file_write")
	if path != "a.go" {
		t.Fatalf("expected a.go, got %s", path)
	}
}

func TestOrphanFileBatchReplaceIntegration(t *testing.T) {
	o := newOrphanFileState()
	o.recordToolCall("write_file", `{"path":"f.go"}`, 1)
	warn := o.recordToolCall("batch_replace", `{}`, 2)
	if warn != "" {
		t.Fatal("batch_replace should mark as integrated")
	}
}

func TestOrphanFileMultiFileEditIntegration(t *testing.T) {
	o := newOrphanFileState()
	o.recordToolCall("write_file", `{"path":"f.go"}`, 1)
	warn := o.recordToolCall("multi_file_edit", `{}`, 2)
	if warn != "" {
		t.Fatal("multi_file_edit should mark as integrated")
	}
}
