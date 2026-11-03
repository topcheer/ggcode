//go:build darwin

package image

import (
	"strings"
	"testing"
)

func TestIssue2999_ParseNSScreenOutput(t *testing.T) {
	out := strings.Join([]string{
		"1\t1\t0\t0\t1512\t982\tBuilt-in Retina Display",
		"2\t0\t-1920\t1512\t1920\t1080\tExternal",
	}, "\n")
	displays := parseNSScreenOutput(out)
	if len(displays) != 2 {
		t.Fatalf("expected 2 displays, got %d", len(displays))
	}
	d1 := displays[0]
	if !d1.IsPrimary || d1.Index != 1 || d1.X != 0 || d1.Y != 0 {
		t.Fatalf("primary display fields wrong: %+v", d1)
	}

	if d1.Width != 1512 || d1.Height != 982 {
		t.Fatalf("logical width/height must be kept verbatim, got %dx%d", d1.Width, d1.Height)
	}
	if d1.Name != "Built-in Retina Display" {
		t.Fatalf("name not parsed: %q", d1.Name)
	}

	d2 := displays[1]
	if d2.IsPrimary {
		t.Fatal("second display must not be primary")
	}
	if d2.X != -1920 || d2.Y != 1512 || d2.Width != 1920 || d2.Height != 1080 {
		t.Fatalf("secondary display coords wrong: %+v", d2)
	}
}

func TestIssue2999_ParseNSScreenOutputSkipsMalformed(t *testing.T) {
	out := "garbage line\n1\t1\t0\t0\t100\t100\tOK\nshort\tline\n"
	displays := parseNSScreenOutput(out)
	if len(displays) != 1 {
		t.Fatalf("expected 1 parsed display, got %d", len(displays))
	}
	if displays[0].Name != "OK" {
		t.Fatalf("wrong survivor: %+v", displays[0])
	}
}

func TestIssue2999_ParseNSScreenOutputEmpty(t *testing.T) {
	if got := parseNSScreenOutput(""); got != nil {
		t.Fatalf("empty input must return nil, got %+v", got)
	}
	if got := parseNSScreenOutput("   \n\n"); got != nil {
		t.Fatalf("whitespace input must return nil, got %+v", got)
	}
}
