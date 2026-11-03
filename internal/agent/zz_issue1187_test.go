package agent

import (
	"strings"
	"testing"
)

const pcProdSrc = `package p

func ProbeConnection(host, port, scheme, user, pass, extra string) error { return nil }

func TestConnection(host, port, scheme, user, pass, extra string) error { return nil }
`

func TestParamCountProdTestPrefixFuncFlagged(t *testing.T) {
	warns := checkExcessiveParams("healthcheck.go", "", pcProdSrc)
	if len(warns) == 0 {
		t.Fatal("Test-prefixed function in production code must be flagged (6 params)")
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "TestConnection") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings must name TestConnection, got: %v", warns)
	}
}

func TestParamCountTestFileExemptionKept(t *testing.T) {
	src := `package p

import "testing"

func TestFoo(a, b, c, d, e, f string) {
	_ = a
}
`
	if warns := checkExcessiveParams("p_test.go", "", src); len(warns) != 0 {
		t.Fatalf("test-file Test function must stay exempt, got: %v", warns)
	}
}

func TestParamCountBenchProdFlagged(t *testing.T) {
	src := `package p

func BenchmarkLoad(a, b, c, d, e, f string) {
	_ = a
}
`
	if warns := checkExcessiveParams("load.go", "", src); len(warns) == 0 {
		t.Fatal("Benchmark-prefixed production function must be flagged")
	}
	if warns := checkExcessiveParams("load_test.go", "", src); len(warns) != 0 {
		t.Fatal("Benchmark-prefixed function in _test.go must stay exempt")
	}
}

func TestParamCountProdStillChecked(t *testing.T) {
	if warns := checkExcessiveParams("p.go", "", pcProdSrc); len(warns) < 2 {
		t.Fatalf("both 6-param production functions must be flagged, got %d warnings", len(warns))
	}
}
