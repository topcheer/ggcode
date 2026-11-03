package agent

import "testing"

func TestFixAmnesia_HealthyStdlibUsageNotFlagged(t *testing.T) {
	healthy := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if missingImportInContent(healthy) {
		t.Fatal("healthy code with correct import must not be flagged as missing-import")
	}

	healthy2 := "package main\n\nimport (\n\t\"os\"\n\t\"strings\"\n)\n\nfunc f() {\n\t_ = os.Args\n\t_ = strings.TrimSpace(\"x\")\n}\n"
	if missingImportInContent(healthy2) {
		t.Fatal("grouped-import healthy code must not be flagged")
	}
}

func TestFixAmnesia_ActuallyMissingImportFlagged(t *testing.T) {
	broken := "package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n"
	if !missingImportInContent(broken) {
		t.Fatal("fmt used without any import must be flagged")
	}
}

func TestFixAmnesia_UnusedVariableSplitFromMissingImport(t *testing.T) {
	cat, _ := classifyToolError("run_command", "./a.go:12:5: declared and not used: x")
	if cat != "unused-variable" {
		t.Fatalf("declared-and-not-used must classify to unused-variable, got %q", cat)
	}

	d := newFixAmnesiaState()
	d.recordErrorObserved("unused-variable", "/tmp/a.go")
	if got := d.checkContentAgainstFixed("/tmp/a.go", "/tmp/b.go", "x := 1\n_ = x\n"); got != "" {
		t.Fatalf("observation without fix must not warn, got: %s", got)
	}
}

func TestFixAmnesia_ObserveThenFixThenWarn(t *testing.T) {
	d := newFixAmnesiaState()

	cat, _ := classifyToolError("run_command", "./a.go:5:2: undefined: fmt.Stringer in a.go")
	if cat != "missing-import" {
		t.Fatalf("classify: cat=%q", cat)
	}
	d.recordErrorObserved(cat, "./a.go")

	if got := d.checkContentAgainstFixed("./a.go", "./b.go", "package b\nfunc g(){ fmt.Println(1) }\n"); got != "" {
		t.Fatalf("observed-but-not-fixed must not warn, got: %s", got)
	}

	d.recordFileEdited("./a.go")

	if got := d.checkContentAgainstFixed("./a.go", "./b.go", "package b\nfunc g(){ fmt.Println(1) }\n"); got == "" {
		t.Fatal("fixed category + matching different file must warn")
	}
}

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
