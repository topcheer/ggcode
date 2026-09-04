package agent

import (
	"testing"
)

func TestCheckInitSideEffects_Empty(t *testing.T) {
	warns := checkInitSideEffects("test.go", "", "")
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings, got %v", warns)
	}
}

func TestCheckInitSideEffects_NoInit(t *testing.T) {
	src := `package main
func foo() { _ = 1 }
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings, got %v", warns)
	}
}

func TestCheckInitSideEffects_FileIO(t *testing.T) {
	src := `package main
import "os"
func init() {
    data, _ := os.ReadFile("config.json")
    _ = data
}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !contains(warns[0], "os.ReadFile") {
		t.Fatalf("expected os.ReadFile in warning, got: %s", warns[0])
	}
}

func TestCheckInitSideEffects_NetworkCall(t *testing.T) {
	src := `package main
import "net/http"
func init() {
    http.Get("http://example.com/health")
}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !contains(warns[0], "http.Get") || !contains(warns[0], "network I/O") {
		t.Fatalf("expected network warning, got: %s", warns[0])
	}
}

func TestCheckInitSideEffects_Goroutine(t *testing.T) {
	src := `package main
func init() {
    go startServer()
}
func startServer() {}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !contains(warns[0], "goroutine") {
		t.Fatalf("expected goroutine warning, got: %s", warns[0])
	}
}

func TestCheckInitSideEffects_LogFatal(t *testing.T) {
	src := `package main
import "log"
func init() {
    log.Fatal("cannot proceed")
}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !contains(warns[0], "Fatal") || !contains(warns[0], "terminates the process") {
		t.Fatalf("expected Fatal warning, got: %s", warns[0])
	}
}

func TestCheckInitSideEffects_EnvMutation(t *testing.T) {
	src := `package main
import "os"
func init() {
    os.Setenv("KEY", "val")
}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
}

func TestCheckInitSideEffects_BenignInit(t *testing.T) {
	src := `package main
var x = 0
func init() {
    x = 42
}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for benign init, got %v", warns)
	}
}

func TestCheckInitSideEffects_MultipleSideEffects(t *testing.T) {
	src := `package main
import ("os"; "net/http"; "log")
func init() {
    os.ReadFile("a.txt")
    http.Get("http://x.com")
    log.Println("msg")
    go worker()
}
func worker() {}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) < 3 {
		t.Fatalf("expected at least 3 warnings, got %d: %v", len(warns), warns)
	}
}

func TestCheckInitSideEffects_Truncation(t *testing.T) {
	src := `package main
import ("os"; "net/http"; "log"; "fmt")
func init() {
    os.ReadFile("a")
    os.ReadFile("b")
    os.ReadFile("c")
    os.ReadFile("d")
    os.ReadFile("e")
    os.ReadFile("f")
    http.Get("http://x")
    log.Println("x")
    log.Println("y")
    log.Println("z")
}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != maxInitSEWarnings+1 {
		t.Fatalf("expected %d entries (truncated+notice), got %d", maxInitSEWarnings+1, len(warns))
	}
	last := warns[len(warns)-1]
	if !contains(last, "more init()") {
		t.Fatalf("expected truncation notice, got: %s", last)
	}
}

func TestCheckInitSideEffects_NonGoFile(t *testing.T) {
	warns := checkInitSideEffects("test.py", "", `def init(): pass`)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for .py file, got %v", warns)
	}
}

func TestCheckInitSideEffects_SyncMutex(t *testing.T) {
	// sync operations are not flagged - they're safe
	src := `package main
import "sync"
var mu sync.Mutex
func init() {
    mu.Lock()
    mu.Unlock()
}
`
	warns := checkInitSideEffects("test.go", "", src)
	if len(warns) != 0 {
		t.Fatalf("expected 0 warnings for sync ops, got %v", warns)
	}
}

// TestInitSE_1577 pins #1577: time.Now/http.NewServeMux pure constructors
// stay silent, time.Sleep still flags, bare panic() is caught.
func TestInitSE_1577(t *testing.T) {
	cases := []struct {
		name, src string
		want      int
	}{
		{"pure time.Now", "package p\nfunc init() { _ = time.Now() }\n", 0},
		{"pure http ctor", "package p\nfunc init() { _ = http.NewServeMux() }\n", 0},
		{"time.Sleep flags", "package p\nfunc init() { time.Sleep(time.Second) }\n", 1},
		{"http.Get flags", "package p\nfunc init() { _, _ = http.Get(\"x\") }\n", 1},
		{"bare panic caught", "package p\nfunc init() { panic(\"no config\") }\n", 1},
	}
	for _, c := range cases {
		got := len(checkInitSideEffects("t.go", "", c.src))
		if got != c.want {
			t.Errorf("%s: got %d warnings, want %d", c.name, got, c.want)
		}
	}
}
