package commands

// #3515 probe: replaceVar's old passthrough re-encoded each raw byte as a
// rune code point (`result += string(s[i])`), turning 3-byte Chinese UTF-8
// into 6-byte Latin-1 mojibake. Bytes must pass through verbatim.

import "testing"

func TestIssue3515_ReplaceVarPreservesUTF8(t *testing.T) {
	// The issue's exact repro: Chinese text around an ASCII variable.
	in := "修复 $FILE 文件"
	want := "修复 x.go 文件"
	if got := replaceVar(in, "$FILE", "x.go"); got != want {
		t.Fatalf("replaceVar broke UTF-8: got %q want %q", got, want)
	}
	// Multiple substitutions inside multi-byte content.
	in2 := "先 $A 后 中 $A 尾"
	want2 := "先 1 后 中 1 尾"
	if got := replaceVar(in2, "$A", "1"); got != want2 {
		t.Fatalf("multi-substitution broke UTF-8: got %q want %q", got, want2)
	}
	// A multi-byte value is inserted intact too.
	if got := replaceVar("a $K b", "$K", "中文"); got != "a 中文 b" {
		t.Fatalf("multi-byte value corrupted: got %q", got)
	}
	// Pure ASCII behavior is unchanged (no key, key absent, key at edges).
	if got := replaceVar("no vars here", "$X", "v"); got != "no vars here" {
		t.Fatalf("ascii passthrough changed: %q", got)
	}
	if got := replaceVar("$K tail", "$K", "v"); got != "v tail" {
		t.Fatalf("leading key changed: %q", got)
	}
	if got := replaceVar("head $K", "$K", "v"); got != "head v" {
		t.Fatalf("trailing key changed: %q", got)
	}
}

func TestIssue3515_ExpandChineseTemplate(t *testing.T) {
	// End-to-end: Command.Expand drives replaceVar; a Chinese custom
	// command template must survive variable substitution.
	c := &Command{
		Name:     "fix",
		Template: "修复 $ARG 问题文件",
	}
	got := c.Expand(map[string]string{"ARG": "main.go"})
	want := "修复 main.go 问题文件"
	if got != want {
		t.Fatalf("Expand broke Chinese template: got %q want %q", got, want)
	}
}
