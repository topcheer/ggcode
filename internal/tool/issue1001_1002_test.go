package tool

import "testing"

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
		{"python3", "-u"},
		{"python3", "-I"},
	}
	for _, c := range warn {
		if !isBareREPL(c[0], []string{c[1]}) {
			t.Errorf("%s %s: REPL-type flag must keep the warning", c[0], c[1])
		}
	}
}

func TestIssue1002ModulePrefixBoundaryNotClaimed(t *testing.T) {
	modulePath := "example.com/testmod"
	impPath := "example.com/testmodfoo/x"

	if impPath == modulePath {
		t.Fatal("test bug")
	}
	if has := len(impPath) > len(modulePath) && impPath[:len(modulePath)] == modulePath && impPath[len(modulePath)] == '/'; has {
		t.Fatal("test bug: import actually is inside module")
	}

	inside := impPath == modulePath || (len(impPath) > len(modulePath) && impPath[:len(modulePath)] == modulePath && impPath[len(modulePath)] == '/')
	if inside {
		t.Errorf("prefix-colliding import %q wrongly claimed by module %q", impPath, modulePath)
	}

	sub := "example.com/testmod/internal/util"
	insideSub := sub == modulePath || (len(sub) > len(modulePath) && sub[:len(modulePath)] == modulePath && sub[len(modulePath)] == '/')
	if !insideSub {
		t.Errorf("true sub-package %q must be claimed by module %q", sub, modulePath)
	}
}
