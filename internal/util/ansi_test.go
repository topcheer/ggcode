package util

import "testing"

func TestStripANSI_BasicColors(t *testing.T) {
	input := "\x1b[32msuccess\x1b[0m"
	got := StripANSI(input)
	want := "success"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_MultiParamColor(t *testing.T) {
	input := "\x1b[1;33;40mwarning\x1b[0m"
	got := StripANSI(input)
	want := "warning"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_CursorMovement(t *testing.T) {
	input := "\x1b[H\x1b[2Jclear screen"
	got := StripANSI(input)
	want := "clear screen"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_MixedInText(t *testing.T) {
	input := "line1\n\x1b[31merror\x1b[0m: something\nline3"
	got := StripANSI(input)
	want := "line1\nerror: something\nline3"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_NoEscapes(t *testing.T) {
	input := "plain text without any escapes"
	got := StripANSI(input)
	if got != input {
		t.Errorf("StripANSI(%q) = %q, want unchanged", input, got)
	}
}

func TestStripANSI_EmptyString(t *testing.T) {
	got := StripANSI("")
	if got != "" {
		t.Errorf("StripANSI(\"\") = %q, want empty", got)
	}
}

func TestStripANSI_OnlyEscape(t *testing.T) {
	input := "\x1b[0m"
	got := StripANSI(input)
	want := ""
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_OSCTerminalTitle(t *testing.T) {
	input := "\x1b]0;My Terminal Title\x07actual output"
	got := StripANSI(input)
	want := "actual output"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_OSCTerminalTitleEscBackslash(t *testing.T) {
	input := "\x1b]2;title\x1b\\actual output"
	got := StripANSI(input)
	want := "actual output"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_CharsetDesignation(t *testing.T) {
	input := "\x1b(Btext"
	got := StripANSI(input)
	want := "text"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_PrivateMode(t *testing.T) {
	input := "\x1b[?25lhidden cursor\x1b[?25h"
	got := StripANSI(input)
	want := "hidden cursor"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_RealWorldGradleOutput(t *testing.T) {
	input := "\x1b[32m> Task :compileJava\x1b[0m\n\x1b[33m> Task :processResources\x1b[0m\nBUILD SUCCESSFUL"
	got := StripANSI(input)
	want := "> Task :compileJava\n> Task :processResources\nBUILD SUCCESSFUL"
	if got != want {
		t.Errorf("StripANSI() = %q, want %q", got, want)
	}
}

func TestStripANSI_KeypadMode(t *testing.T) {
	input := "\x1b=keypad on"
	got := StripANSI(input)
	want := "keypad on"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestStripANSI_PreserveNewlinesAndTabs(t *testing.T) {
	input := "\tcol1\tcol2\n\x1b[36mdata\x1b[0m\tval"
	got := StripANSI(input)
	want := "\tcol1\tcol2\ndata\tval"
	if got != want {
		t.Errorf("StripANSI(%q) = %q, want %q", input, got, want)
	}
}

func TestContainsESC(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"plain text", false},
		{"", false},
		{"\x1b[0m", true},
		{"text\x1b[32mmore", true},
		{"just \x1b char", true},
	}
	for _, tt := range tests {
		got := containsESC(tt.input)
		if got != tt.want {
			t.Errorf("containsESC(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
