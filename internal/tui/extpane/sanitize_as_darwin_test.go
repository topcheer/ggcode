//go:build darwin

package extpane

import (
	"strings"
	"testing"
)

func TestSanitizeAS(t *testing.T) {
	s := sanitizeAS(`hello "world" \test`)
	if !strings.Contains(s, `\"`) || !strings.Contains(s, `\\`) {
		t.Errorf("escaping failed: %s", s)
	}
	if strings.Contains(sanitizeAS("a\x01b"), "\x01") {
		t.Error("control char not stripped")
	}
}
