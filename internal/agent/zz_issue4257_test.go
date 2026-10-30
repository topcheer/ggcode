package agent

import "testing"

// #4257: pasted fences/quotes/logs must not be promoted to user constraints.
func TestIssue4257_NonDirectiveTextNotExtracted(t *testing.T) {
	pasted := "help me fix this:\n" +
		"```\n2026-10-16 12:00:00 ERROR cannot connect\nnever run this on CI runners\")\n```\n" +
		"> never touch the vendor dir, says the README"
	got := extractConstraints(pasted)
	for _, c := range got {
		if zz4257ContainsAny(c, "cannot connect", "CI runners", "vendor") {
			t.Fatalf("pasted non-directive text extracted as constraint: %q", c)
		}
	}
	// A real directive outside any fence still extracts.
	got = extractConstraints("do not modify the auth module please")
	found := false
	for _, c := range got {
		if zz4257ContainsAny(c, "auth module") {
			found = true
		}
	}
	if !found {
		t.Fatal("genuine user directive must still be extracted")
	}
}

func zz4257ContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) && (s == sub || zz4257Contains(s, sub)) {
			return true
		}
	}
	return false
}

func zz4257Contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
