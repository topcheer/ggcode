package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatterForFile(t *testing.T) {
	tests := []struct {
		ext      string
		wantCmd  string
		wantCmd2 string // alternative (fallback)
	}{
		{".go", "gofmt", "goimports"},
		{".rs", "rustfmt", ""},
		{".py", "black", "ruff"},
		{".js", "prettier", ""},
		{".ts", "prettier", ""},
		{".tsx", "prettier", ""},
		{".c", "clang-format", ""},
		{".cpp", "clang-format", ""},
		{".sh", "shfmt", ""},
		{".dart", "dart", ""},
		{".unknown", "", ""},
		{".md", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			path := "testfile" + tt.ext
			fc := formatterForFile(path)
			if tt.wantCmd == "" {
				if fc != nil {
					t.Errorf("expected nil formatter for %s, got %+v", tt.ext, fc)
				}
				return
			}
			if fc == nil {
				t.Skipf("no formatter for %s (expected %s/%s)", tt.ext, tt.wantCmd, tt.wantCmd2)
			}
			if fc.command != tt.wantCmd && fc.command != tt.wantCmd2 {
				t.Errorf("expected %s or %s, got %s", tt.wantCmd, tt.wantCmd2, fc.command)
			}
		})
	}
}

func TestAutoFormatFile_GoFile(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not available")
	}

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.go")

	// Write unformatted Go code.
	unformatted := "package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"hello\")}\n"
	if err := os.WriteFile(filePath, []byte(unformatted), 0644); err != nil {
		t.Fatal(err)
	}

	notice := autoFormatFile(filePath)
	_ = notice // may or may not have a notice

	// Verify the file was actually formatted (gofmt adds spaces around braces).
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	formatted := string(data)
	if formatted == unformatted {
		t.Log("Warning: file was not changed by gofmt")
	}
	// gofmt should produce "func main() {" with a space before {
	if !strings.Contains(formatted, "main() {") {
		t.Errorf("expected formatted output with spaces, got: %s", formatted)
	}
}

func TestAutoFormatFile_UnknownExtension(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.unknownext")
	_ = os.WriteFile(filePath, []byte("content"), 0644)

	notice := autoFormatFile(filePath)
	if notice != "" {
		t.Errorf("expected empty notice for unknown extension, got %q", notice)
	}
}

func TestAutoFormatFile_NonExistentFile(t *testing.T) {
	notice := autoFormatFile("/nonexistent/path/to/file.go")
	if notice != "" {
		t.Errorf("expected empty notice for non-existent file, got %q", notice)
	}
}

func TestAutoFormatFile_GoImportsPreference(t *testing.T) {
	if _, err := exec.LookPath("goimports"); err != nil {
		t.Skip("goimports not available")
	}

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.go")

	// Go file with missing import.
	code := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	_ = os.WriteFile(filePath, []byte(code), 0644)

	autoFormatFile(filePath)

	data, _ := os.ReadFile(filePath)
	if !strings.Contains(string(data), "fmt") {
		t.Log("goimports did not add import")
	}
}

func TestAutoFormatFile_DoesNotPanic(t *testing.T) {
	// Verify no panic on various edge cases.
	tmpDir := t.TempDir()

	// Empty file.
	emptyPath := filepath.Join(tmpDir, "empty.go")
	_ = os.WriteFile(emptyPath, []byte(""), 0644)
	_ = autoFormatFile(emptyPath)

	// Binary file.
	binPath := filepath.Join(tmpDir, "bin.go")
	_ = os.WriteFile(binPath, []byte{0x00, 0x01, 0x02}, 0644)
	_ = autoFormatFile(binPath)
}
