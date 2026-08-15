package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Assembly-layer regression tests for #328/#330: these tests MUST go through
// checkWriteIntegrity (the production pipeline entry), NOT the detector
// functions directly. Tests that call detectors directly bypass the registry
// and stay green even when a check is unregistered (the exact failure mode
// fixed in #328/#330).

func TestAssemblyDeprecatedAPI(t *testing.T) {
	old := `package main

import "os"

func read(p string) ([]byte, error) { return os.ReadFile(p) }
`
	new := `package main

import "io/ioutil"

func read(p string) ([]byte, error) { return ioutil.ReadFile(p) }
`
	w := checkWriteIntegrity("x.go", old, new)
	if w == "" {
		t.Fatal("expected deprecated-api warning via checkWriteIntegrity, got none")
	}
	if !strings.Contains(w, "ioutil.ReadFile") && !strings.Contains(w, "deprecated") {
		t.Fatalf("unexpected warning content: %q", w)
	}
}

func TestAssemblySuspiciousComparison(t *testing.T) {
	new := `package main

import (
	"database/sql"
	"errors"
)

func find(db *sql.DB) error {
	var n int
	row := db.QueryRow("select 1")
	if err := row.Scan(&n); err == sql.ErrNoRows {
		return errors.New("none")
	}
	return nil
}
`
	w := checkWriteIntegrity("x.go", "", new)
	if w == "" {
		t.Fatal("expected suspicious-comparison warning via checkWriteIntegrity, got none")
	}
}

func TestAssemblyPrintfFormat(t *testing.T) {
	new := `package main

import "fmt"

func report(n int) {
	fmt.Println(fmt.Sprintf("processed %d items", n))
}
`
	w := checkWriteIntegrity("x.go", "", new)
	if w == "" {
		t.Fatal("expected printf-format warning via checkWriteIntegrity, got none")
	}
}

func TestAssemblyDependencyManifests(t *testing.T) {
	// go.mod: known-vulnerable golang.org/x/crypto version
	new := "module example.com/m\n\ngo 1.21\n\nrequire golang.org/x/crypto v0.16.0\n"
	w := checkWriteIntegrity("go.mod", "", new)
	if w == "" {
		t.Fatal("expected dependency-vuln warning via checkWriteIntegrity, got none")
	}
}

func TestRegistryContainsReRegisteredChecks(t *testing.T) {
	registerAllChecks()
	want := []string{
		"deprecated-api", "interface-compliance",
		"printf-format", "suspicious-comparison",
		"dep-major-bump", "dependency-vuln",
	}
	got := map[string]bool{}
	for _, c := range allChecks {
		got[c.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("check %q missing from registry", name)
		}
	}
}

// #330 gap: interface-compliance and dep-major-bump assembly coverage
// (verified live via production path; codified here for regression).

func TestAssemblyInterfaceCompliance(t *testing.T) {
	dir := t.TempDir()
	// Type in the same package partially implementing the interface.
	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(
		"package smoke\n\ntype Handler struct{}\n\nfunc (h *Handler) Foo() error { return nil }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	old := "package smoke\n\ntype Handler interface {\n\tFoo() error\n}\n"
	new := "package smoke\n\ntype Handler interface {\n\tFoo() error\n\tBar() error\n}\n"
	w := checkWriteIntegrity(filepath.Join(dir, "iface.go"), old, new)
	if w == "" {
		t.Fatal("expected interface-compliance warning via checkWriteIntegrity, got none")
	}
}

func TestAssemblyDepMajorBump(t *testing.T) {
	old := "{\n  \"dependencies\": {\n    \"react\": \"^18.2.0\"\n  }\n}\n"
	new := "{\n  \"dependencies\": {\n    \"react\": \"^19.0.0\"\n  }\n}\n"
	w := checkWriteIntegrity("package.json", old, new)
	if w == "" {
		t.Fatal("expected dep-major-bump warning via checkWriteIntegrity, got none")
	}
	if !strings.Contains(w, "react") {
		t.Fatalf("unexpected warning content: %q", w)
	}
}
