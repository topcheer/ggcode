package config

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

	_, v, ok := parseEnvAssignment(`FOO="`)
	if !ok || v != `"` {
		t.Fatalf(`FOO=" must return the raw lone quote, got ok=%v v=%q`, ok, v)
	}

	_, v, ok = parseEnvAssignment(`FOO="bar"`)
	if !ok || v != "bar" {
		t.Fatalf("quoted value must unquote, got ok=%v v=%q", ok, v)
	}

	_, v, ok = parseEnvAssignment(`FOO='`)
	if !ok || v != `'` {
		t.Fatalf(`FOO=' must return raw, got ok=%v v=%q`, ok, v)
	}
}
