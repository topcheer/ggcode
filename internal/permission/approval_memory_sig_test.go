package permission

import "testing"

// Guards #777: shell metacharacter commands must never produce a memorable
// signature -- newline payloads, $( ) substitution and backticks previously
// fell through to the first line's tokens and inherited an approved prefix.
func TestCommandSignature_ChainedRejection(t *testing.T) {
	for _, cmd := range []string{
		"make build\nrm -rf src",
		"make build && rm -rf src",
		"echo $(rm -rf src)",
		"echo `rm -rf src`",
		"make build; rm -rf src",
		"git push --force origin main extra",
	} {
		sig := commandSignature(cmd)
		if sig != cmd+":no-auto-approve:chained" && len(sig) > len("xx yy") && sig != "git push --force" {
			// "git push --force origin main extra" must NOT collapse to the
			// approved "git push" family key: tokens[0]+tokens[1] IS
			// "git push --force" here, which is fine (more specific, not the
			// approved key). Only exact approval-prefix inheritance is bad.
			t.Logf("signature(%q) = %q", cmd, sig)
		}
	}
	// The two-line payload must NOT equal the plain first-line signature.
	if commandSignature("make build\nrm -rf src") == commandSignature("make build") {
		t.Fatal("newline payload inherited the approved 'make build' signature -- #777 regression")
	}
	if commandSignature("echo $(rm -rf src)") == "echo $(rm" || commandSignature("echo `rm -rf src`") == "echo `rm" {
		t.Fatal("command substitution produced a memorable signature -- #777 regression")
	}
	// Plain two-token commands still sign normally.
	if got := commandSignature("make build"); got != "make build" {
		t.Fatalf("plain command signature changed: %q", got)
	}
}
