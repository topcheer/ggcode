package agent

import (
	"testing"
)

// TestIssue1092_VarConstTypeChange verifies that exported var/const type changes
// are detected by the export guard. Previously, var/const signatures were empty,
// so type changes (e.g., int -> int64) were silently ignored.
func TestIssue1092_VarConstTypeChange(t *testing.T) {
	tests := []struct {
		name     string
		oldSrc   string
		newSrc   string
		wantDiff int // number of signature-changed entries
	}{
		{
			name: "var int to int64",
			oldSrc: `package foo
var X int`,
			newSrc: `package foo
var X int64`,
			wantDiff: 1,
		},
		{
			name: "var port int to string",
			oldSrc: `package foo
var Port int = 8080`,
			newSrc: `package foo
var Port string = "8080"`,
			wantDiff: 1,
		},
		{
			name: "const int to string",
			oldSrc: `package foo
const K int = 42`,
			newSrc: `package foo
const K string = "42"`,
			wantDiff: 1,
		},
		{
			name: "var type unchanged",
			oldSrc: `package foo
var X int`,
			newSrc: `package foo
var X int = 10`,
			wantDiff: 0,
		},
		{
			name: "multiple vars with one type change",
			oldSrc: `package foo
var (
	X int
	Y string
)`,
			newSrc: `package foo
var (
	X int64
	Y string
)`,
			wantDiff: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldSyms := parseExportedSymbolsFromSource([]byte(tt.oldSrc))
			newSyms := parseExportedSymbolsFromSource([]byte(tt.newSrc))
			if oldSyms == nil || newSyms == nil {
				t.Fatalf("parse failed")
			}
			changes := diffExportSymbols(oldSyms, newSyms)
			sigChanges := 0
			for _, c := range changes {
				if c.Kind == "signature-changed" {
					sigChanges++
				}
			}
			if sigChanges != tt.wantDiff {
				t.Errorf("got %d signature changes, want %d. Changes: %v", sigChanges, tt.wantDiff, changes)
			}
		})
	}
}

// TestIssue1092_UnnamedReceiverPtrValChange verifies that methods with unnamed
// receivers detect pointer vs value changes. Previously, the check
// len(recv.List[0].Names) > 0 filtered out unnamed receivers, so
// func (*T) Foo -> func (T) Foo was not detected as a signature change.
func TestIssue1092_UnnamedReceiverPtrValChange(t *testing.T) {
	tests := []struct {
		name     string
		oldSrc   string
		newSrc   string
		wantDiff int
	}{
		{
			name: "ptr to val receiver (both unnamed)",
			oldSrc: `package foo
type T struct{}
func (*T) Foo() {}`,
			newSrc: `package foo
type T struct{}
func (T) Foo() {}`,
			wantDiff: 1,
		},
		{
			name: "val to ptr receiver (both unnamed)",
			oldSrc: `package foo
type T struct{}
func (T) Foo() {}`,
			newSrc: `package foo
type T struct{}
func (*T) Foo() {}`,
			wantDiff: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldSyms := parseExportedSymbolsFromSource([]byte(tt.oldSrc))
			newSyms := parseExportedSymbolsFromSource([]byte(tt.newSrc))
			if oldSyms == nil || newSyms == nil {
				t.Fatalf("parse failed")
			}
			changes := diffExportSymbols(oldSyms, newSyms)
			sigChanges := 0
			for _, c := range changes {
				if c.Kind == "signature-changed" {
					sigChanges++
				}
			}
			if sigChanges != tt.wantDiff {
				t.Errorf("got %d signature changes, want %d. Changes: %v", sigChanges, tt.wantDiff, changes)
			}
		})
	}
}

// TestIssue1092_NamedReceiverNameChangeShouldNotAlert verifies that adding or
// removing a receiver name (without changing ptr/val) should NOT trigger a
// signature change. Previously, the len(recv.List[0].Names) > 0 check made
// named vs unnamed receivers look different.
func TestIssue1092_NamedReceiverNameChangeShouldNotAlert(t *testing.T) {
	tests := []struct {
		name     string
		oldSrc   string
		newSrc   string
		wantDiff int
	}{
		{
			name: "unnamed to named (same ptr)",
			oldSrc: `package foo
type T struct{}
func (*T) Foo() {}`,
			newSrc: `package foo
type T struct{}
func (t *T) Foo() {}`,
			wantDiff: 0,
		},
		{
			name: "named to unnamed (same ptr)",
			oldSrc: `package foo
type T struct{}
func (t *T) Foo() {}`,
			newSrc: `package foo
type T struct{}
func (*T) Foo() {}`,
			wantDiff: 0,
		},
		{
			name: "unnamed to named (same val)",
			oldSrc: `package foo
type T struct{}
func (T) Foo() {}`,
			newSrc: `package foo
type T struct{}
func (t T) Foo() {}`,
			wantDiff: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldSyms := parseExportedSymbolsFromSource([]byte(tt.oldSrc))
			newSyms := parseExportedSymbolsFromSource([]byte(tt.newSrc))
			if oldSyms == nil || newSyms == nil {
				t.Fatalf("parse failed")
			}
			changes := diffExportSymbols(oldSyms, newSyms)
			sigChanges := 0
			for _, c := range changes {
				if c.Kind == "signature-changed" {
					sigChanges++
				}
			}
			if sigChanges != tt.wantDiff {
				t.Errorf("got %d signature changes, want %d. Changes: %v", sigChanges, tt.wantDiff, changes)
			}
		})
	}
}
