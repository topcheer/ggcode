package agent

import "testing"

// #2555 probe: MustXxx exemption + regression control
func TestProbe2555MustExemption(t *testing.T) {
	// Case 1: MustXxx helper with bare panic -> must be exempted now
	mustSrc := `package p
func MustCompilePattern(s string) { if s == "" { panic("empty") } }`
	if got := len(findBarePanics(mustSrc)); got != 0 {
		t.Errorf("MustXxx should be exempted, got %d warnings", got)
	}
	// Case 2: ordinary function bare panic -> still flagged (regression control)
	plainSrc := `package p
func loadThing(s string) { if s == "" { panic("empty") } }`
	if got := len(findBarePanics(plainSrc)); got != 1 {
		t.Errorf("ordinary bare panic must still be flagged, got %d warnings", got)
	}
	// Case 3: MustXxx closure (GenDecl FuncLit) stays flagged per issue note (host name unknowable)
	closureSrc := `package p
var f = func() { panic("boom") }`
	if got := len(findBarePanics(closureSrc)); got != 1 {
		t.Errorf("FuncLit in GenDecl stays flagged (host name unknowable), got %d", got)
	}
}
