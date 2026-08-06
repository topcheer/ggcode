package agent

import (
	"testing"
)

func TestCheckPathTraversal_GoFilepathJoinWithUserInput(t *testing.T) {
	old := "package main\nfunc handler() {}\n"
	new := `package main
import (
	"net/http"
	"path/filepath"
)
func handler(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join("/data", r.URL.Query().Get("file"))
	_ = p
}`
	warnings := checkPathTraversal("/tmp/main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected path traversal warning for filepath.Join with user input")
	}
	if !ptHasSubstring(warnings[0], "filepath.Join") {
		t.Errorf("expected filepath.Join in warning, got: %s", warnings[0])
	}
}

func TestCheckPathTraversal_GoReadFileConcatenation(t *testing.T) {
	old := ""
	new := `package main
import (
	"net/http"
	"os"
)
func handler(w http.ResponseWriter, r *http.Request) {
	data, _ := os.ReadFile("/data/" + r.URL.Query().Get("name"))
	_ = data
}`
	warnings := checkPathTraversal("/tmp/main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for os.ReadFile with concatenated user input")
	}
}

func TestCheckPathTraversal_GoServeFileDynamic(t *testing.T) {
	old := ""
	new := `package main
import "net/http"
func handler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, r.FormValue("path"))
}`
	warnings := checkPathTraversal("/tmp/main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for http.ServeFile with dynamic path")
	}
}

func TestCheckPathTraversal_GoServeFileLiteralOK(t *testing.T) {
	old := ""
	new := `package main
import "net/http"
func handler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/index.html")
}`
	warnings := checkPathTraversal("/tmp/main.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for literal ServeFile path, got: %v", warnings)
	}
}

func TestCheckPathTraversal_GoExplicitTraversalLiteral(t *testing.T) {
	old := ""
	new := `package main
import "os"
func risky() {
	_ = os.Open("../../../etc/passwd")
}`
	warnings := checkPathTraversal("/tmp/main.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for explicit traversal literal")
	}
	if !ptHasSubstring(warnings[0], "explicit traversal") {
		t.Errorf("expected explicit traversal category, got: %s", warnings[0])
	}
}

func TestCheckPathTraversal_GoSafeCodeNoWarning(t *testing.T) {
	old := ""
	new := `package main
import "os"
func readConfig() {
	_ = os.ReadFile("/etc/myapp/config.yaml")
}`
	warnings := checkPathTraversal("/tmp/main.go", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for safe static path, got: %v", warnings)
	}
}

func TestCheckPathTraversal_DeltaAware(t *testing.T) {
	// The pattern already exists in old content -- should NOT warn.
	content := `package main
import "net/http"
import "path/filepath"
func handler(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join("/data", r.URL.Query().Get("file"))
	_ = p
}`
	warnings := checkPathTraversal("/tmp/main.go", content, content)
	if len(warnings) != 0 {
		t.Fatalf("expected no delta warnings for pre-existing pattern, got: %v", warnings)
	}
}

func TestCheckPathTraversal_JoinPathJoinWithUserInput(t *testing.T) {
	old := ""
	new := "const path = require('path');\nconst fs = require('fs');\n" +
		"router.get('/file', (req, res) => {\n" +
		"\tconst filePath = path.join(baseDir, req.params.filename);\n" +
		"\tfs.readFile(filePath);\n" +
		"});"
	warnings := checkPathTraversal("/tmp/app.js", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected path traversal warning for JS path.join with user input")
	}
}

func TestCheckPathTraversal_JoinFsConcatUserInput(t *testing.T) {
	old := ""
	new := "const fs = require('fs');\n" +
		"function serveFile(req, res) {\n" +
		"\tfs.readFile('/data/' + req.query.file);\n" +
		"}"
	warnings := checkPathTraversal("/tmp/app.js", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for JS fs.readFile with concatenated user input")
	}
}

func TestCheckPathTraversal_PythonOpenWithUserInput(t *testing.T) {
	old := ""
	new := "from flask import Flask, request\n" +
		"app = Flask(__name__)\n" +
		"@app.route('/file')\n" +
		"def serve():\n" +
		"\tf = open('/data/' + request.args.get('name'))\n" +
		"\treturn f.read()"
	warnings := checkPathTraversal("/tmp/app.py", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for Python open with concatenated user input")
	}
}

func TestCheckPathTraversal_PythonSendFileWithUserInput(t *testing.T) {
	old := ""
	new := "from flask import Flask, request, send_file\n" +
		"app = Flask(__name__)\n" +
		"@app.route('/download')\n" +
		"def download():\n" +
		"\treturn send_file(request.args.get('path'))"
	warnings := checkPathTraversal("/tmp/app.py", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected warning for Python send_file with user input")
	}
}

func TestCheckPathTraversal_PythonSafeNoWarning(t *testing.T) {
	old := ""
	new := "import os\n" +
		"def read_config():\n" +
		"\twith open('/etc/myapp/config.yaml') as f:\n" +
		"\t\treturn f.read()"
	warnings := checkPathTraversal("/tmp/app.py", old, new)
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for safe Python static path, got: %v", warnings)
	}
}

func TestCheckPathTraversal_UnsupportedExtReturnsNil(t *testing.T) {
	warnings := checkPathTraversal("/tmp/data.json", "", `{"key": "value"}`)
	if len(warnings) != 0 {
		t.Fatalf("expected nil for unsupported extension, got: %v", warnings)
	}
}

func TestCheckPathTraversal_EmptyContentReturnsNil(t *testing.T) {
	warnings := checkPathTraversal("/tmp/main.go", "", "")
	if len(warnings) != 0 {
		t.Fatalf("expected nil for empty content, got: %v", warnings)
	}
}

func TestCheckPathTraversal_MaxWarnings(t *testing.T) {
	old := ""
	// Multiple different traversal patterns.
	new := `package main
import (
	"os"
	"path/filepath"
	"net/http"
)
func a(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join("/a", r.URL.Query().Get("f"))
	_ = p
}
func b(w http.ResponseWriter, r *http.Request) {
	_ = os.ReadFile("/b/" + r.FormValue("f"))
}
func c(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, r.FormValue("path"))
}
func d(w http.ResponseWriter, r *http.Request) {
	_ = os.Open("../../etc/passwd")
}`
	warnings := checkPathTraversal("/tmp/main.go", old, new)
	if len(warnings) > 3 {
		t.Fatalf("expected at most 3 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestPtContainsTraversalLiteral(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`os.Open("../../../etc/passwd")`, true},
		{`p := "'../secret"`, true},
		{`path := "/var/log"`, false},
		{`query := "SELECT .. FROM"`, false},
	}
	for _, tc := range cases {
		got := ptContainsTraversalLiteral(tc.line)
		if got != tc.want {
			t.Errorf("ptContainsTraversalLiteral(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestDetectLangExt(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/tmp/main.go", "go"},
		{"/tmp/app.js", "js"},
		{"/tmp/app.ts", "js"},
		{"/tmp/app.tsx", "js"},
		{"/tmp/app.py", "py"},
		{"/tmp/data.json", ""},
	}
	for _, tc := range cases {
		got := detectLangExt(tc.path)
		if got != tc.want {
			t.Errorf("detectLangExt(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// ptContainsStr checks if a substring appears in any element of the slice.
func ptContainsStr(slice []string, substr string) bool {
	for _, s := range slice {
		if ptHasSubstring(s, substr) {
			return true
		}
	}
	return false
}

func ptHasSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
