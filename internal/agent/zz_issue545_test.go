package agent

import "testing"

// Issue #545: HTML5 optional-end-tag implicit closing. Valid concise HTML5
// (minifier output, template engines, terse hand-writing) must not be
// reported as unbalanced. Three probe scenarios from the issue, plus
// negatives: JSX/XHTML stay strict, genuine mismatches still flagged.

func TestIssue545HTML5ImplicitListItems(t *testing.T) {
	if got := checkTagBalance("list.html", `<ul><li>one<li>two</ul>`); got != "" {
		t.Errorf("legal implicit <li> closing flagged: %s", got)
	}
}

func TestIssue545HTML5ImplicitTableCells(t *testing.T) {
	if got := checkTagBalance("table.html", `<table><tr><td>a<td>b</table>`); got != "" {
		t.Errorf("legal implicit <td> closing flagged: %s", got)
	}
	if got := checkTagBalance("rows.html", `<table><tr><td>a<tr><td>b</table>`); got != "" {
		t.Errorf("legal implicit <td>/<tr> chain closing flagged: %s", got)
	}
}

func TestIssue545HTML5SiblingParagraphs(t *testing.T) {
	if got := checkTagBalance("p.html", `<p>one<p>two`); got != "" {
		t.Errorf("legal trailing optional <p> siblings flagged: %s", got)
	}
	if got := checkTagBalance("pdiv.html", `<div><p>text</div>`); got != "" {
		t.Errorf("legal </div> closing an open <p> flagged: %s", got)
	}
}

func TestIssue545DefinitionListsAndOptions(t *testing.T) {
	if got := checkTagBalance("dl.html", `<dl><dt>term<dd>def<dt>t2<dd>d2</dl>`); got != "" {
		t.Errorf("legal implicit <dt>/<dd> closing flagged: %s", got)
	}
	if got := checkTagBalance("sel.html", `<select><option>a<option>b</select>`); got != "" {
		t.Errorf("legal implicit <option> closing flagged: %s", got)
	}
}

func TestIssue545GenuineMismatchesStillFlagged(t *testing.T) {
	// </ol> against an open <ul> is a mismatch even with optional li popped.
	if got := checkTagBalance("bad.html", `<ul><li>one</ol>`); got == "" {
		t.Error("ul/ol mismatch not flagged")
	}
	// Non-optional elements must never be implicitly closed.
	if got := checkTagBalance("bad2.html", `<div><span></div>`); got == "" {
		t.Error("div/span mismatch not flagged")
	}
	if got := checkTagBalance("bad3.html", `<div><p>a</span>`); got == "" {
		t.Error("stray </span> not flagged")
	}
	// A genuinely unclosed container still reports.
	if got := checkTagBalance("bad4.html", `<div><p>text`); got == "" {
		t.Error("unclosed <div> not flagged")
	}
}

func TestIssue545JSXAndXHTMLStayStrict(t *testing.T) {
	// JSX requires explicit closing tags even for HTML-named elements.
	if got := checkTagBalance("List.jsx", `<ul><li>one<li>two</ul>`); got == "" {
		t.Error("JSX implicit <li> closing must stay strict (not flagged)")
	}
	// XHTML is XML: strict pairing required.
	if got := checkTagBalance("page.xhtml", `<ul><li>one<li>two</ul>`); got == "" {
		t.Error("XHTML implicit <li> closing must stay strict (not flagged)")
	}
	// XML family unchanged.
	if got := checkTagBalance("feed.xml", `<item>a<item>b</channel>`); got == "" {
		t.Error("XML implicit closing must stay strict (not flagged)")
	}
}
