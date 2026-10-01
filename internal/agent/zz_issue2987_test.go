package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// zz_issue2987_test.go - regression probes for #2987: the #1527 case A
// receiver exemption only matched type names ending in "Server", but the FP
// mechanism is SHAPE, not the name - any worker method holding the wg on the
// struct (`p.wg.Done()`) has its Add() in the spawner, invisible to
// function-granularity analysis. *Pool/*Manager/*Worker (the most common Go
// worker names) all got the misleading "Done() panics, call Add first"
// advice that #1527 proved agents follow into a Wait deadlock.

const issue2987PoolSrc = `package pool

import "sync"

type Pool struct{ wg sync.WaitGroup }

func (p *Pool) Run() {
	for i := 0; i < 3; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	p.wg.Wait()
}

func (p *Pool) worker() {
	defer p.wg.Done()
	_ = 1
}
`

func TestIssue2987NonServerReceiverWorkerNotFlagged(t *testing.T) {
	for _, iss := range checkWaitGroupMisuse("pool.go", "", issue2987PoolSrc) {
		if strings.Contains(iss, "Done() is called but Add() is never called") {
			t.Fatalf("#2987: *Pool receiver worker (spawner Adds) must be exempt, got: %s", iss)
		}
	}
}

func TestIssue2987LocalWGVariableStillChecked(t *testing.T) {
	// A method using a LOCAL wg variable (not the receiver field) with
	// Done-but-no-Add stays a true positive: the field scan finds nothing
	// and the general path applies.
	src := `package p
import "sync"
func (m *Manager) bad() {
	var wg sync.WaitGroup
	wg.Done()
	_ = m
}
`
	found := false
	for _, iss := range checkWaitGroupMisuse("m.go", "", src) {
		if strings.Contains(iss, "Done() is called but Add() is never called") {
			found = true
		}
	}
	if !found {
		t.Fatalf("#2987: local-var Done-without-Add must stay flagged")
	}
}

func TestIssue2987ReceiverFieldHeuristicUnit(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", issue2987PoolSrc, 0)
	if err != nil {
		t.Fatal(err)
	}
	var worker *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "worker" {
			worker = fd
		}
	}
	if worker == nil {
		t.Fatal("worker method not found")
	}
	if !recvFieldWGDone(worker) {
		t.Fatalf("#2987: recvFieldWGDone must match p.wg.Done() form")
	}
}
