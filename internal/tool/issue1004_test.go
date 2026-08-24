package tool

import "testing"

// TestIssue1004ByteExactTransformsByteRecount pins the fix: crlf-converted
// and line-numbers-stripped transforms recount occurrences under their own
// byte-exact semantics (indent differences are significant), so a genuinely
// unique edit is accepted and a true collision is still rejected.
func TestIssue1004ByteExactTransformsByteRecount(t *testing.T) {
	// Scenario A: CRLF file; first block single-indent, second block
	// double-indent. LF-only old_text byte-matches ONLY the first block
	// after CRLF conversion. Old TrimSpace recount folded the indent
	// difference and reported 2 (false ambiguity, #1004).
	contentA := "if x {\r\n\tinside()\r\n}\r\n\r\nif y {\r\n\t\tinside()\r\n}\r\n"
	oldA := "if x {\n\tinside()\n}"
	if loose, _ := lenientRecountRaw(contentA, oldA, "crlf-converted"); loose != 1 {
		t.Errorf("crlf-converted: unique under byte semantics, want 1, got %d", loose)
	}

	// True collision under CRLF semantics must still report 2.
	contentB := "if x {\r\n\tinside()\r\n}\r\nif x {\r\n\tinside()\r\n}\r\n"
	if loose, _ := lenientRecountRaw(contentB, oldA, "crlf-converted"); loose != 2 {
		t.Errorf("crlf-converted: true collision, want 2, got %d", loose)
	}

	// Scenario B: numbered file. Block at line 10 loses its body indent on
	// stripping ("  11\tinside()" -> "inside()"); block at line 20 keeps one
	// tab ("  21\t\tinside()" -> "\tinside()"). Stripped probe matches only
	// the line-20 block.
	contentC := "  10\tif x {\n  11\tinside()\n  12\t}\n\n  20\tif y {\n  21\t\tinside()\n  22\t}\n"
	if loose, _ := lenientRecountRaw(contentC, "  20\tif y {\n\tinside()\n}", "line-numbers-stripped"); loose != 1 {
		t.Errorf("line-numbers-stripped: unique under byte semantics, want 1, got %d", loose)
	}

	// True collision under stripping semantics: both blocks identical after
	// prefix removal -> 2.
	contentD := "  10\tif x {\n  11\t\tinside()\n  12\t}\n  20\tif x {\n  21\t\tinside()\n  22\t}\n"
	if loose, _ := lenientRecountRaw(contentD, "  10\tif x {\n\tinside()\n}", "line-numbers-stripped"); loose != 2 {
		t.Errorf("line-numbers-stripped: true collision, want 2, got %d", loose)
	}
}
