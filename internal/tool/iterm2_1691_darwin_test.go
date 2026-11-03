//go:build darwin

package tool

import (
	"strings"
	"testing"
)

func TestIterm2MenuScriptHasNoGoEscapes1691(t *testing.T) {

	got := escapeAS("menu\x1bitem")
	if strings.Contains(got, "\\x1b") || strings.Contains(got, "\\u") {
		t.Fatalf("escapeAS must not emit Go escapes: %q", got)
	}
}
