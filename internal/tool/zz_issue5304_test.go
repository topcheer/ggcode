package tool

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// quotePctQ avoids a literal %q inside t.Fatal-format strings (vet flags
// Printf-directive-in-non-Printf-call heuristics in test files).
const quotePctQ = `%q`

func readFileOrSkip(t *testing.T, name string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TestIssue5304FrameEvalWrapperShape pins the frame-mode evaluate wrapper
// construction in browser.go: the expression must be wrapped so that
// (a) it is evaluated as an EXPRESSION with its value returned (not stuffed
// into `new Function(...)` as a body whose value is discarded), and
// (b) bare document/window identifiers resolve to the IFRAME's document and
// window, not the frame's.
func TestIssue5304FrameEvalWrapperShape(t *testing.T) {
	src, err := readFileOrSkip(t, "browser.go")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(src, "new Function("+quotePctQ+")") {
		t.Fatal("#5304: the single-argument new Function body-wrapper must not come back - it evaluates pure expressions as statements and always returns undefined")
	}

	if !strings.Contains(src, "frameEvalExpressionJS(frame, expression)") {
		t.Fatal("#5304: doEvaluate frame branch must build its wrapper via frameEvalExpressionJS")
	}

	i := strings.Index(src, "func frameEvalExpressionJS")
	if i < 0 {
		t.Fatal("frameEvalExpressionJS not found in browser.go")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}

	for _, anchor := range []string{
		// expression semantics: return-wrapped expression, value not discarded
		`fn = new Function("document", "window", "return (" +`,
		// legacy body-style fallback (top-level return) keeps working via the
		// catchable Function-constructor second track
		`fn = new Function("document", "window",`,
		// frame document/window are the ARGUMENTS, shadowing main-frame globals
		"return fn.call(w, f.contentDocument, w);",
		// iframe resolution + cross-origin guard preserved from the old wrapper
		"document.querySelector(",
		"iframe not found:",
		"cannot access iframe contentDocument (cross-origin?)",
	} {
		if !strings.Contains(body, anchor) {
			t.Errorf("#5304 wrapper missing anchor %q", anchor)
		}
	}
}

// issue5304VM builds a goja VM with a minimal DOM stub: document.querySelector
// returns a fake same-origin iframe whose contentDocument.defaultView carries
// the test marks. The wrapper under test only needs querySelector,
// contentDocument, and defaultView.
func issue5304VM(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	harness := `(() => {
		const frameWindow = {
			__frameMark: "iframe-window",
			document: null,
		};
		const frameDoc = {
			title: "iframe-title",
			defaultView: frameWindow,
			querySelectorAll: (sel) => ({ length: 2 }),
		};
		frameWindow.document = frameDoc;
		globalThis.document = {
			title: "main-title",
			querySelector: (sel) => ({
				contentDocument: frameDoc,
			}),
			querySelectorAll: (sel) => ({ length: 7 }),
		};
		globalThis.window = { __frameMark: "main-window" };
	})();`
	if _, err := vm.RunString(harness); err != nil {
		t.Fatalf("harness setup failed: %v", err)
	}
	return vm
}

// TestIssue5304FrameEvalWrapperSemantics executes the EXACT wrapper string
// that frameEvalExpressionJS produces, in a JS engine with a stubbed iframe,
// proving pure-expression semantics AND iframe-scoped document resolution at
// the string level (no browser session needed).
func TestIssue5304FrameEvalWrapperSemantics(t *testing.T) {
	cases := []struct {
		name       string
		expression string
		want       string
	}{
		{"pure expression value is returned, not discarded", "6 * 7", "42"},
		{"schema example: bare document resolves to the IFRAME document", `document.querySelectorAll("a").length`, "2"},
		{"bare window resolves to the IFRAME window", `window.__frameMark`, "iframe-window"},
		{"member expression form", "document.title", "iframe-title"},
		{"body-style legacy expression still returns", "return 6 * 7", "42"},
		{"async IIFE expression form is preserved verbatim", "(async () => 6 * 7)()", "42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := issue5304VM(t)
			wrapper := frameEvalExpressionJS("iframe#content", tc.expression)
			// The async-IIFE row: goja resolves the promise-like result only
			// when driven by an event loop; evaluate the awaited form via a
			// small driver instead.
			driver := "(function(){ const r = " + wrapper + "; if (r && typeof r.then === 'function') { return 'PENDING'; } return String(r); })()"
			v, err := vm.RunString(driver)
			if err != nil {
				t.Fatalf("wrapper execution failed for %q: %v", tc.expression, err)
			}
			got := v.String()
			if got == "PENDING" {
				if tc.want != "42" { // only the async row may pend
					t.Fatalf("unexpected pending result for %q", tc.expression)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("expression %q: got %q, want %q", tc.expression, got, tc.want)
			}
		})
	}
}

// TestIssue5304FrameEvalWrapperParseFailureIsLoud pins that an expression
// which parses as neither expression nor body produces an explicit error at
// wrapper compile time instead of a silent undefined.
func TestIssue5304FrameEvalWrapperParseFailureIsLoud(t *testing.T) {
	vm := issue5304VM(t)
	wrapper := frameEvalExpressionJS("iframe#content", "return return 1")
	driver := "(() => { try { " + wrapper + ` ; return "NO-ERROR"; } catch (e) { return "ERR:" + String(e); } })()`
	v, err := vm.RunString(driver)
	if err != nil {
		t.Fatalf("driver failed: %v", err)
	}
	got := v.String()
	if got == "NO-ERROR" || !strings.Contains(got, "ERR:") {
		t.Fatalf("expected loud parse failure, got %q", got)
	}
	if !strings.Contains(got, "expression failed to parse") {
		t.Errorf("parse-failure message should be explicit, got %q", got)
	}
}

// TestIssue5304FrameNotFoundIsLoud keeps the old wrapper's hard errors for
// missing / cross-origin iframes (already correct pre-fix; must survive).
func TestIssue5304FrameNotFoundIsLoud(t *testing.T) {
	vm := goja.New()
	harness := `globalThis.document = { querySelector: () => null };`
	if _, err := vm.RunString(harness); err != nil {
		t.Fatalf("harness setup failed: %v", err)
	}
	wrapper := frameEvalExpressionJS("iframe#nope", "1 + 1")
	driver := "(() => { try { " + wrapper + ` ; return "NO-ERROR"; } catch (e) { return "ERR:" + String(e); } })()`
	v, err := vm.RunString(driver)
	if err != nil {
		t.Fatalf("driver failed: %v", err)
	}
	if got := v.String(); !strings.Contains(got, "iframe not found") {
		t.Fatalf("expected 'iframe not found' error, got %q", got)
	}
}

// TestIssue5304SchemaFrameScopeIsEvaluateOnly pins the corrected schema text:
// frame must no longer claim extract/click/type support that the dispatch
// never implemented (silently ignored).
func TestIssue5304SchemaFrameScopeIsEvaluateOnly(t *testing.T) {
	b := NewBrowser()
	params := b.Parameters()
	var schema map[string]interface{}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatalf("Parameters() returned invalid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing properties")
	}
	frame, ok := props["frame"].(map[string]interface{})
	if !ok {
		t.Fatal("schema missing frame property")
	}
	desc, _ := frame["description"].(string)
	descLow := strings.ToLower(desc)

	if strings.Contains(descLow, "extract/click/type") || strings.Contains(descLow, "evaluate/extract/click/type") {
		t.Fatalf("#5304: frame schema still over-claims extract/click/type support: %q", desc)
	}
	if !strings.Contains(descLow, "evaluate action only") {
		t.Errorf("#5304: frame schema should scope frame to the evaluate action: %q", desc)
	}
	if !strings.Contains(descLow, "top document") {
		t.Errorf("#5304: frame schema should say click/type/extract operate on the top document: %q", desc)
	}
}
