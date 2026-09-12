package config

// #2104 regression: a lone double-quote value (`export FOO="` - a
// truncated rc-file line) matched BOTH HasPrefix and HasSuffix, Unquote
// failed, and the #1519 fall-through sliced value[1:0] - a startup panic
// from any shell rc line. The single-quote branch always had the
// len >= 2 guard.

import (
	"testing"
)

func TestParseEnvAssignmentLoneDoubleQuote(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("lone double-quote panicked: %v", r)
		}
	}()
	for _, line := range []string{`export FOO="`, `FOO="`, `FOO=""x`} {
		name, _, ok := parseEnvAssignment(line)
		if !ok && name != "" {
			t.Fatalf("%q: inconsistent result", line)
		}
	}
	// The lone quote is returned raw (mirrors the single-quote branch).
	_, v, ok := parseEnvAssignment(`FOO="`)
	if !ok || v != `"` {
		t.Fatalf(`FOO=" must return the raw lone quote, got ok=%v v=%q`, ok, v)
	}
	// Sanity: normal quoted values still unquote.
	_, v, ok = parseEnvAssignment(`FOO="bar"`)
	if !ok || v != "bar" {
		t.Fatalf("quoted value must unquote, got ok=%v v=%q", ok, v)
	}
	// And the single-quote twin does not panic either.
	_, v, ok = parseEnvAssignment(`FOO='`)
	if !ok || v != `'` {
		t.Fatalf(`FOO=' must return raw, got ok=%v v=%q`, ok, v)
	}
}
