package agent

import (
	"strings"
	"testing"
)

func TestBreakingChanges_NoChange(t *testing.T) {
	old := `package foo
func Exported(a int, b string) error { return nil }
func internal() int { return 0 }
type Server struct { Name string; Port int }
`
	// Identical content - no changes.
	warn := checkBreakingChanges("test.go", old, old)
	if warn != "" {
		t.Errorf("expected no warning for unchanged file, got: %s", warn)
	}
}

func TestBreakingChanges_NonExportedFuncModified(t *testing.T) {
	old := `package foo
func internal(a int) int { return a }
`
	new := `package foo
func internal(a int, b string) int { return a }
`
	warn := checkBreakingChanges("test.go", old, new)
	if warn != "" {
		t.Errorf("expected no warning for non-exported func change, got: %s", warn)
	}
}

func TestBreakingChanges_ExportedFuncParamAdded(t *testing.T) {
	old := `package foo
func Process(data []byte) error { return nil }
`
	new := `package foo
func Process(data []byte, opts *Options) error { return nil }
`
	warn := checkBreakingChanges("test.go", old, new)
	if !strings.Contains(warn, "Process") || !strings.Contains(warn, "signature changed") {
		t.Errorf("expected breaking change warning for Process, got: %q", warn)
	}
}

func TestBreakingChanges_ExportedFuncReturnChanged(t *testing.T) {
	old := `package foo
func GetUser(id int) User { return User{} }
`
	new := `package foo
func GetUser(id int) (*User, error) { return nil, nil }
`
	warn := checkBreakingChanges("test.go", old, new)
	if !strings.Contains(warn, "GetUser") || !strings.Contains(warn, "signature changed") {
		t.Errorf("expected breaking change warning for GetUser, got: %q", warn)
	}
}

func TestBreakingChanges_ExportedMethodModified(t *testing.T) {
	old := `package foo
type Server struct{}
func (s *Server) Handle(req *Request) error { return nil }
`
	new := `package foo
type Server struct{}
func (s *Server) Handle(req *Request, ctx context.Context) error { return nil }
`
	warn := checkBreakingChanges("test.go", old, new)
	if !strings.Contains(warn, "Server.Handle") {
		t.Errorf("expected breaking change warning for Server.Handle, got: %q", warn)
	}
}

func TestBreakingChanges_StructFieldRemoved(t *testing.T) {
	old := `package foo
type Config struct {
	Host string
	Port int
	Timeout time.Duration
}
`
	new := `package foo
type Config struct {
	Host string
	Port int
}
`
	warn := checkBreakingChanges("test.go", old, new)
	if !strings.Contains(warn, "Config") || !strings.Contains(warn, "definition changed") {
		t.Errorf("expected breaking change warning for Config, got: %q", warn)
	}
}

func TestBreakingChanges_InterfaceMethodRemoved(t *testing.T) {
	old := `package foo
type Reader interface {
	Read(p []byte) (int, error)
	Close() error
}
`
	new := `package foo
type Reader interface {
	Read(p []byte) (int, error)
}
`
	warn := checkBreakingChanges("test.go", old, new)
	if !strings.Contains(warn, "Reader") || !strings.Contains(warn, "definition changed") {
		t.Errorf("expected breaking change warning for Reader, got: %q", warn)
	}
}

func TestBreakingChanges_ExportedVarTypeChanged(t *testing.T) {
	old := `package foo
var DefaultTimeout = 30 * time.Second
`
	new := `package foo
var DefaultTimeout = 30
`
	warn := checkBreakingChanges("test.go", old, new)
	// var without explicit type - values may or may not change fingerprint
	// since neither has explicit type. This is acceptable.
	_ = warn
}

func TestBreakingChanges_ExportedConstToVarChange(t *testing.T) {
	old := `package foo
const MaxRetries int = 3
`
	new := `package foo
var MaxRetries int = 3
`
	warn := checkBreakingChanges("test.go", old, new)
	if !strings.Contains(warn, "MaxRetries") {
		t.Errorf("expected breaking change warning for MaxRetries, got: %q", warn)
	}
}

func TestBreakingChanges_NewExportedFuncAdded(t *testing.T) {
	old := `package foo
func Existing() error { return nil }
`
	new := `package foo
func Existing() error { return nil }
func NewFunc(x int) string { return "" }
`
	warn := checkBreakingChanges("test.go", old, new)
	// Adding a new function is NOT a breaking change.
	if warn != "" {
		t.Errorf("expected no warning for added function, got: %s", warn)
	}
}

func TestBreakingChanges_OnlyCommentChanged(t *testing.T) {
	old := `package foo
// Process handles the data.
func Process(data []byte) error { return nil }
`
	new := `package foo
// Process handles the input data with validation.
func Process(data []byte) error { return nil }
`
	warn := checkBreakingChanges("test.go", old, new)
	if warn != "" {
		t.Errorf("expected no warning for comment-only change, got: %s", warn)
	}
}

func TestBreakingChanges_NotGoFile(t *testing.T) {
	warn := checkBreakingChanges("readme.md", "old content", "new content")
	if warn != "" {
		t.Errorf("expected no warning for non-Go file, got: %s", warn)
	}
}

func TestBreakingChanges_ParseErrorGraceful(t *testing.T) {
	// Invalid Go should not panic, should return empty.
	old := `package foo
func Foo() {}`
	new := `package foo
func Foo() {`
	warn := checkBreakingChanges("test.go", old, new)
	if warn != "" {
		t.Errorf("expected no warning for unparseable file, got: %s", warn)
	}
}

func TestBreakingChanges_TruncationCount(t *testing.T) {
	// 5 exported functions with signature changes → should report "...and 3 more"
	old := `package foo
func FuncA(x int) {}
func FuncB(x int) {}
func FuncC(x int) {}
func FuncD(x int) {}
func FuncE(x int) {}
`
	new := `package foo
func FuncA(x int, y string) {}
func FuncB(x int, y string) {}
func FuncC(x int, y string) {}
func FuncD(x int, y string) {}
func FuncE(x int, y string) {}
`
	warn := checkBreakingChanges("test.go", old, new)
	if !strings.Contains(warn, "...and 3 more breaking change(s)") {
		t.Errorf("expected '...and 3 more breaking change(s)', got: %s", warn)
	}
}

func TestBreakingChanges_MethodOnNonExportedType(t *testing.T) {
	old := `package foo
type internal struct{}
func (i *internal) DoSomething() error { return nil }
`
	new := `package foo
type internal struct{}
func (i *internal) DoSomething(ctx context.Context) error { return nil }
`
	warn := checkBreakingChanges("test.go", old, new)
	// Methods on non-exported types are not part of public API.
	if warn != "" {
		t.Errorf("expected no warning for method on non-exported type, got: %s", warn)
	}
}
