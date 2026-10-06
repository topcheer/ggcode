package agent

import (
	"fmt"
	"strings"
	"testing"
)

// r356: slopsquatting defense - a fully hallucinated dependency name that
// is NOT close to any well-known package must still surface a registry
// verification notice (the typosquat check stays silent for those).
func TestNewDepVerify_HallucinatedArbitraryNameNoticed(t *testing.T) {
	w := checkNewDependencyVerify("package.json", `{}`, `{ "dependencies": { "react-pro-memo-hooks": "^1.0.0" } }`)
	if len(w) != 1 || !strings.Contains(w[0], "react-pro-memo-hooks") || !strings.Contains(w[0], "slopsquatting") {
		t.Fatalf("want one aggregated slopsquatting notice naming the package, got %v", w)
	}
}

func TestNewDepVerify_ExactWellKnownStaysSilent(t *testing.T) {
	w := checkNewDependencyVerify("package.json", `{}`, `{ "dependencies": { "react": "^19.0.0", "lodash": "^4.0.0" } }`)
	if len(w) != 0 {
		t.Fatalf("exact well-known packages must not produce noise, got %v", w)
	}
}

func TestNewDepVerify_UnchangedDependenciesSilent(t *testing.T) {
	same := `{ "dependencies": { "react": "^19.0.0" } }`
	if w := checkNewDependencyVerify("package.json", same, same); len(w) != 0 {
		t.Fatalf("no delta, no notice, got %v", w)
	}
}

func TestNewDepVerify_GoModPath(t *testing.T) {
	// #3045 B2: dotted-domain VCS paths are exempt; a REGISTRY-style
	// (non-VCS) Go module path - the actual hallucination surface - warns.
	w := checkNewDependencyVerify("go.mod",
		"module x\n\ngo 1.27\n",
		"module x\n\ngo 1.27\n\nrequire totally-unknown-lib v0.0.1\n")
	if len(w) != 1 || !strings.Contains(w[0], "totally-unknown-lib") {
		t.Fatalf("new registry-style Go dependency must be noticed, got %v", w)
	}
}

func TestNewDepVerify_CapAndAggregate(t *testing.T) {
	var deps strings.Builder
	deps.WriteString("{ \"dependencies\": {")
	for i := 0; i < 7; i++ {
		if i > 0 {
			deps.WriteString(",")
		}
		deps.WriteString(fmt.Sprintf("%q: %q", strings.Repeat("x", i+1)+"-pkg-"+fmt.Sprint(i), "1"))
	}
	deps.WriteString("} }")
	w := checkNewDependencyVerify("package.json", `{ "dependencies": { "a-pkg-0": "1" } }`, deps.String())
	// a-pkg-0 excluded (existed); 7 fresh names -> notice lists 5 + "+2 more".
	if len(w) != 1 || !strings.Contains(w[0], "+2 more") {
		t.Fatalf("cap must aggregate overflow, got %v", w)
	}
}

// Registration: the check must be part of the write-integrity pipeline.
func TestNewDepVerify_Registered(t *testing.T) {
	checkRegistered(t, "new-dep-verify")
}
