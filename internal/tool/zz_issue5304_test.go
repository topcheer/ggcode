package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #5304: the frame wrapper must evaluate the expression as a PURE
// EXPRESSION (value returned) with document/window shadowed to the
// iframe's content document - not wrap it in new Function as a body
// (which statement-evaluates, discards the value, and always returned
// undefined: silent false results).
func TestFrameScopeJSPureExpression(t *testing.T) {
	wrapped := frameScopeJS("iframe#content", "document.querySelectorAll(\"a\").length")

	if strings.Contains(wrapped, "new Function") {
		t.Fatal("wrapper must not use new Function: it evaluates the expression as a function body and always returns undefined")
	}
	if !strings.Contains(wrapped, "((document, window) => (") {
		t.Fatal("wrapper must shadow document/window via an arrow-function parameter list so bare identifiers resolve to the iframe's realm")
	}
	if !strings.Contains(wrapped, `document.querySelectorAll("a").length`) {
		t.Fatal("expression must be embedded verbatim")
	}
	if !strings.Contains(wrapped, "iframe not found: iframe#content") {
		t.Fatal("wrapper must throw a descriptive error when the iframe selector matches nothing")
	}
	if !strings.Contains(wrapped, "cannot access iframe contentDocument") {
		t.Fatal("wrapper must throw a descriptive error on cross-origin frames")
	}
	if !strings.Contains(wrapped, "const w = d.defaultView") || !strings.Contains(wrapped, "const d = f.contentDocument") {
		t.Fatal("window must bind to the iframe's contentDocument.defaultView")
	}
}

func execBrowserAction(t *testing.T, action string, extra map[string]any) (string, bool) {
	t.Helper()
	input := map[string]any{"action": action, "description": "unit"}
	for k, v := range extra {
		input[k] = v
	}
	raw, _ := json.Marshal(input)
	res, err := NewBrowser().Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("execute %s: %v", action, err)
	}
	return res.Content, res.IsError
}

// #5304: frame used to be silently ignored for click/type while the schema
// promised support - now it must fail loudly instead of acting on the main
// frame's elements.
func TestBrowserClickTypeFrameExplicitError(t *testing.T) {
	content, isErr := execBrowserAction(t, "click", map[string]any{"selector": "#x", "frame": "iframe#f"})
	if !isErr {
		t.Fatalf("click+frame must be an explicit error, got success: %s", content)
	}
	if !strings.Contains(content, "frame is not supported for click") {
		t.Fatalf("click+frame error must name the limitation, got: %s", content)
	}

	content, isErr = execBrowserAction(t, "type", map[string]any{"selector": "#x", "text": "hi", "frame": "iframe#f"})
	if !isErr {
		t.Fatalf("type+frame must be an explicit error, got success: %s", content)
	}
	if !strings.Contains(content, "frame is not supported for type") {
		t.Fatalf("type+frame error must name the limitation, got: %s", content)
	}
}

func TestBrowserExtractFrameRequiresSelector(t *testing.T) {
	content, isErr := execBrowserAction(t, "extract", map[string]any{"frame": "iframe#f"})
	if !isErr {
		t.Fatalf("extract+frame without selector must be an explicit error, got success: %s", content)
	}
	if !strings.Contains(content, "frame extraction requires a CSS selector") {
		t.Fatalf("unexpected error content: %s", content)
	}
}
