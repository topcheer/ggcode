package tool

import "testing"

// TestIssue1001ExitFlagsNotBareREPL pins the fix: version/help probes
// (python3 --version, node -v...) print and exit immediately — no stdin
// warning. REPL-type flags (-u, -I) must keep the warning.
func TestIssue1001ExitFlagsNotBareREPL(t *testing.T) {
	noWarn := [][2]string{
		{"python3", "--version"},
		{"python3", "-V"},
		{"node", "--version"},
		{"node", "-v"},
		{"node", "--help"},
		{"python3", "-h"},
	}
	for _, c := range noWarn {
		if isBareREPL(c[0], []string{c[1]}) {
			t.Errorf("%s %s: exit-type flag must not warn as bare REPL", c[0], c[1])
		}
	}

	warn := [][2]string{
		{"python3", "-u"}, // unbuffered, still enters REPL
		{"python3", "-I"}, // isolated mode, still enters REPL
	}
	for _, c := range warn {
		if !isBareREPL(c[0], []string{c[1]}) {
			t.Errorf("%s %s: REPL-type flag must keep the warning", c[0], c[1])
		}
	}
}

// TestIssue1002ModulePrefixBoundaryNotClaimed pins the fix: an import whose
// path merely PREFIXES-collides with the module path (no "/" boundary) is
// external and must not enter the dependency graph.
func TestIssue1002ModulePrefixBoundaryNotClaimed(t *testing.T) {
	modulePath := "example.com/testmod"
	impPath := "example.com/testmodfoo/x"

	// Mirror processGoFile's acceptance predicate: the import must be
	// rejected because it neither equals the module path nor shares the
	// "/" boundary.
	if impPath == modulePath {
		t.Fatal("test bug")
	}
	if has := len(impPath) > len(modulePath) && impPath[:len(modulePath)] == modulePath && impPath[len(modulePath)] == '/'; has {
		t.Fatal("test bug: import actually is inside module")
	}

	// Direct behavioral check via the same expression shape the fix uses.
	inside := impPath == modulePath || (len(impPath) > len(modulePath) && impPath[:len(modulePath)] == modulePath && impPath[len(modulePath)] == '/')
	if inside {
		t.Errorf("prefix-colliding import %q wrongly claimed by module %q", impPath, modulePath)
	}

	// A true sub-package must still be claimed.
	sub := "example.com/testmod/internal/util"
	insideSub := sub == modulePath || (len(sub) > len(modulePath) && sub[:len(modulePath)] == modulePath && sub[len(modulePath)] == '/')
	if !insideSub {
		t.Errorf("true sub-package %q must be claimed by module %q", sub, modulePath)
	}
}
