//go:build goolm

package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestIssue4455_MacroRunAcceptsNumberAndBoolArgs(t *testing.T) {

	raw := json.RawMessage(`[3, true, "text"]`)
	var in []any
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	var args []string
	for _, v := range in {
		switch v.(type) {
		case string, float64, bool, nil:
			args = append(args, fmt.Sprint(v))
		default:
			t.Fatalf("scalar %v rejected", v)
		}
	}
	out, err := expandMacroArgs(json.RawMessage(`{"limit": "{{1}}", "flag": "{{2}}", "name": "{{3}}"}`), args)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{`"limit": "3"`, `"flag": "true"`, `"name": "text"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized scalar missing: want %q in %s", want, got)
		}
	}
}

func TestIssue4455_MacroExpandSinglePassNoReinjection(t *testing.T) {

	out, err := expandMacroArgs(json.RawMessage(`"{{1}}"`), []string{"{{2}}", "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != `"{{2}}"` {
		t.Fatalf("literal {{2}} in arg value must survive untouched, got %s (was re-injected: old sequential bug #4455)", got)
	}

	out2, err := expandMacroArgs(json.RawMessage(`"{{1}} {{7}}"`), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out2); got != `"a {{7}}"` {
		t.Fatalf("unsupplied placeholder must stay literal, got %s", got)
	}
}
