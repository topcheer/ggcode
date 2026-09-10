package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #1527 case A pin: the struct-field wg receiver shape must be exempt.
func Test1527ReceiverWGShapeExempt(t *testing.T) {
	src := `package p
import "sync"
type Server struct{ wg sync.WaitGroup }
func (s *Server) Start() { s.wg.Add(1); go s.worker() }
func (s *Server) worker() { defer s.wg.Done(); _ = 1 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "t.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, iss := range analyzeWGFunc(fn) {
			if containsSub1527(iss.pattern, "Add() is never called") {
				t.Fatalf("receiver wg shape (spawner Adds) must be exempt, got: %s", iss.pattern)
			}
		}
	}
}

// #1527 case B pin: description must not be scanned for read-only git tokens.
func Test1527DescriptionNotScanned(t *testing.T) {
	args := `{"description":"检查 git log 后执行 reset","commit":null}`
	if isReadOnlyGitInvocation("git_reset", args) {
		t.Fatal("git_reset with log in DESCRIPTION must still be mutating")
	}
}

// #1527 case C pin: mutating git via run_command must classify.
func Test1527RunCommandGitMutates(t *testing.T) {
	if !runCommandMutatesTree(`{"command":"git checkout main"}`) {
		t.Fatal("git checkout via run_command must mutate")
	}
	if runCommandMutatesTree(`{"command":"go build ./..."}`) {
		t.Fatal("non-git run_command must not classify")
	}
	if runCommandMutatesTree(`{"command":"git status"}`) {
		t.Fatal("read-only git via run_command must not classify")
	}
}

// #1527 case D pin: line-number drift must not break the pre-existing gate.
func Test1527DeltaLineDrift(t *testing.T) {
	if normalizeIntegrityMsg("line 10: unterminated string") != normalizeIntegrityMsg("line 15: unterminated string") {
		t.Fatal("same problem at drifted line must normalize equal (insertions above must not count as introduced)")
	}
	if normalizeIntegrityMsg("line 10: unterminated string") == normalizeIntegrityMsg("line 10: missing brace") {
		t.Fatal("different problems must stay different")
	}
}

func containsSub1527(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
