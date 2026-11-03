package im

import (
	"strings"
	"testing"
)

func TestSanitizeSignalAttachmentName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a;b.pdf", "a_b.pdf"},
		{"a,b.txt", "a_b.txt"},
		{"a;b;c.png", "a_b_c.png"},
		{"plain.pdf", "plain.pdf"},
		{"中文 报告.pdf", "中文 报告.pdf"},
		{"", ""},
		{"semi;only;", "semi_only_"},
	}
	for _, c := range cases {
		if got := sanitizeSignalAttachmentName(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSignalAttachmentURLDelimiters asserts the emitted attachment entry
// has no raw ';' or ',' inside the filename segment — the invariant the
// server-side parser relies on.
func TestSignalAttachmentURLDelimiters(t *testing.T) {
	name := sanitizeSignalAttachmentName("report;final,v2.pdf")
	entry := "data:application/pdf;filename=" + name + ";base64,QUJD"
	head := entry[:strings.LastIndex(entry, ";base64,")]

	meta := strings.TrimPrefix(head, "data:application/pdf;")
	segs := strings.Split(meta, ";")
	if len(segs) != 1 || !strings.HasPrefix(segs[0], "filename=") {
		t.Fatalf("filename segment split by ';' - parser would truncate: %q", meta)
	}
	if !strings.HasSuffix(segs[0], ".pdf") {
		t.Fatalf("extension lost: %q", segs[0])
	}
}
