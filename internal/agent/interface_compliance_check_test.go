package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckInterfaceCompliance_NewMethodAdded(t *testing.T) {
	tmp := t.TempDir()

	// Create an implementor file.
	implFile := filepath.Join(tmp, "impl.go")
	implSrc := `package foo

type Reader struct{}

func (r *Reader) Read(p []byte) (int, error) {
	return 0, nil
}
`
	if err := os.WriteFile(implFile, []byte(implSrc), 0644); err != nil {
		t.Fatal(err)
	}

	// The interface file: old version had only Read, new adds Write.
	oldSrc := `package foo

type ReadWriter interface {
	Read(p []byte) (int, error)
}
`
	newSrc := `package foo

type ReadWriter interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
}
`

	ifaceFile := filepath.Join(tmp, "iface.go")
	w := checkInterfaceCompliance(ifaceFile, oldSrc, newSrc)
	if w == "" {
		t.Fatal("expected compliance warning for missing Write method")
	}
	if !strings.Contains(w, "Write") {
		t.Errorf("warning should mention missing Write method: %s", w)
	}
	if !strings.Contains(w, "Reader") {
		t.Errorf("warning should mention Reader type: %s", w)
	}
}

func TestCheckInterfaceCompliance_NoViolation(t *testing.T) {
	tmp := t.TempDir()

	// Implementor has all methods.
	implFile := filepath.Join(tmp, "impl.go")
	implSrc := `package foo

type ReadWriter struct{}

func (rw *ReadWriter) Read(p []byte) (int, error) {
	return 0, nil
}

func (rw *ReadWriter) Write(p []byte) (int, error) {
	return 0, nil
}
`
	if err := os.WriteFile(implFile, []byte(implSrc), 0644); err != nil {
		t.Fatal(err)
	}

	oldSrc := `package foo

type ReadWriter interface {
	Read(p []byte) (int, error)
}
`
	newSrc := `package foo

type ReadWriter interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
}
`

	ifaceFile := filepath.Join(tmp, "iface.go")
	w := checkInterfaceCompliance(ifaceFile, oldSrc, newSrc)
	if w != "" {
		t.Errorf("expected no compliance warning, got: %s", w)
	}
}

func TestCheckInterfaceCompliance_NoInterfaceChange(t *testing.T) {
	oldSrc := `package foo

type Reader interface {
	Read(p []byte) (int, error)
}
`
	newSrc := `package foo

type Reader interface {
	Read(p []byte) (int, error)
}

// some unrelated change
var x = 42
`
	w := checkInterfaceCompliance("iface.go", oldSrc, newSrc)
	if w != "" {
		t.Errorf("expected no warning when interface didn't change, got: %s", w)
	}
}

func TestCheckInterfaceCompliance_NonGoFile(t *testing.T) {
	w := checkInterfaceCompliance("iface.py", "old", "new")
	if w != "" {
		t.Errorf("expected empty for non-Go file")
	}
}

func TestCheckInterfaceCompliance_TestFile(t *testing.T) {
	w := checkInterfaceCompliance("iface_test.go", "", "")
	if w != "" {
		t.Errorf("expected empty for test file")
	}
}

func TestCheckInterfaceCompliance_RemovedMethod(t *testing.T) {
	tmp := t.TempDir()

	implFile := filepath.Join(tmp, "impl.go")
	implSrc := `package foo

type Service struct{}

func (s *Service) Start() error { return nil }
func (s *Service) Stop() error { return nil }
func (s *Service) Name() string { return "svc" }
`
	if err := os.WriteFile(implFile, []byte(implSrc), 0644); err != nil {
		t.Fatal(err)
	}

	oldSrc := `package foo

type Runner interface {
	Start() error
	Stop() error
}
`
	newSrc := `package foo

type Runner interface {
	Start() error
	Stop() error
	Name() string
	Restart() error
}
`

	ifaceFile := filepath.Join(tmp, "iface.go")
	w := checkInterfaceCompliance(ifaceFile, oldSrc, newSrc)
	if w == "" {
		t.Fatal("expected compliance warning for missing Restart method")
	}
	if !strings.Contains(w, "Restart") {
		t.Errorf("warning should mention missing Restart method: %s", w)
	}
	if !strings.Contains(w, "Service") {
		t.Errorf("warning should mention Service type: %s", w)
	}
}

func TestCheckInterfaceCompliance_NewInterface(t *testing.T) {
	tmp := t.TempDir()

	implFile := filepath.Join(tmp, "impl.go")
	implSrc := `package foo

type Store struct{}

func (s *Store) Get(key string) (string, error) { return "", nil }
func (s *Store) Set(key, val string) error { return nil }
`
	if err := os.WriteFile(implFile, []byte(implSrc), 0644); err != nil {
		t.Fatal(err)
	}

	// New interface (didn't exist before).
	newSrc := `package foo

type Cache interface {
	Get(key string) (string, error)
	Set(key, val string) error
	Delete(key string) error
	Flush() error
}
`

	ifaceFile := filepath.Join(tmp, "iface.go")
	w := checkInterfaceCompliance(ifaceFile, "", newSrc)
	if w == "" {
		t.Fatal("expected compliance warning for new interface with missing methods")
	}
	if !strings.Contains(w, "Delete") || !strings.Contains(w, "Flush") {
		t.Errorf("warning should mention missing Delete and Flush methods: %s", w)
	}
}

func TestCheckInterfaceCompliance_EmptyContent(t *testing.T) {
	w := checkInterfaceCompliance("iface.go", "", "")
	if w != "" {
		t.Errorf("expected empty for empty content")
	}
}
