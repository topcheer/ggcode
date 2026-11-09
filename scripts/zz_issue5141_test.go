package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// zz_issue5141: static-half drift guard.
//
// Face B of #5141: #5127 hand-added LookupSyncedContextWindow inside the
// GENERATED internal/config/context_window.go. The sync workflow regenerates
// the file every run (make sync-model-caps first), which silently wiped the
// hand-added function and broke the companion test's build — locally green
// (no regen ran), CI red (regen every run).
//
// This guard pins the issue's acceptance item: "assert committed static
// half == template output". The generator is //go:build ignore (cannot be
// imported per the #2857 precedent), so we evaluate the staticCode()
// concatenated raw-string expression FROM SOURCE TEXT and require the
// committed generated file to END WITH that evaluated template output,
// byte-for-byte. generateGoCode emits header + dynamic table + staticCode()
// as the tail. Directionality:
//   - hand-editing the generated file (Face B shape: appending a function
//     after the static half, or editing inside it) breaks the suffix match
//     -> red immediately, forcing hand code into context_window_lookup.go;
//   - editing the template without re-running sync leaves the committed
//     tail at the OLD static half -> red until regen is committed.
func TestGeneratedContextWindowStaticHalfMatchesTemplate(t *testing.T) {
	tmplSrc, err := os.ReadFile("sync-model-caps.go")
	if err != nil {
		t.Fatalf("read generator source: %v", err)
	}
	want, err := evalStaticCodeFromSource(string(tmplSrc))
	if err != nil {
		t.Fatalf("extract template staticCode(): %v", err)
	}
	if strings.TrimSpace(want) == "" {
		t.Fatal("template staticCode() evaluated to empty - guard cannot pin an empty tail")
	}
	committed, err := os.ReadFile("../internal/config/context_window.go")
	if err != nil {
		t.Fatalf("read committed generated file: %v", err)
	}
	got := string(committed)
	if !strings.HasSuffix(got, want) {
		overlap := 0
		maxCmp := len(got)
		if len(want) < maxCmp {
			maxCmp = len(want)
		}
		for overlap < maxCmp && got[len(got)-1-overlap] == want[len(want)-1-overlap] {
			overlap++
		}
		t.Errorf("committed context_window.go static half drifted from template staticCode(): last %d bytes match then diverge (committed len=%d, template len=%d). Either the generated file was hand-edited (move hand code to context_window_lookup.go) or the template changed without re-running `make sync-model-caps` (regen and commit).",
			overlap, len(got), len(want))
	}

	// Face B replay (in-memory): hand-adding a function after the static
	// half is exactly the #5127 shape that regen silently wiped - this
	// guard must break on it, proving the suffix pin actually bites.
	if strings.HasSuffix(got+"\nfunc handAddedFn() {}\n", want) {
		t.Error("guard invariant too weak: appending hand code after the static half would still satisfy the suffix match")
	}
}

// evalStaticCodeFromSource evaluates the single return expression of
// staticCode() in the generator source. The expression is a concatenation
// of raw string literals and "`"-only interpreted literals (the escaping
// trick for embedding backticks); anything else is a guard-breaking shape
// and fails loudly.
func evalStaticCodeFromSource(src string) (string, error) {
	const marker = "func staticCode() string {"
	idx := strings.Index(src, marker)
	if idx < 0 {
		return "", errOf("staticCode() declaration not found in generator source")
	}
	rest := src[idx+len(marker):]
	ret := strings.Index(rest, "return ")
	if ret < 0 {
		return "", errOf("staticCode() has no return expression")
	}
	expr := rest[ret+len("return "):]

	var sb strings.Builder
	i := 0
	for i < len(expr) {
		c := expr[i]
		switch {
		case c == '`':
			j := strings.IndexByte(expr[i+1:], '`')
			if j < 0 {
				return "", errOf("unterminated raw string literal in staticCode()")
			}
			sb.WriteString(expr[i+1 : i+1+j])
			i += j + 2
		case c == '"':
			j := strings.IndexByte(expr[i+1:], '"')
			if j < 0 {
				return "", errOf("unterminated interpreted literal in staticCode()")
			}
			lit := expr[i+1 : i+1+j]
			if lit != "`" {
				return "", errOf("staticCode() template shape changed: interpreted literal %q is not the backtick-escape form - update the guard's evaluator", lit)
			}
			sb.WriteString("`")
			i += j + 2
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '+':
			i++
		case c == '}':
			// Function closing brace terminates the return expression. Raw
			// literals inside the template contain top-level '}' bytes of their
			// own, but those are consumed by the backquote scan above - an
			// unquoted '}' can only be the function boundary.
			return sb.String(), nil
		default:
			return "", errOf("staticCode() template shape changed: unexpected character %q at offset %d - update the guard's evaluator", string(c), i)
		}
	}
	return sb.String(), nil
}

func errOf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
