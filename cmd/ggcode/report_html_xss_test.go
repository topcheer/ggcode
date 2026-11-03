package main

import (
	"strings"
	"testing"
)

func TestReportHTMLToolNameEscaping(t *testing.T) {
	tpl := htmlTemplate("{}")

	escaped := strings.Count(tpl, `esc(String(t.name))`)
	if escaped < 3 {
		t.Errorf("expected >=3 escaped t.name sinks (toolSummaryList, detailToolList, slowestTools), got %d", escaped)
	}

	for _, raw := range []string{`'+t.name+'`, `"+t.name+"`} {
		if strings.Contains(tpl, raw) {
			t.Errorf("unescaped tool-name innerHTML interpolation %q still present in template", raw)
		}
	}
}
