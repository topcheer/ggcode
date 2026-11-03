package agent

import "testing"

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

	if got := checkTagBalance("bad.html", `<ul><li>one</ol>`); got == "" {
		t.Error("ul/ol mismatch not flagged")
	}

	if got := checkTagBalance("bad2.html", `<div><span></div>`); got == "" {
		t.Error("div/span mismatch not flagged")
	}
	if got := checkTagBalance("bad3.html", `<div><p>a</span>`); got == "" {
		t.Error("stray </span> not flagged")
	}

	if got := checkTagBalance("bad4.html", `<div><p>text`); got == "" {
		t.Error("unclosed <div> not flagged")
	}
}

func TestIssue545JSXAndXHTMLStayStrict(t *testing.T) {

	if got := checkTagBalance("List.jsx", `<ul><li>one<li>two</ul>`); got == "" {
		t.Error("JSX implicit <li> closing must stay strict (not flagged)")
	}

	if got := checkTagBalance("page.xhtml", `<ul><li>one<li>two</ul>`); got == "" {
		t.Error("XHTML implicit <li> closing must stay strict (not flagged)")
	}

	if got := checkTagBalance("feed.xml", `<item>a<item>b</channel>`); got == "" {
		t.Error("XML implicit closing must stay strict (not flagged)")
	}
}
