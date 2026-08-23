package main

import (
	"strings"
	"testing"
)

// TestReportHTMLToolNameEscaping (#933 companion) locks in the XSS fix:
// every t.name innerHTML interpolation in the report template must go
// through esc(String(t.name)). A regression that drops any esc() (as
// happened at toolSummaryList L418 / detailToolList L711 before ca004ecd)
// fails this test.
func TestReportHTMLToolNameEscaping(t *testing.T) {
	tpl := htmlTemplate("{}")

	escaped := strings.Count(tpl, `esc(String(t.name))`)
	if escaped < 3 {
		t.Errorf("expected >=3 escaped t.name sinks (toolSummaryList, detailToolList, slowestTools), got %d", escaped)
	}

	// Any remaining raw interpolation of t.name into an HTML string is a bug.
	for _, raw := range []string{`'+t.name+'`, `"+t.name+"`} {
		if strings.Contains(tpl, raw) {
			t.Errorf("unescaped tool-name innerHTML interpolation %q still present in template", raw)
		}
	}
}
