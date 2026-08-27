package agent

// Regression tests for issue #1142:
//   1. Delta comparison used fset.Position().String() ("line:column") as the
//      old/new key - inserting a comment line above an existing function
//      shifted it and re-reported it as newly introduced.
//   2. checkExcessiveParams was never registered in the write integrity
//      registry, so the detector was dead code.

import (
	"go/token"
	"testing"
)

func TestParamCountDelta_CommentInsertionAboveShiftsLines(t *testing.T) {
	// Issue #1142 repro: old file has a 6-param function; new content inserts
	// one comment line above it. Fingerprint-based delta must yield 0 warnings.
	oldContent := `package main

// existing header
func complex(a, b, c int, d, e string, f bool) {}
`
	newContent := `package main

// NEW comment inserted above - shifts the function down by one line

// existing header
func complex(a, b, c int, d, e string, f bool) {}
`
	warnings := checkExcessiveParams("test.go", oldContent, newContent)
	if len(warnings) != 0 {
		t.Errorf("line-shifted pre-existing function must not re-warn, got %d: %v", len(warnings), warnings)
	}
}

func TestParamCountDelta_LineDeletionAboveShiftsLines(t *testing.T) {
	// Symmetric case: removing lines above also shifts positions upward.
	oldContent := `package main

func helper() {}

// stale comment to delete below

func complex(a, b, c int, d, e string, f bool) {}
`
	newContent := `package main

func helper() {}

func complex(a, b, c int, d, e string, f bool) {}
`
	warnings := checkExcessiveParams("test.go", oldContent, newContent)
	if len(warnings) != 0 {
		t.Errorf("upward-shifted pre-existing function must not re-warn, got %d: %v", len(warnings), warnings)
	}
}

func TestParamCountDelta_ParamChangeStillWarns(t *testing.T) {
	// The fingerprint is content-based: adding a parameter to the same
	// function is a genuine change and must still warn.
	oldContent := `package main
func complex(a, b, c int, d, e string) {}
`
	newContent := `package main
func complex(a, b, c int, d, e string, f bool) {}
`
	warnings := checkExcessiveParams("test.go", oldContent, newContent)
	if len(warnings) == 0 {
		t.Error("param count change on the same function should warn even without line movement")
	}
}

func TestPcFingerprint_PositionIndependent(t *testing.T) {
	a := paramCountInstance{funcName: "f", pos: token.Position{Line: 3}, count: 6,
		params: []string{"a", "b", "c", "d", "e", "f"}}
	b := paramCountInstance{funcName: "f", pos: token.Position{Line: 9}, count: 6,
		params: []string{"a", "b", "c", "d", "e", "f"}}
	if a.pcFingerprint() != b.pcFingerprint() {
		t.Errorf("same content at different positions must share fingerprint: %q vs %q",
			a.pcFingerprint(), b.pcFingerprint())
	}
	c := a
	c.count = 7
	if a.pcFingerprint() == c.pcFingerprint() {
		t.Error("different param counts must produce different fingerprints")
	}
	d := paramCountInstance{funcName: "f", pos: a.pos, count: 6,
		params: []string{"x", "b", "c", "d", "e", "f"}}
	if a.pcFingerprint() == d.pcFingerprint() {
		t.Error("different params must produce different fingerprints")
	}
}

func TestParamCountRegisteredInWriteIntegrityRegistry(t *testing.T) {
	registerAllChecks()
	found := false
	for _, chk := range allChecks {
		if chk.Name == "param-count" {
			found = true
			goLang := false
			for _, l := range chk.Langs {
				if l == LangGo {
					goLang = true
				}
			}
			if !goLang {
				t.Error("param-count entry must be restricted to LangGo")
			}
			break
		}
	}
	if !found {
		t.Fatal("param-count detector is not registered in the write integrity registry (#1142 dead-code defect)")
	}
}
