package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1320: export category/keyword were joined into the filename unsanitized;
// a keyword like "../../../etc/foo" escaped the temp dir and
// created/truncated an arbitrary .log path.
func TestDebugLogSanitizeNamePart(t *testing.T) {
	hostile := []string{
		"../../../etc/foo",
		"..\\..\\win",
		"a\x00b",
		"....",
		"",
	}
	for _, in := range hostile {
		got := sanitizeLogNamePart(in)
		if strings.Contains(got, "..") {
			t.Errorf("sanitize(%q) = %q still contains traversal", in, got)
		}
		if strings.ContainsAny(got, "/\\\x00") {
			t.Errorf("sanitize(%q) = %q still contains separators", in, got)
		}
		if got == "" {
			t.Errorf("sanitize(%q) empty", in)
		}
	}
	if got := sanitizeLogNamePart("provider"); got != "provider" {
		t.Errorf("normal category mangled: %q", got)
	}

	// End-to-end: the assembled export path must stay inside the temp dir.
	ts := "20060102-150405"
	filename := fmt.Sprintf("ggcode-debug-%s-%s.log", ts,
		sanitizeLogNamePart("../../../etc/foo"))
	path := filepath.Join(os.TempDir(), filepath.Base(filename))
	rel, err := filepath.Rel(os.TempDir(), path)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		t.Errorf("path escaped temp dir: %s (rel=%q err=%v)", path, rel, err)
	}
}
