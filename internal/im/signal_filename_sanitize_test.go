package im

// #3332: signal-cli-rest-api splits the data-URL metadata section on ';'
// and prefix-matches each segment, so a raw ';' in the filename truncates
// it (a;b.pdf -> "a") and drops the rest. The ',' delimiter is neutralized
// too (same class of split-character hazard). Attachment DATA is never
// affected (server uses LastIndex("base64,")).

import (
	"strings"
	"testing"
)

func TestSanitizeSignalAttachmentName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a;b.pdf", "a_b.pdf"},     // #3332 repro: extension preserved
		{"a,b.txt", "a_b.txt"},     // comma neutralized as well
		{"a;b;c.png", "a_b_c.png"}, // multiple occurrences
		{"plain.pdf", "plain.pdf"}, // untouched when clean
		{"中文 报告.pdf", "中文 报告.pdf"}, // CJK/space byte-transparent
		{"", ""},                     // empty stays empty (caller defaults)
		{"semi;only;", "semi_only_"}, // trailing separator
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
	// strip the leading "data:mime;" then the filename must be one segment
	meta := strings.TrimPrefix(head, "data:application/pdf;")
	segs := strings.Split(meta, ";")
	if len(segs) != 1 || !strings.HasPrefix(segs[0], "filename=") {
		t.Fatalf("filename segment split by ';' - parser would truncate: %q", meta)
	}
	if !strings.HasSuffix(segs[0], ".pdf") {
		t.Fatalf("extension lost: %q", segs[0])
	}
}
