package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #5304: evaluate with a frame selector must (1) treat the expression as an
// expression (return its value, not undefined) and (2) resolve bare
// `document` to the IFRAME document, not the main frame. The old wrapper
// used new Function(expression), which silently returned undefined for
// expression-style inputs and resolved bare document to the main frame.
func TestIssue5304EvaluateFrameExpressionSemantics(t *testing.T) {
	b, err := filepath.Abs("browser.go")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	src, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("browser.go not readable: %v", err)
	}
	s := string(src)

	// (1) expression semantics: the wrapper must eval the expression in the
	// iframe window via indirect eval (w.eval), which returns the value.
	if !strings.Contains(s, "return w.eval(") {
		t.Error("#5304: frame evaluate must run the expression as an indirect eval in the iframe window (return w.eval(...)), not as a new Function body that always yields undefined")
	}
	// statement-body fallback must exist for statement-style inputs
	if !strings.Contains(s, "e instanceof SyntaxError") {
		t.Error("#5304: frame evaluate must keep a statement-body fallback on SyntaxError")
	}
	// (2) bare document must resolve to the iframe: the old form called
	// fn.call(defaultView, contentDocument) with new Function, whose bare
	// document still hit the main frame. The fixed wrapper must not use
	// new Function as the primary path.
	if strings.Contains(s, "const fn = new Function(%q);") && !strings.Contains(s, "w.eval(%q)") {
		t.Error("#5304: new Function must not be the primary evaluation path (bare document resolved to the main frame)")
	}
	// cross-origin guard retained (same-origin only contract)
	if !strings.Contains(s, "cannot access iframe contentDocument (cross-origin?)") {
		t.Error("#5304: cross-origin guard on contentDocument must be retained")
	}
}

// Minimal JS-level reproduction of the semantics: new Function on a bare
// expression yields undefined; eval on the same text yields the value.
func TestIssue5304FunctionVsEvalSemantics(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	script := `const f = new Function("1 + 2"); const a = f.call({}, {});
const e = eval("1 + 2");
console.log(JSON.stringify({fnBody: String(a), evalExpr: e}));`
	out, err := exec.Command(node, "-e", script).Output()
	if err != nil {
		t.Fatalf("node exec: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"fnBody":"undefined"`) || !strings.Contains(got, `"evalExpr":3`) {
		t.Fatalf("#5304 semantics probe failed: new Function(expr) must be undefined, eval(expr) must be 3; got %s", got)
	}
}
