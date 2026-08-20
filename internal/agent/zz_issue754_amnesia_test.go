package agent

import "testing"

// Regression guards for #754.

// Defect 1: missing-import contentPattern must NOT match healthy code that
// merely uses stdlib symbols with correct imports (old pattern hit ~100% of
// healthy Go files).
func TestFixAmnesia_HealthyStdlibUsageNotFlagged(t *testing.T) {
	healthy := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if missingImportInContent(healthy) {
		t.Fatal("healthy code with correct import must not be flagged as missing-import")
	}
	// Grouped import block form.
	healthy2 := "package main\n\nimport (\n\t\"os\"\n\t\"strings\"\n)\n\nfunc f() {\n\t_ = os.Args\n\t_ = strings.TrimSpace(\"x\")\n}\n"
	if missingImportInContent(healthy2) {
		t.Fatal("grouped-import healthy code must not be flagged")
	}
}

// Defect 1 (positive side): actually missing import must still be detected.
func TestFixAmnesia_ActuallyMissingImportFlagged(t *testing.T) {
	broken := "package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if !missingImportInContent(broken) {
		t.Fatal("fmt used without any import must be flagged")
	}
}

// Defect 2: "declared and not used" must classify to its own category that
// has NO content pattern (using variables in new files is healthy code).
func TestFixAmnesia_UnusedVariableSplitFromMissingImport(t *testing.T) {
	cat, _ := classifyToolError("run_command", "./a.go:12:5: declared and not used: x")
	if cat != "unused-variable" {
		t.Fatalf("declared-and-not-used must classify to unused-variable, got %q", cat)
	}
	// Observation seeding must NOT directly arm cross-file warnings.
	d := newFixAmnesiaState()
	d.recordErrorObserved("unused-variable", "/tmp/a.go")
	if got := d.checkContentAgainstFixed("/tmp/a.go", "/tmp/b.go", "x := 1\n_ = x\n"); got != "" {
		t.Fatalf("observation without fix must not warn, got: %s", got)
	}
}

// Defect 3: observe->fix two-phase wiring. Only a successful edit of the
// erroring file promotes the category to FIXED; then a different file whose
// content matches triggers the warning.
func TestFixAmnesia_ObserveThenFixThenWarn(t *testing.T) {
	d := newFixAmnesiaState()
	// Observe missing-import error in a.go (undefined stdlib symbol).
	cat, _ := classifyToolError("run_command", "./a.go:5:2: undefined: fmt.Stringer in a.go")
	if cat != "missing-import" {
		t.Fatalf("classify: cat=%q", cat)
	}
	d.recordErrorObserved(cat, "./a.go")
	// No edit yet -> healthy b.go must not warn even if it uses fmt without import? Actually b.go DOES miss import:
	// but category is only OBSERVED, not FIXED.
	if got := d.checkContentAgainstFixed("./a.go", "./b.go", "package b\nfunc g(){ fmt.Println(1) }\n"); got != "" {
		t.Fatalf("observed-but-not-fixed must not warn, got: %s", got)
	}
	// Successful edit of a.go promotes to fixed.
	d.recordFileEdited("./a.go")
	// Now b.go with genuinely missing import warns.
	if got := d.checkContentAgainstFixed("./a.go", "./b.go", "package b\nfunc g(){ fmt.Println(1) }\n"); got == "" {
		t.Fatal("fixed category + matching different file must warn")
	}
}

// Nil-deref pattern still functional end-to-end (issue's second FP source
// was that ANY err!=nil block + method call matched; the pattern is
// unchanged but seeding path now requires nil-pointer text, not panic text).
func TestFixAmnesia_NilDerefSeedingRequiresNilPointerText(t *testing.T) {
	cat, _ := classifyToolError("run_command", "panic: runtime error: invalid memory address")
	if cat != "" {
		t.Fatalf("generic panic must not seed nil-deref, got %q", cat)
	}
	cat2, _ := classifyToolError("run_command", "./a.go:10:3: nil pointer dereference")
	if cat2 != "nil-deref-after-nil-check" {
		t.Fatalf("explicit nil pointer text must seed, got %q", cat2)
	}
}
