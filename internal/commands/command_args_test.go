package commands

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"env v2", []string{"env", "v2"}},
		{"  env\tv2  ", []string{"env", "v2"}},
		{`"my env" v2`, []string{"my env", "v2"}},
		{`env "a  b"`, []string{"env", "a  b"}},
		{`a"b c"d e`, []string{"ab cd", "e"}},
		{`"unterminated`, []string{"unterminated"}},
	}
	for _, tc := range cases {
		got := SplitArgs(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitArgs(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestExpandWithArgsPositional(t *testing.T) {
	c := &Command{Template: "deploy $1 to $2 (all: $ARGUMENTS)"}
	got := c.ExpandWithArgs(nil, SplitArgs(`staging "1.2.3" `))
	want := "deploy staging to 1.2.3 (all: staging 1.2.3)"
	if got != want {
		t.Fatalf("ExpandWithArgs = %q, want %q", got, want)
	}
}

func TestExpandWithArgsBracketAndBrace(t *testing.T) {
	c := &Command{Template: "$ARGUMENTS[0] then ${2} end $ARGUMENTS[5]"}
	got := c.ExpandWithArgs(nil, []string{"first", "second"})
	want := "first then second end "
	if got != want {
		t.Fatalf("ExpandWithArgs = %q, want %q", got, want)
	}
}

func TestExpandWithArgsMissingPositionIsEmpty(t *testing.T) {
	c := &Command{Template: "a=$1 b=$2"}
	if got, want := c.ExpandWithArgs(nil, []string{"x"}), "a=x b="; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandWithArgsDollarTenStaysLiteral(t *testing.T) {
	c := &Command{Template: "price $10"}
	if got, want := c.ExpandWithArgs(nil, []string{"x"}), "price $10"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandWithArgsValuesNotReexpanded(t *testing.T) {
	c := &Command{Template: "got $1 and $2"}
	if got, want := c.ExpandWithArgs(nil, []string{"$2", "$1"}), "got $2 and $1"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandWithArgsFixesArgsPrefixCorruption(t *testing.T) {
	// $ARGS is a prefix of $ARGUMENTS: the old map-order replacement could
	// emit "a bUMENTS" when ARGS ran before the untouched $ARGUMENTS text.
	c := &Command{Template: "$ARGS then $ARGUMENTS"}
	got := c.ExpandWithArgs(map[string]string{"ARGS": "a b"}, []string{"x", "y"})
	want := "a b then x y"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandLegacyLeavesPositionalUntouched(t *testing.T) {
	c := &Command{Template: "run $1 with ${2} and $ARGS"}
	got := c.Expand(map[string]string{"ARGS": "x"})
	want := "run $1 with ${2} and x"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandWithArgsEmptyArgListLeavesPositionalUntouched(t *testing.T) {
	c := &Command{Template: "run $1 with $ARGS"}
	got := c.ExpandWithArgs(map[string]string{"ARGS": "x"}, SplitArgs("   "))
	want := "run $1 with x"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExpandWithArgsNoDollarShortCircuit(t *testing.T) {
	c := &Command{Template: "plain template"}
	if got, want := c.ExpandWithArgs(nil, []string{"a"}), "plain template"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
