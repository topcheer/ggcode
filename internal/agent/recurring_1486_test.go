package agent

import "testing"

func Test1486PathPrefixKeepsSiblingPackages(t *testing.T) {
	a := stripPathToBasename("a/util.go:42")
	b := stripPathToBasename("b/util.go:42")
	if a == b {
		t.Fatalf("a/util.go and b/util.go must fingerprint apart, got %q both", a)
	}
	if stripPathToBasename("./internal/agent/foo.go:1") != "internal/agent/foo.go:1" {
		t.Fatalf("root-marker strip: %q", stripPathToBasename("./internal/agent/foo.go:1"))
	}

	if stripPathToBasename("./internal/agent/foo.go:1") != stripPathToBasename("internal/agent/foo.go:1") {
		t.Fatal("same package with/without ./ must fingerprint equal")
	}

	if stripPathToBasename("../pkg/x.go:3") != "pkg/x.go:3" {
		t.Fatalf("../ strip: %q", stripPathToBasename("../pkg/x.go:3"))
	}

	if stripPathToBasename("main.go:7") != "main.go:7" {
		t.Fatalf("bare basename must be untouched, got %q", stripPathToBasename("main.go:7"))
	}
}

func Test1486EditGateSemantics(t *testing.T) {
	args := `{"command":"go test ./..."}`

	s := newRedundantReverifyState()
	s.recordToolCall("run_command", args, 1, false)
	if h := s.recordToolCall("run_command", args, 2, false); h == "" {
		t.Fatal("identical re-verify with no landed edit must still warn")
	}

	s2 := newRedundantReverifyState()
	s2.recordToolCall("run_command", args, 1, false)
	s2.recordEdit("edit_file")
	if h := s2.recordToolCall("run_command", args, 2, false); h != "" {
		t.Fatal("re-verify after a landed edit must not warn")
	}
}
