package agent

import (
	"strings"
	"testing"
)

func TestCheckHardcodedHost_GoListenAndServe(t *testing.T) {
	src := `package main
import "net/http"
func main() {
	http.ListenAndServe(":8080", nil)
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "hardcoded bind address") {
		t.Errorf("unexpected message: %s", w[0])
	}
}

func TestCheckHardcodedHost_GoListenAndServeTLS(t *testing.T) {
	src := `package main
import "net/http"
func main() {
	http.ListenAndServeTLS(":8443", "cert.pem", "key.pem", nil)
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for TLS, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_GoNetListen(t *testing.T) {
	src := `package main
import "net"
func main() {
	ln, _ := net.Listen("tcp", ":3000")
	_ = ln
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for net.Listen, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_GoMethodListenAndServe(t *testing.T) {
	src := `package main
import "net/http"
type Server struct{}
func (s *Server) Run() {
	s.server.ListenAndServe(":9090")
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for method call, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_GoEnvVarNoWarning(t *testing.T) {
	src := `package main
import (
	"net/http"
	"os"
)
func main() {
	addr := os.Getenv("ADDR")
	http.ListenAndServe(addr, nil)
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != 0 {
		t.Fatalf("expected 0 warnings for env var, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_GoNonDevPortNoWarning(t *testing.T) {
	src := `package main
import "net/http"
func main() {
	http.ListenAndServe(":1234", nil)
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != 0 {
		t.Fatalf("expected 0 warnings for non-dev port, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_GoWildcardBind(t *testing.T) {
	src := `package main
import "net/http"
func main() {
	http.ListenAndServe("0.0.0.0:8080", nil)
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for 0.0.0.0 bind, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_DeltaAware(t *testing.T) {
	oldSrc := `package main
import "net/http"
func main() {
	http.ListenAndServe(":8080", nil)
}`
	newSrc := oldSrc + "\nfunc extra() { http.ListenAndServe(\":3000\", nil) }"
	w := checkHardcodedHost("server.go", oldSrc, newSrc)
	if len(w) != 1 {
		t.Fatalf("expected 1 delta warning, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "3000") {
		t.Errorf("expected warning for new :3000, got: %s", w[0])
	}
}

func TestCheckHardcodedHost_JSTSListen(t *testing.T) {
	src := `const express = require('express');
const app = express();
app.listen(3000);`
	w := checkHardcodedHost("server.js", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 JS warning, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "process.env.PORT") {
		t.Errorf("unexpected message: %s", w[0])
	}
}

func TestCheckHardcodedHost_JSTSHost(t *testing.T) {
	src := `const config = { host: '0.0.0.0', port: 8080 };`
	w := checkHardcodedHost("config.ts", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for hardcoded host, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_JSTSEnvVarNoWarning(t *testing.T) {
	src := `const port = process.env.PORT || 3000;
app.listen(port);`
	w := checkHardcodedHost("server.ts", "", src)
	if len(w) != 0 {
		t.Fatalf("expected 0 warnings for env var, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_PythonFlask(t *testing.T) {
	src := `from flask import Flask
app = Flask(__name__)
app.run(host='0.0.0.0', port=5000)`
	w := checkHardcodedHost("app.py", "", src)
	if len(w) < 1 {
		t.Fatalf("expected at least 1 Python warning, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_PythonPortVar(t *testing.T) {
	src := `PORT = 8080`
	w := checkHardcodedHost("config.py", "", src)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for PORT = 8080, got %d: %v", len(w), w)
	}
}

func TestCheckHardcodedHost_EmptyFile(t *testing.T) {
	w := checkHardcodedHost("server.go", "", "")
	if len(w) != 0 {
		t.Fatalf("expected 0 warnings for empty file, got %d", len(w))
	}
}

func TestCheckHardcodedHost_UnsupportedExt(t *testing.T) {
	src := "http.ListenAndServe(':8080', nil)"
	w := checkHardcodedHost("server.txt", "", src)
	if len(w) != 0 {
		t.Fatalf("expected 0 warnings for .txt, got %d", len(w))
	}
}

func TestCheckHardcodedHost_MaxWarnings(t *testing.T) {
	src := `package main
import (
	"net/http"
	"net"
)
func main() {
	http.ListenAndServe(":8080", nil)
	http.ListenAndServe(":3000", nil)
	http.ListenAndServe(":5000", nil)
	http.ListenAndServe(":9090", nil)
	http.ListenAndServe(":8081", nil)
	ln, _ := net.Listen("tcp", ":8888")
	_ = ln
}`
	w := checkHardcodedHost("server.go", "", src)
	if len(w) != hardcodedHostMaxWarnings+1 {
		t.Fatalf("expected %d warnings (cap + truncation notice), got %d: %v",
			hardcodedHostMaxWarnings+1, len(w), w)
	}
	if !strings.Contains(w[len(w)-1], "more") {
		t.Errorf("expected truncation notice as last element, got: %s", w[len(w)-1])
	}
}

func TestExtractPort(t *testing.T) {
	cases := []struct {
		addr string
		port string
	}{
		{":8080", "8080"},
		{"localhost:3000", "3000"},
		{"0.0.0.0:5000", "5000"},
		{"[::]:8080", "8080"},
		{"noport", ""},
	}
	for _, tc := range cases {
		if got := extractPort(tc.addr); got != tc.port {
			t.Errorf("extractPort(%q) = %q, want %q", tc.addr, got, tc.port)
		}
	}
}

func TestIsHardcodedAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{":8080", true},
		{"0.0.0.0", true},
		{"0.0.0.0:3000", true},
		{"localhost:5000", true},
		{":1234", false},
		{"", false},
		{"example.com", false},
	}
	for _, tc := range cases {
		if got := isHardcodedAddr(tc.addr); got != tc.want {
			t.Errorf("isHardcodedAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
