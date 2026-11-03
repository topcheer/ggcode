package agent

import "testing"

func TestProbe2555MustExemption(t *testing.T) {

	mustSrc := `package p
func MustCompilePattern(s string) { if s == "" { panic("empty") } }`
	if got := len(findBarePanics(mustSrc)); got != 0 {
		t.Errorf("MustXxx should be exempted, got %d warnings", got)
	}

	plainSrc := `package p
func loadThing(s string) { if s == "" { panic("empty") } }`
	if got := len(findBarePanics(plainSrc)); got != 1 {
		t.Errorf("ordinary bare panic must still be flagged, got %d warnings", got)
	}

	closureSrc := `package p
var f = func() { panic("boom") }`
	if got := len(findBarePanics(closureSrc)); got != 1 {
		t.Errorf("FuncLit in GenDecl stays flagged (host name unknowable), got %d", got)
	}
}
