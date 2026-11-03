package agent

import (
	"strings"
	"testing"
)

const ccPreexistingSrc = `package main

import "fmt"

func main() {
	if false {
		fmt.Println("debug block kept on purpose")
	}
	fmt.Println("real work")
}
`

func TestConstantConditional_DeltaGate_PreexistingNotReReported(t *testing.T) {

	edited := strings.Replace(ccPreexistingSrc, `"real work"`, `"real work v2"`, 1)
	got := checkConstantConditional("main.go", ccPreexistingSrc, edited)
	if len(got) != 0 {
		t.Fatalf("pre-existing if-false must not be re-reported on unrelated edit, got %v", got)
	}
}

func TestConstantConditional_DeltaGate_ZeroDeltaSkipped(t *testing.T) {
	got := checkConstantConditional("main.go", ccPreexistingSrc, ccPreexistingSrc)
	if len(got) != 0 {
		t.Fatalf("zero-delta write must be skipped entirely, got %v", got)
	}
}

func TestConstantConditional_DeltaGate_NewInstanceOfSameCondSurfaces(t *testing.T) {

	edited := strings.Replace(ccPreexistingSrc, "func main() {", "func main() {\n\tif false {\n\t\tprintln(\"another debug\")\n\t}", 1)
	got := checkConstantConditional("main.go", ccPreexistingSrc, edited)
	if len(got) != 1 {
		t.Fatalf("exactly the newly-added if-false must be reported, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "always-false") {
		t.Fatalf("expected always-false warning, got %q", got[0])
	}
}

func TestConstantConditional_DeltaGate_NewDifferentCondSurfaces(t *testing.T) {

	edited := strings.Replace(ccPreexistingSrc, "func main() {", "func main() {\n\tif true {\n\t\tprintln(\"always runs\")\n\t}", 1)
	got := checkConstantConditional("main.go", ccPreexistingSrc, edited)
	if len(got) != 1 {
		t.Fatalf("only the new if-true must be reported, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "always-true") {
		t.Fatalf("expected always-true warning, got %q", got[0])
	}
}

func TestConstantConditional_DeltaGate_LineShiftNotReReported(t *testing.T) {

	edited := strings.Replace(ccPreexistingSrc, "func main() {",
		"func main() {\n\t// added comment line 1\n\t// added comment line 2\n\t// added comment line 3", 1)
	got := checkConstantConditional("main.go", ccPreexistingSrc, edited)
	if len(got) != 0 {
		t.Fatalf("line-shifted pre-existing condition must not be re-reported, got %v", got)
	}
}

func TestConstantConditional_DeltaGate_RemovalClearsWarnings(t *testing.T) {

	edited := strings.Replace(ccPreexistingSrc, "	if false {\n\t\tfmt.Println(\"debug block kept on purpose\")\n\t}\n", "", 1)
	got := checkConstantConditional("main.go", ccPreexistingSrc, edited)
	if len(got) != 0 {
		t.Fatalf("removal must not warn, got %v", got)
	}
}
